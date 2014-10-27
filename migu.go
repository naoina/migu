package migu

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"sort"
	"strings"

	"github.com/naoina/migu/dialect"
)

// Sync synchronizes the schema between Go's struct and the database.
// Go's struct may be provided via the filename of the source file, or via
// the src parameter.
//
// If src != nil, Sync parses the source from src and filename is not used.
// The type of the argument for the src parameter must be string, []byte, or
// io.Reader. If src == nil, Sync parses the file specified by filename.
//
// All query for synchronization will be performed within the transaction if
// storage engine supports the transaction. (e.g. MySQL's MyISAM engine does
// NOT support the transaction)
func Sync(db *sql.DB, filename string, src interface{}) error {
	sqls, err := Diff(db, filename, src)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, sql := range sqls {
		if _, err := tx.Exec(sql); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Diff returns SQLs for schema synchronous between database and Go's struct.
func Diff(db *sql.DB, filename string, src interface{}) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	ast.FileExports(f)
	structASTMap := map[string]*ast.StructType{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.TypeSpec:
			if t, ok := x.Type.(*ast.StructType); ok {
				structASTMap[x.Name.Name] = t
			}
			return false
		default:
			return true
		}
	})
	structMap := map[string][]*field{}
	for name, structAST := range structASTMap {
		for _, fld := range structAST.Fields.List {
			var typeName string
			switch t := fld.Type.(type) {
			case *ast.Ident:
				typeName = t.Name
			case *ast.SelectorExpr:
				typeName = t.X.(*ast.Ident).Name + "." + t.Sel.Name
			case *ast.StarExpr:
				typeName = "*" + t.X.(*ast.Ident).Name
			default:
				return nil, fmt.Errorf("migu: BUG: unknown type %T", t)
			}
			f := field{
				Type: typeName,
			}
			if fld.Comment != nil {
				f.Comment = strings.TrimSpace(fld.Comment.Text())
			}
			for _, ident := range fld.Names {
				field := f
				field.Name = ident.Name
				structMap[name] = append(structMap[name], &field)
			}
		}
	}
	tableMap, err := getTableMap(db)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(structMap))
	for name := range structMap {
		names = append(names, name)
	}
	sort.Strings(names)
	d := &dialect.MySQL{}
	var migrations []string
	for _, name := range names {
		model := structMap[name]
		columns, ok := tableMap[name]
		tableName := toSnakeCase(name)
		if !ok {
			columns := make([]string, len(model))
			for i, f := range model {
				column := []string{toSnakeCase(f.Name), d.ColumnType(f.Type)}
				if f.Default != "" {
					column = append(column, "DEFAULT", f.Default)
				}
				if f.PrimaryKey {
					column = append(column, "NOT NULL", "AUTO_INCREMENT", "PRIMARY KEY")
				} else if f.Unique {
					column = append(column, "UNIQUE")
				}
				if f.Comment != "" {
					column = append(column, "COMMENT", fmt.Sprintf("'%s'", f.Comment))
				}
				columns[i] = strings.Join(column, " ")
			}
			migrations = append(migrations, fmt.Sprintf(`CREATE TABLE %s (
  %s
)`, tableName, strings.Join(columns, ",\n  ")))
		} else {
			table := map[string]*columnSchema{}
			for _, column := range columns {
				table[toCamelCase(column.ColumnName)] = column
			}
			for _, f := range model {
				switch column, ok := table[f.Name]; {
				case !ok:
					migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s ADD %s %s`, tableName, toSnakeCase(f.Name), d.ColumnType(f.Type)))
				case f.Type != column.columnType():
					migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s MODIFY %s %s`, tableName, toSnakeCase(f.Name), d.ColumnType(f.Type)))
				}
				delete(table, f.Name)
			}
			for _, f := range table {
				migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s DROP %s`, tableName, toSnakeCase(f.ColumnName)))
			}
		}
		delete(structMap, name)
		delete(tableMap, name)
	}
	for name := range tableMap {
		migrations = append(migrations, fmt.Sprintf(`DROP TABLE %s`, toSnakeCase(name)))
	}
	return migrations, nil
}

type field struct {
	Name       string
	Type       string
	Comment    string
	Unique     bool
	PrimaryKey bool
	Default    string
	Size       int64
}

// Fprint generates Go's structs from database schema and writes to output.
func Fprint(output io.Writer, db *sql.DB) error {
	tableMap, err := getTableMap(db)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(tableMap))
	for name := range tableMap {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, schema := range tableMap[name] {
			if schema.DataType == "datetime" {
				if err := fprintln(output, importAST("time")); err != nil {
					return err
				}
				break
			}
		}
	}
	for _, name := range names {
		if err := fprintln(output, structAST(name, tableMap[name])); err != nil {
			return err
		}
	}
	return nil
}

func getTableMap(db *sql.DB) (map[string][]*columnSchema, error) {
	dbname, err := getCurrentDBName(db)
	if err != nil {
		return nil, err
	}
	query := `
SELECT
  TABLE_NAME,
  COLUMN_NAME,
  COLUMN_DEFAULT,
  IS_NULLABLE,
  DATA_TYPE,
  CHARACTER_MAXIMUM_LENGTH,
  CHARACTER_OCTET_LENGTH,
  NUMERIC_PRECISION,
  NUMERIC_SCALE,
  COLUMN_TYPE,
  COLUMN_KEY,
  COLUMN_COMMENT
FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = ?
ORDER BY TABLE_NAME, ORDINAL_POSITION`
	rows, err := db.Query(query, dbname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tableMap := map[string][]*columnSchema{}
	for rows.Next() {
		schema := &columnSchema{}
		if err := rows.Scan(
			&schema.TableName,
			&schema.ColumnName,
			&schema.ColumnDefault,
			&schema.IsNullable,
			&schema.DataType,
			&schema.CharacterMaximumLength,
			&schema.CharacterOctetLength,
			&schema.NumericPrecision,
			&schema.NumericScale,
			&schema.ColumnType,
			&schema.ColumnKey,
			&schema.ColumnComment,
		); err != nil {
			return nil, err
		}
		tableName := toCamelCase(schema.TableName)
		tableMap[tableName] = append(tableMap[tableName], schema)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tableMap, nil
}

func getCurrentDBName(db *sql.DB) (string, error) {
	var dbname sql.NullString
	err := db.QueryRow(`SELECT DATABASE()`).Scan(&dbname)
	return dbname.String, err
}

func fprintln(output io.Writer, decl ast.Decl) error {
	if err := format.Node(output, token.NewFileSet(), decl); err != nil {
		return err
	}
	fmt.Fprintf(output, "\n\n")
	return nil
}

func importAST(pkg string) ast.Decl {
	return &ast.GenDecl{
		Tok: token.IMPORT,
		Specs: []ast.Spec{
			&ast.ImportSpec{
				Path: &ast.BasicLit{
					Kind:  token.STRING,
					Value: fmt.Sprintf(`"%s"`, pkg),
				},
			},
		},
	}
}

func structAST(name string, schemas []*columnSchema) ast.Decl {
	var fields []*ast.Field
	for _, schema := range schemas {
		fields = append(fields, schema.fieldAST())
	}
	return &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(toCamelCase(name)),
				Type: &ast.StructType{
					Fields: &ast.FieldList{
						List: fields,
					},
				},
			},
		},
	}
}

type columnSchema struct {
	TableName              string
	ColumnName             string
	OrdinalPosition        int64
	ColumnDefault          sql.NullString
	IsNullable             string
	DataType               string
	CharacterMaximumLength sql.NullInt64
	CharacterOctetLength   sql.NullInt64
	NumericPrecision       sql.NullInt64
	NumericScale           sql.NullInt64
	ColumnType             string
	ColumnKey              string
	ColumnComment          string
}

func (schema *columnSchema) fieldAST() *ast.Field {
	field := &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(toCamelCase(schema.ColumnName)),
		},
		Type: ast.NewIdent(schema.columnType()),
		// Tag: &ast.BasicLit{
		// Kind:  token.STRING,
		// Value: "",
		// },
	}
	return field
}

func (schema *columnSchema) columnType() string {
	switch schema.DataType {
	case "tinyint", "smallint", "mediumint", "int":
		if schema.isUnsigned() {
			return "uint"
		} else {
			return "int"
		}
	case "bigint":
		if schema.isUnsigned() {
			return "uint64"
		} else {
			return "int64"
		}
	case "varchar", "text":
		return "string"
	case "datetime":
		return "time.Time"
	default:
		return "string"
	}
}

func (schema *columnSchema) isUnsigned() bool {
	return strings.Contains(schema.ColumnType, "unsigned")
}
