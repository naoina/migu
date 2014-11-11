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
	structASTMap, err := makeStructASTMap(filename, src)
	if err != nil {
		return nil, err
	}
	structMap := map[string][]*field{}
	for name, structAST := range structASTMap {
		for _, fld := range structAST.Fields.List {
			typeName, err := detectTypeName(fld)
			if err != nil {
				return nil, err
			}
			f, err := newField(typeName, fld)
			if err != nil {
				return nil, err
			}
			for _, ident := range fld.Names {
				field := *f
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
				columns[i] = columnSQL(d, f)
			}
			migrations = append(migrations, fmt.Sprintf(`CREATE TABLE %s (
  %s
)`, d.Quote(tableName), strings.Join(columns, ",\n  ")))
		} else {
			table := map[string]*columnSchema{}
			for _, column := range columns {
				table[toCamelCase(column.ColumnName)] = column
			}
			var modifySQLs []string
			var dropSQLs []string
			for _, f := range model {
				m, d, err := alterTableSQLs(d, tableName, table, f)
				if err != nil {
					return nil, err
				}
				modifySQLs = append(modifySQLs, m...)
				dropSQLs = append(dropSQLs, d...)
				delete(table, f.Name)
			}
			migrations = append(migrations, append(dropSQLs, modifySQLs...)...)
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
	Size       uint64
}

func newField(typeName string, f *ast.Field) (*field, error) {
	ret := &field{
		Type: typeName,
	}
	if f.Tag != nil {
		s, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			return nil, err
		}
		if err := parseStructTag(ret, reflect.StructTag(s)); err != nil {
			return nil, err
		}
	}
	if f.Comment != nil {
		ret.Comment = strings.TrimSpace(f.Comment.Text())
	}
	return ret, nil
}

// Fprint generates Go's structs from database schema and writes to output.
func Fprint(output io.Writer, db *sql.DB) error {
	tableMap, err := getTableMap(db)
	if err != nil {
		return err
	}
	if hasDatetimeColumn(tableMap) {
		if err := fprintln(output, importAST("time")); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(tableMap))
	for name := range tableMap {
		names = append(names, name)
	}
	sort.Strings(names)
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

const (
	tagDefault    = "default"
	tagPrimaryKey = "pk"
	tagUnique     = "unique"
	tagSize       = "size"
)

func getTableMap(db *sql.DB) (map[string][]*columnSchema, error) {
	dbname, err := getCurrentDBName(db)
	if err != nil {
		return nil, err
	}
	indexMap, err := getIndexMap(db, dbname)
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
  EXTRA,
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
			&schema.Extra,
			&schema.ColumnComment,
		); err != nil {
			return nil, err
		}
		tableName := toCamelCase(schema.TableName)
		tableMap[tableName] = append(tableMap[tableName], schema)
		if tableIndex, exists := indexMap[schema.TableName]; exists {
			if info, exists := tableIndex[schema.ColumnName]; exists {
				schema.NonUnique = info.NonUnique
				schema.IndexName = info.IndexName
			}
		}
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

type indexInfo struct {
	NonUnique int64
	IndexName string
}

func getIndexMap(db *sql.DB, dbname string) (map[string]map[string]indexInfo, error) {
	query := `
SELECT
  TABLE_NAME,
  COLUMN_NAME,
  NON_UNIQUE,
  INDEX_NAME
FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = ?`
	rows, err := db.Query(query, dbname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexMap := make(map[string]map[string]indexInfo)
	for rows.Next() {
		var (
			tableName  string
			columnName string
			index      indexInfo
		)
		if err := rows.Scan(&tableName, &columnName, &index.NonUnique, &index.IndexName); err != nil {
			return nil, err
		}
		if _, exists := indexMap[tableName]; !exists {
			indexMap[tableName] = make(map[string]indexInfo)
		}
		indexMap[tableName][columnName] = index
	}
	return indexMap, rows.Err()
}

func formatDefault(d dialect.Dialect, t, def string) string {
	switch t {
	case "string":
		return d.QuoteString(def)
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

func makeStructASTMap(filename string, src interface{}) (map[string]*ast.StructType, error) {
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
	return structASTMap, nil
}

func detectTypeName(n ast.Node) (string, error) {
	switch t := n.(type) {
	case *ast.Field:
		return detectTypeName(t.Type)
	case *ast.Ident:
		return t.Name, nil
	case *ast.SelectorExpr:
		name, err := detectTypeName(t.X)
		if err != nil {
			return "", err
		}
		return name + "." + t.Sel.Name, nil
	case *ast.StarExpr:
		name, err := detectTypeName(t.X)
		if err != nil {
			return "", err
		}
		return "*" + name, nil
	default:
		return "", fmt.Errorf("migu: BUG: unknown type %T", t)
	}
}

func columnSQL(d dialect.Dialect, f *field) string {
	colType, null, autoIncrementable := d.ColumnType(f.Type, f.Size, f.PrimaryKey)
	column := []string{d.Quote(toSnakeCase(f.Name)), colType}
	if !null {
		column = append(column, "NOT NULL")
	}
	if f.Default != "" {
		column = append(column, "DEFAULT", formatDefault(d, f.Type, f.Default))
	}
	if f.PrimaryKey {
		if autoIncrementable {
			column = append(column, d.AutoIncrement())
		}
		column = append(column, "PRIMARY KEY")
	} else if f.Unique {
		column = append(column, "UNIQUE")
	}
	if f.Comment != "" {
		column = append(column, "COMMENT", d.QuoteString(f.Comment))
	}
	return strings.Join(column, " ")
}

func alterTableSQLs(d dialect.Dialect, tableName string, table map[string]*columnSchema, f *field) (modifySQLs, dropSQLs []string, err error) {
	column, exists := table[f.Name]
	if !exists {
		return []string{
			fmt.Sprintf(`ALTER TABLE %s ADD %s`, d.Quote(tableName), columnSQL(d, f)),
		}, nil, nil
	}
	types, err := column.GoFieldTypes()
	if err != nil {
		return nil, nil, err
	}
	oldFieldAST, err := column.fieldAST()
	if err != nil {
		return nil, nil, err
	}
	oldF, err := newField(f.Type, oldFieldAST)
	if err != nil {
		return nil, nil, err
	}
	oldF.Name = f.Name
	if !inStrings(types, f.Type) || !reflect.DeepEqual(oldF, f) {
		tableName = d.Quote(tableName)
		colSQL := columnSQL(d, f)
		modifySQLs = append(modifySQLs, fmt.Sprintf(`ALTER TABLE %s MODIFY %s`, tableName, colSQL))
		var drop []string
		if oldF.PrimaryKey != f.PrimaryKey && !f.PrimaryKey {
			drop = append(drop, `DROP PRIMARY KEY`)
			if column.Extra == "auto_increment" {
				drop = append(drop, `MODIFY `+colSQL)
				modifySQLs = nil
			}
		}
		if oldF.Unique != f.Unique && !f.Unique {
			if column.hasPrimaryKey() {
				drop = append(drop, `DROP PRIMARY KEY`)
			} else {
				drop = append(drop, `DROP INDEX `+column.IndexName)
			}
		}
		if len(drop) > 0 {
			dropSQLs = append(dropSQLs, fmt.Sprintf(`ALTER TABLE %s %s`, tableName, strings.Join(drop, ", ")))
		}
	}
	return modifySQLs, dropSQLs, nil
}

func hasDatetimeColumn(t map[string][]*columnSchema) bool {
	for _, schemas := range t {
		for _, schema := range schemas {
			if schema.DataType == "datetime" {
				return true
			}
		}
	}
	return false
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

func parseStructTag(f *field, tag reflect.StructTag) error {
	migu := tag.Get("migu")
	if migu == "" {
		return nil
	}
	for _, opt := range strings.Split(migu, ",") {
		optval := strings.SplitN(opt, ":", 2)
		switch optval[0] {
		case tagDefault:
			if len(optval) > 1 {
				f.Default = optval[1]
			}
		case tagPrimaryKey:
			f.PrimaryKey = true
		case tagUnique:
			f.Unique = true
		case tagSize:
			if len(optval) < 2 {
				return fmt.Errorf("`size' tag must specify the parameter")
			}
			size, err := strconv.ParseUint(optval[1], 10, 64)
			if err != nil {
				return err
			}
			f.Size = size
		default:
			return fmt.Errorf("unknown option: `%s'", opt)
		}
	}
	return nil
}

type columnSchema struct {
	TableName              string
	ColumnName             string
	OrdinalPosition        int64
	ColumnDefault          sql.NullString
	IsNullable             string
	DataType               string
	CharacterMaximumLength *uint64
	CharacterOctetLength   sql.NullInt64
	NumericPrecision       sql.NullInt64
	NumericScale           sql.NullInt64
	ColumnType             string
	ColumnKey              string
	Extra                  string
	ColumnComment          string
	NonUnique              int64
	IndexName              string
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
	var tags []string
	if schema.ColumnDefault.Valid {
		tags = append(tags, tagDefault+":"+schema.ColumnDefault.String)
	}
	if schema.hasPrimaryKey() {
		tags = append(tags, tagPrimaryKey)
	}
	if schema.hasUniqueKey() {
		tags = append(tags, tagUnique)
	}
	if schema.hasSize() {
		tags = append(tags, fmt.Sprintf("%s:%d", tagSize, schema.CharacterMaximumLength))
	}
	if len(tags) > 0 {
		field.Tag = &ast.BasicLit{
			Kind:  token.STRING,
			Value: fmt.Sprintf("`migu:\"%s\"`", strings.Join(tags, ",")),
		}
	}
	if schema.ColumnComment != "" {
		field.Comment = &ast.CommentGroup{
			List: []*ast.Comment{
				{Text: schema.ColumnComment},
			},
		}
	}
	return field, nil
}

func (schema *columnSchema) GoFieldTypes() ([]string, error) {
	switch schema.DataType {
	case "tinyint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint8"}, nil
			}
			return []string{"uint8"}, nil
		}
		if schema.isNullable() {
			return []string{"*int8", "*bool", "sql.NullBool"}, nil
		}
		return []string{"int8", "bool"}, nil
	case "smallint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint16"}, nil
			}
			return []string{"uint16"}, nil
		}
		if schema.isNullable() {
			return []string{"*int16"}, nil
		}
		return []string{"int16"}, nil
	case "mediumint", "int":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint", "*uint32"}, nil
			}
			return []string{"uint", "uint32"}, nil
		}
		if schema.isNullable() {
			return []string{"*int", "*int32"}, nil
		}
		return []string{"int", "int32"}, nil
	case "bigint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint64"}, nil
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
	case "double":
		if schema.isNullable() {
			return []string{"*float32", "*float64", "sql.NullFloat64"}, nil
		}
		return []string{"float32", "float64"}, nil
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

func (schema *columnSchema) hasPrimaryKey() bool {
	return schema.ColumnKey == "PRI" && strings.ToUpper(schema.IndexName) == "PRIMARY"
}

func (schema *columnSchema) hasUniqueKey() bool {
	return schema.ColumnKey != "" && schema.IndexName != "" && !schema.hasPrimaryKey()
}

func (schema *columnSchema) hasSize() bool {
	return schema.DataType == "varchar" && schema.CharacterMaximumLength != nil && *schema.CharacterMaximumLength != uint64(255)
}
