package migu

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"reflect"
	"sort"
	"strconv"
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
			if fld.Tag != nil {
				s, err := strconv.Unquote(fld.Tag.Value)
				if err != nil {
					return nil, err
				}
				tag := reflect.StructTag(s)
				if def := tag.Get("default"); def != "" {
					f.Default = formatDefault(f.Type, def)
				}
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
				colType, null := d.ColumnType(f.Type)
				column := []string{d.Quote(toSnakeCase(f.Name)), colType}
				if !null {
					column = append(column, "NOT NULL")
				}
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
)`, d.Quote(tableName), strings.Join(columns, ",\n  ")))
		} else {
			table := map[string]*columnSchema{}
			for _, column := range columns {
				table[toCamelCase(column.ColumnName)] = column
			}
			for _, f := range model {
				if column, ok := table[f.Name]; ok {
					types, err := column.GoFieldTypes()
					if err != nil {
						return nil, err
					}
					if !inStrings(types, f.Type) {
						colType, null := d.ColumnType(f.Type)
						m := fmt.Sprintf(`ALTER TABLE %s MODIFY %s %s`, d.Quote(tableName), d.Quote(toSnakeCase(f.Name)), colType)
						if !null {
							m += " NOT NULL"
						}
						migrations = append(migrations, m)
					}
				} else {
					colType, null := d.ColumnType(f.Type)
					m := fmt.Sprintf(`ALTER TABLE %s ADD %s %s`, d.Quote(tableName), d.Quote(toSnakeCase(f.Name)), colType)
					if !null {
						m += " NOT NULL"
					}
					migrations = append(migrations, m)
				}
				delete(table, f.Name)
			}
			for _, f := range table {
				migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s DROP %s`, d.Quote(tableName), d.Quote(toSnakeCase(f.ColumnName))))
			}
		}
		delete(structMap, name)
		delete(tableMap, name)
	}
	for name := range tableMap {
		migrations = append(migrations, fmt.Sprintf(`DROP TABLE %s`, d.Quote(toSnakeCase(name))))
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
		s, err := structAST(name, tableMap[name])
		if err != nil {
			return err
		}
		if err := fprintln(output, s); err != nil {
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

func formatDefault(t, def string) string {
	switch t {
	case "string":
		return `'` + def + `'`
	default:
		return def
	}
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

func structAST(name string, schemas []*columnSchema) (ast.Decl, error) {
	var fields []*ast.Field
	for _, schema := range schemas {
		f, err := schema.fieldAST()
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
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
	}, nil
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

func (schema *columnSchema) fieldAST() (*ast.Field, error) {
	types, err := schema.GoFieldTypes()
	if err != nil {
		return nil, err
	}
	field := &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(toCamelCase(schema.ColumnName)),
		},
		Type: ast.NewIdent(types[0]),
	}
	if schema.ColumnDefault.Valid {
		field.Tag = &ast.BasicLit{
			Kind:  token.STRING,
			Value: fmt.Sprintf(`"default:\"%s\""`, schema.ColumnDefault.String),
		}
	}
	return field, nil
}

func (schema *columnSchema) GoFieldTypes() ([]string, error) {
	switch schema.DataType {
	case "tinyint", "smallint", "mediumint", "int":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint", "sql.NullInt64"}, nil
			}
			return []string{"uint"}, nil
		}
		if schema.isNullable() {
			return []string{"*int", "sql.NullInt64"}, nil
		}
		return []string{"int"}, nil
	case "bigint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint64", "sql.NullInt64"}, nil
			}
			return []string{"uint64"}, nil
		}
		if schema.isNullable() {
			return []string{"*int64", "sql.NullInt64"}, nil
		}
		return []string{"int64"}, nil
	case "varchar", "text", "mediumtext", "longtext":
		if schema.isNullable() {
			return []string{"*string", "sql.NullString"}, nil
		}
		return []string{"string"}, nil
	case "datetime":
		if schema.isNullable() {
			return []string{"*time.Time"}, nil
		}
		return []string{"time.Time"}, nil
	default:
		return nil, fmt.Errorf("BUG: unexpected data type: %s", schema.DataType)
	}
}

func (schema *columnSchema) isUnsigned() bool {
	return strings.Contains(schema.ColumnType, "unsigned")
}

func (schema *columnSchema) isNullable() bool {
	return strings.ToUpper(schema.IsNullable) == "YES"
}
