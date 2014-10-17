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
)

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
				fmt.Printf("%#v\n", t)
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
	var migrations []string
	for _, name := range names {
		model := structMap[name]
		table, ok := tableMap[name]
		tableName := toSnakeCase(name)
		if !ok {
			columns := make([]string, len(model))
			for i, f := range model {
				column := []string{toSnakeCase(f.Name), mysqlType(f.Type)}
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
			for _, f := range model {
				switch column, ok := table[f.Name]; {
				case !ok:
					migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s ADD %s %s`, tableName, toSnakeCase(f.Name), mysqlType(f.Type)))
				case f.Type != column.columnType():
					migrations = append(migrations, fmt.Sprintf(`ALTER TABLE %s MODIFY %s %s`, tableName, toSnakeCase(f.Name), mysqlType(f.Type)))
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

func mysqlType(name string) string {
	switch name {
	case "string":
		return "NOT NULL VARCHAR(255)"
	case "int":
		return "NOT NULL INT"
	case "int64":
		return "NOT NULL BIGINT"
	case "uint":
		return "NOT NULL UNSIGNED INT"
	case "bool":
		return "NOT NULL BOOLEAN"
	case "float32", "float64":
		return "NOT NULL DOUBLE"
	case "sql.NullString", "*string":
		return "VARCHAR(255)"
	case "sql.NullBool", "*bool":
		return "BOOLEAN"
	case "sql.NullInt64", "*int64":
		return "BIGINT"
	case "sql.NullFloat64", "*float64":
		return "DOUBLE"
	default:
		return "VARCHAR(255)"
	}
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

func getTableMap(db *sql.DB) (map[string]map[string]*columnSchema, error) {
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
	tableMap := map[string]map[string]*columnSchema{}
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
			&schema.ColumnKey,
			&schema.ColumnComment,
		); err != nil {
			return nil, err
		}
		tableName := toCamelCase(schema.TableName)
		if tableMap[tableName] == nil {
			tableMap[tableName] = map[string]*columnSchema{}
		}
		tableMap[tableName][toCamelCase(schema.ColumnName)] = schema
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

func structAST(name string, schemaMap map[string]*columnSchema) ast.Decl {
	var fields []*ast.Field
	for _, schema := range schemaMap {
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
		return "int"
	case "bigint":
		return "int64"
	case "varchar", "text":
		return "string"
	case "datetime":
		return "time.Time"
	default:
		return "string"
	}
}
