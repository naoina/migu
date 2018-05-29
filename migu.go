package migu

import (
	"database/sql"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/naoina/go-stringutil"
	"github.com/naoina/migu/dialect"
)

const (
	commentPrefix       = "//"
	marker              = "+migu"
	annotationSeparator = ':'
	defaultVarcharSize  = 255
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
	var filenames []string
	structASTMap := make(map[string]*structAST)
	if src == nil {
		files, err := collectFiles(filename)
		if err != nil {
			return nil, err
		}
		filenames = files
	} else {
		filenames = append(filenames, filename)
	}
	for _, filename := range filenames {
		m, err := makeStructASTMap(filename, src)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			structASTMap[k] = v
		}
	}
	d := &dialect.MySQL{}
	structMap := map[string]*table{}
	for name, structAST := range structASTMap {
		for _, fld := range structAST.StructType.Fields.List {
			typeName, err := detectTypeName(fld)
			if err != nil {
				return nil, err
			}
			f, err := newField(d, typeName, fld)
			if err != nil {
				return nil, err
			}
			if f.Ignore {
				continue
			}
			if !(ast.IsExported(f.Name) || (f.Name == "_" && f.Name != f.Column)) {
				continue
			}
			if structMap[name] == nil {
				structMap[name] = &table{
					Option: structAST.Annotation.Option,
				}
			}
			structMap[name].Fields = append(structMap[name].Fields, f)
		}
	}
	names := make([]string, 0, len(structMap))
	for name := range structMap {
		names = append(names, name)
	}
	tableMap, err := getTableMap(db, names...)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var migrations []string
	droppedColumn := map[string]struct{}{}
	for _, name := range names {
		tbl := structMap[name]
		tableName := d.Quote(name)
		var oldFields []*field
		if columns, ok := tableMap[name]; ok {
			for _, c := range columns {
				oldFieldAST, err := c.fieldAST(d)
				if err != nil {
					return nil, err
				}
				f, err := newField(d, fmt.Sprint(oldFieldAST.Type), oldFieldAST)
				if err != nil {
					return nil, err
				}
				oldFields = append(oldFields, f)
			}
			fields := makeAlterTableFields(oldFields, tbl.Fields)
			specs := make([]string, 0, len(fields))
			for _, f := range fields {
				switch {
				case f.IsAdded():
					specs = append(specs, fmt.Sprintf("ADD %s", columnSQL(d, f.new)))
				case f.IsDropped():
					specs = append(specs, fmt.Sprintf("DROP %s", d.Quote(f.old.Column)))
				case f.IsModified():
					specs = append(specs, fmt.Sprintf("CHANGE %s %s", d.Quote(f.old.Column), columnSQL(d, f.new)))
				}
			}
			if pkColumns, changed := makePrimaryKeyColumns(oldFields, tbl.Fields); len(pkColumns) > 0 {
				if changed {
					specs = append(specs, "DROP PRIMARY KEY")
				}
				for i, c := range pkColumns {
					pkColumns[i] = d.Quote(c)
				}
				specs = append(specs, fmt.Sprintf("ADD PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
			}
			if len(specs) > 0 {
				migrations = append(migrations, fmt.Sprintf("ALTER TABLE %s %s", tableName, strings.Join(specs, ", ")))
			}
			for _, f := range fields {
				if f.IsDropped() {
					droppedColumn[f.old.Column] = struct{}{}
				}
			}
		} else {
			columns := make([]string, len(tbl.Fields))
			for i, f := range tbl.Fields {
				columns[i] = columnSQL(d, f)
			}
			if pkColumns, _ := makePrimaryKeyColumns(oldFields, tbl.Fields); len(pkColumns) > 0 {
				for i, c := range pkColumns {
					pkColumns[i] = d.Quote(c)
				}
				columns = append(columns, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
			}
			query := fmt.Sprintf("CREATE TABLE %s (\n"+
				"  %s\n"+
				")", tableName, strings.Join(columns, ",\n  "))
			if tbl.Option != "" {
				query += " " + tbl.Option
			}
			migrations = append(migrations, query)
		}
		addIndexes, dropIndexes := makeIndexes(oldFields, tbl.Fields)
		for _, index := range dropIndexes {
			// If the column which has the index will be deleted, Migu will not delete the index related to the column
			// because the index will be deleted when the column which related to the index will be deleted.
			if _, ok := droppedColumn[index.Columns[0]]; !ok {
				migrations = append(migrations, fmt.Sprintf("DROP INDEX %s ON %s", d.Quote(index.Name), tableName))
			}
		}
		for _, index := range addIndexes {
			columns := make([]string, 0, len(index.Columns))
			for _, c := range index.Columns {
				columns = append(columns, d.Quote(c))
			}
			if index.Unique {
				migrations = append(migrations, fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", d.Quote(index.Name), tableName, strings.Join(columns, ",")))
			} else {
				migrations = append(migrations, fmt.Sprintf("CREATE INDEX %s ON %s (%s)", d.Quote(index.Name), tableName, strings.Join(columns, ",")))
			}
		}
		delete(structMap, name)
		delete(tableMap, name)
	}
	for name := range tableMap {
		migrations = append(migrations, fmt.Sprintf(`DROP TABLE %s`, d.Quote(name)))
	}
	return migrations, nil
}

func collectFiles(path string) ([]string, error) {
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return []string{path}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	list, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}
	var filenames []string
	for _, info := range list {
		if info.IsDir() {
			continue
		}
		name := info.Name()
		switch name[0] {
		case '.', '_':
			continue
		}
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		filenames = append(filenames, filepath.Join(path, name))
	}
	return filenames, nil
}

type table struct {
	Fields []*field
	Option string
}

type index struct {
	Name    string
	Columns []string
	Unique  bool
}

type field struct {
	Name          string
	GoType        string
	Type          string
	Column        string
	Comment       string
	RawIndexes    []string
	RawUniques    []string
	PrimaryKey    bool
	AutoIncrement bool
	Ignore        bool
	Default       string
	Size          uint64
	Extra         string
	Nullable      bool
	Unsigned      bool
	Precision     int64
	Scale         int64
}

func newField(d dialect.Dialect, typeName string, f *ast.Field) (*field, error) {
	ret := &field{
		GoType: typeName,
		Size:   defaultVarcharSize,
	}
	if len(f.Names) > 0 && f.Names[0] != nil {
		ret.Name = f.Names[0].Name
	}
	if f.Tag != nil {
		s, err := strconv.Unquote(f.Tag.Value)
		if err != nil {
			return nil, err
		}
		if err := parseStructTag(d, ret, reflect.StructTag(s)); err != nil {
			return nil, err
		}
	}
	if f.Comment != nil {
		ret.Comment = strings.TrimSpace(f.Comment.Text())
	}
	if ret.Column == "" {
		ret.Column = stringutil.ToSnakeCase(ret.Name)
	}
	colType, unsigned, null := d.ColumnType(ret.GoType, ret.Size, ret.AutoIncrement)
	if ret.Type != "" {
		colType = ret.Type
	}
	ret.Unsigned = unsigned
	ret.Nullable = null
	if ret.Type = d.DataType(colType, ret.Size, ret.Unsigned, ret.Precision, ret.Scale); ret.Type == "" {
		return nil, fmt.Errorf("unknown data type: `%s'", colType)
	}
	return ret, nil
}

func (f *field) Indexes() []string {
	indexes := make([]string, 0, len(f.RawIndexes))
	for _, index := range f.RawIndexes {
		if index == "" {
			index = f.Column
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func (f *field) UniqueIndexes() []string {
	uniques := make([]string, 0, len(f.RawUniques))
	for _, u := range f.RawUniques {
		if u == "" {
			u = f.Column
		}
		uniques = append(uniques, u)
	}
	return uniques
}

func (f *field) IsDifferent(another *field) bool {
	if f == nil && another == nil {
		return false
	}
	return ((f == nil && another != nil) || (f != nil && another == nil)) ||
		f.Type != another.Type ||
		f.Nullable != another.Nullable ||
		f.Default != another.Default ||
		f.Size != another.Size ||
		f.Column != another.Column ||
		f.Extra != another.Extra ||
		f.Comment != another.Comment ||
		f.AutoIncrement != another.AutoIncrement ||
		(f.Type == "DECIMAL" && (f.Precision != another.Precision || f.Scale != another.Scale))
}

func makePrimaryKeyColumns(oldFields, newFields []*field) (pkColumns []string, changed bool) {
	for _, f := range newFields {
		if f.PrimaryKey {
			pkColumns = append(pkColumns, f.Column)
		}
	}
	m := map[string]struct{}{}
	for _, f := range oldFields {
		if f.PrimaryKey {
			m[f.Column] = struct{}{}
		}
	}
	if len(m) != len(pkColumns) {
		if len(m) == 0 {
			return pkColumns, false
		}
		return pkColumns, true
	}
	for _, pk := range pkColumns {
		if _, exists := m[pk]; !exists {
			return pkColumns, true
		}
	}
	return nil, false
}

func makeIndexes(oldFields, newFields []*field) (addIndexes, dropIndexes []*index) {
	var dropIndexNames []string
	var addIndexNames []string
	dropIndexMap := map[string]*index{}
	addIndexMap := map[string]*index{}
	m := make(map[string]*field, len(oldFields))
	for _, f := range oldFields {
		m[f.Column] = f
	}
	for _, f := range newFields {
		oldField := m[f.Column]
		if oldField == nil {
			oldField = &field{}
		}
		oindexes, nindexes := oldField.Indexes(), f.Indexes()
		oldUniqueIndexes, newUniqueIndexes := oldField.UniqueIndexes(), f.UniqueIndexes()
		for _, name := range oindexes {
			if !inStrings(nindexes, name) {
				if dropIndexMap[name] == nil {
					dropIndexMap[name] = &index{Name: name, Unique: false}
					dropIndexNames = append(dropIndexNames, name)
				}
				dropIndexMap[name].Columns = append(dropIndexMap[name].Columns, oldField.Column)
			}
		}
		for _, name := range oldUniqueIndexes {
			if !inStrings(newUniqueIndexes, name) {
				if dropIndexMap[name] == nil {
					dropIndexMap[name] = &index{Name: name, Unique: true}
					dropIndexNames = append(dropIndexNames, name)
				}
				dropIndexMap[name].Columns = append(dropIndexMap[name].Columns, oldField.Column)
			}
		}
		for _, name := range nindexes {
			if !inStrings(oindexes, name) {
				if addIndexMap[name] == nil {
					addIndexMap[name] = &index{Name: name, Unique: false}
					addIndexNames = append(addIndexNames, name)
				}
				addIndexMap[name].Columns = append(addIndexMap[name].Columns, f.Column)
			}
		}
		for _, name := range newUniqueIndexes {
			if !inStrings(oldUniqueIndexes, name) {
				if addIndexMap[name] == nil {
					addIndexMap[name] = &index{Name: name, Unique: true}
					addIndexNames = append(addIndexNames, name)
				}
				addIndexMap[name].Columns = append(addIndexMap[name].Columns, f.Column)
			}
		}
	}
	for _, name := range addIndexNames {
		addIndexes = append(addIndexes, addIndexMap[name])
	}
	for _, name := range dropIndexNames {
		dropIndexes = append(dropIndexes, dropIndexMap[name])
	}
	return addIndexes, dropIndexes
}

type modifiedField struct {
	old *field
	new *field
}

func (f *modifiedField) IsAdded() bool {
	return f.old == nil && f.new != nil
}

func (f *modifiedField) IsDropped() bool {
	return f.old != nil && f.new == nil
}

func (f *modifiedField) IsModified() bool {
	return f.old != nil && f.new != nil
}

func makeAlterTableFields(oldFields, newFields []*field) (fields []modifiedField) {
	oldTable := make(map[string]*field, len(oldFields))
	for _, f := range oldFields {
		oldTable[f.Column] = f
		oldTable[f.Name] = f
	}
	newTable := make(map[string]*field, len(newFields))
	for _, f := range newFields {
		newTable[f.Column] = f
		newTable[f.Name] = f
	}
	for _, f := range newFields {
		oldF := oldTable[f.Column]
		if oldF == nil {
			oldF = oldTable[f.Name]
		}
		if oldF.IsDifferent(f) {
			fields = append(fields, modifiedField{
				old: oldF,
				new: f,
			})
		}
	}
	for _, f := range oldFields {
		if newTable[f.Column] == nil && newTable[f.Name] == nil {
			fields = append(fields, modifiedField{
				old: f,
				new: nil,
			})
		}
	}
	return fields
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
	d := &dialect.MySQL{}
	for _, name := range names {
		s, err := makeStructAST(d, name, tableMap[name])
		if err != nil {
			return err
		}
		fmt.Fprintln(output, commentPrefix+marker)
		if err := fprintln(output, s); err != nil {
			return err
		}
	}
	return nil
}

const (
	tagDefault       = "default"
	tagPrimaryKey    = "pk"
	tagAutoIncrement = "autoincrement"
	tagIndex         = "index"
	tagUnique        = "unique"
	tagSize          = "size"
	tagColumn        = "column"
	tagType          = "type"
	tagExtra         = "extra"
	tagPrecision     = "precision"
	tagScale         = "scale"
	tagIgnore        = "-"
)

func getTableMap(db *sql.DB, tables ...string) (map[string][]*columnSchema, error) {
	dbname, err := getCurrentDBName(db)
	if err != nil {
		return nil, err
	}
	indexMap, err := getIndexMap(db, dbname)
	if err != nil {
		return nil, err
	}
	parts := []string{
		"SELECT",
		"  TABLE_NAME,",
		"  COLUMN_NAME,",
		"  COLUMN_DEFAULT,",
		"  IS_NULLABLE,",
		"  DATA_TYPE,",
		"  CHARACTER_MAXIMUM_LENGTH,",
		"  CHARACTER_OCTET_LENGTH,",
		"  NUMERIC_PRECISION,",
		"  NUMERIC_SCALE,",
		"  DATETIME_PRECISION,",
		"  COLUMN_TYPE,",
		"  COLUMN_KEY,",
		"  EXTRA,",
		"  COLUMN_COMMENT",
		"FROM information_schema.COLUMNS",
		"WHERE TABLE_SCHEMA = ?",
	}
	args := []interface{}{dbname}
	if len(tables) > 0 {
		placeholder := strings.Repeat(",?", len(tables))
		placeholder = placeholder[1:] // truncate the heading comma.
		parts = append(parts, fmt.Sprintf("AND TABLE_NAME IN (%s)", placeholder))
		for _, t := range tables {
			args = append(args, t)
		}
	}
	parts = append(parts, "ORDER BY TABLE_NAME, ORDINAL_POSITION")
	query := strings.Join(parts, "\n")
	rows, err := db.Query(query, args...)
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
			&schema.DatetimePrecision,
			&schema.ColumnType,
			&schema.ColumnKey,
			&schema.Extra,
			&schema.ColumnComment,
		); err != nil {
			return nil, err
		}
		tableMap[schema.TableName] = append(tableMap[schema.TableName], schema)
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
	query := strings.Join([]string{
		"SELECT",
		"  TABLE_NAME,",
		"  COLUMN_NAME,",
		"  NON_UNIQUE,",
		"  INDEX_NAME",
		"FROM information_schema.STATISTICS",
		"WHERE TABLE_SCHEMA = ?",
	}, "\n")
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

type structAST struct {
	StructType *ast.StructType
	Annotation *annotation
}

func makeStructASTMap(filename string, src interface{}) (map[string]*structAST, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	structASTMap := map[string]*structAST{}
	for _, decl := range f.Decls {
		d, ok := decl.(*ast.GenDecl)
		if !ok || d.Tok != token.TYPE || d.Doc == nil {
			continue
		}
		annotation, err := parseAnnotation(d.Doc)
		if err != nil {
			return nil, err
		}
		if annotation == nil {
			continue
		}
		for _, spec := range d.Specs {
			s, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			t, ok := s.Type.(*ast.StructType)
			if !ok {
				continue
			}
			st := &structAST{
				StructType: t,
				Annotation: annotation,
			}
			if annotation.Table != "" {
				structASTMap[annotation.Table] = st
			} else {
				structASTMap[stringutil.ToSnakeCase(s.Name.Name)] = st
			}
		}
	}
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
	case *ast.ArrayType:
		name, err := detectTypeName(t.Elt)
		if err != nil {
			return "", err
		}
		return "[]" + name, nil
	default:
		return "", fmt.Errorf("migu: BUG: unknown type %T", t)
	}
}

func columnSQL(d dialect.Dialect, f *field) string {
	column := []string{d.Quote(f.Column), f.Type}
	if !f.Nullable {
		column = append(column, "NOT NULL")
	}
	if f.Default != "" {
		column = append(column, "DEFAULT", formatDefault(d, f.GoType, f.Default))
	}
	if f.AutoIncrement && d.AutoIncrement() != "" {
		column = append(column, d.AutoIncrement())
	}
	if f.Extra != "" {
		column = append(column, f.Extra)
	}
	if f.Comment != "" {
		column = append(column, "COMMENT", d.QuoteString(f.Comment))
	}
	return strings.Join(column, " ")
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

func makeStructAST(d dialect.Dialect, name string, schemas []*columnSchema) (ast.Decl, error) {
	var fields []*ast.Field
	for _, schema := range schemas {
		f, err := schema.fieldAST(d)
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return &ast.GenDecl{
		Tok: token.TYPE,
		Specs: []ast.Spec{
			&ast.TypeSpec{
				Name: ast.NewIdent(stringutil.ToUpperCamelCase(name)),
				Type: &ast.StructType{
					Fields: &ast.FieldList{
						List: fields,
					},
				},
			},
		},
	}, nil
}

func parseStructTag(d dialect.Dialect, f *field, tag reflect.StructTag) error {
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
		case tagAutoIncrement:
			f.AutoIncrement = true
		case tagIndex:
			if len(optval) == 2 {
				f.RawIndexes = append(f.RawIndexes, optval[1])
			} else {
				f.RawIndexes = append(f.RawIndexes, "")
			}
		case tagUnique:
			if len(optval) == 2 {
				f.RawUniques = append(f.RawUniques, optval[1])
			} else {
				f.RawUniques = append(f.RawUniques, "")
			}
		case tagIgnore:
			f.Ignore = true
		case tagColumn:
			if len(optval) < 2 {
				return fmt.Errorf("`column` tag must specify the parameter")
			}
			f.Column = optval[1]
		case tagType:
			if len(optval) < 2 {
				return fmt.Errorf("`type` tag must specify the parameter")
			}
			f.Type = optval[1]
		case tagSize:
			if len(optval) < 2 {
				return fmt.Errorf("`size' tag must specify the parameter")
			}
			size, err := strconv.ParseUint(optval[1], 10, 64)
			if err != nil {
				return err
			}
			f.Size = size
		case tagExtra:
			if len(optval) < 2 {
				return fmt.Errorf("`extra` tag must specify the parameter")
			}
			f.Extra = optval[1]
		case tagPrecision:
			if len(optval) < 2 {
				return fmt.Errorf("`precision` tag must specify the parameter")
			}
			prec, err := strconv.ParseInt(optval[1], 10, 64)
			if err != nil {
				return err
			}
			f.Precision = prec
		case tagScale:
			if len(optval) < 2 {
				return fmt.Errorf("`scale` tag must specify the parameter")
			}
			scale, err := strconv.ParseInt(optval[1], 10, 64)
			if err != nil {
				return err
			}
			f.Scale = scale
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
	DatetimePrecision      sql.NullInt64
	ColumnType             string
	ColumnKey              string
	Extra                  string
	ColumnComment          string
	NonUnique              int64
	IndexName              string
}

func (schema *columnSchema) fieldAST(d dialect.Dialect) (*ast.Field, error) {
	goTypes, typ, err := schema.GoFieldTypes()
	if err != nil {
		return nil, err
	}
	field := &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(stringutil.ToUpperCamelCase(schema.ColumnName)),
		},
		Type: ast.NewIdent(goTypes[0]),
	}
	var tags []string
	if typ != "" {
		tags = append(tags, fmt.Sprintf("%s:%s", tagType, typ))
	}
	if schema.ColumnDefault.Valid && (schema.ColumnType != "datetime" || schema.ColumnDefault.String != "0000-00-00 00:00:00") {
		tags = append(tags, tagDefault+":"+schema.ColumnDefault.String)
	}
	if schema.hasPrimaryKey() {
		tags = append(tags, tagPrimaryKey)
	}
	if schema.hasAutoIncrement() {
		tags = append(tags, tagAutoIncrement)
	}
	if schema.hasIndex() {
		if schema.IndexName == schema.ColumnName {
			tags = append(tags, tagIndex)
		} else {
			tags = append(tags, fmt.Sprintf("%s:%s", tagIndex, schema.IndexName))
		}
	}
	if schema.hasUniqueKey() {
		if schema.IndexName == schema.ColumnName {
			tags = append(tags, tagUnique)
		} else {
			tags = append(tags, fmt.Sprintf("%s:%s", tagUnique, schema.IndexName))
		}
	}
	if schema.hasSize() {
		if *schema.CharacterMaximumLength != defaultVarcharSize {
			tags = append(tags, fmt.Sprintf("%s:%d", tagSize, *schema.CharacterMaximumLength))
		}
	}
	if schema.hasPrecision() {
		tags = append(tags, fmt.Sprintf("%s:%d", tagPrecision, schema.NumericPrecision.Int64))
	}
	if schema.hasScale() {
		tags = append(tags, fmt.Sprintf("%s:%d", tagScale, schema.NumericScale.Int64))
	}
	if schema.hasDatetimePrecision() {
		tags = append(tags, fmt.Sprintf("%s:%d", tagPrecision, schema.DatetimePrecision.Int64))
	}
	if schema.hasExtra() {
		tags = append(tags, fmt.Sprintf("%s:%s", tagExtra, strings.ToUpper(schema.Extra)))
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
				{Text: " // " + schema.ColumnComment},
			},
		}
	}
	return field, nil
}

func (schema *columnSchema) GoFieldTypes() (goTypes []string, typ string, err error) {
	switch schema.DataType {
	case "tinyint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint8"}, "", nil
			}
			return []string{"uint8"}, "", nil
		}
		if schema.ColumnType == "tinyint(1)" {
			if schema.isNullable() {
				return []string{"*bool", "sql.NullBool"}, "", nil
			}
			return []string{"bool"}, "", nil
		}
		if schema.isNullable() {
			return []string{"*int8"}, "", nil
		}
		return []string{"int8"}, "", nil
	case "smallint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint16"}, "", nil
			}
			return []string{"uint16"}, "", nil
		}
		if schema.isNullable() {
			return []string{"*int16"}, "", nil
		}
		return []string{"int16"}, "", nil
	case "mediumint", "int":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint", "*uint32"}, "", nil
			}
			return []string{"uint", "uint32"}, "", nil
		}
		if schema.isNullable() {
			return []string{"*int", "*int32"}, "", nil
		}
		return []string{"int", "int32"}, "", nil
	case "bigint":
		if schema.isUnsigned() {
			if schema.isNullable() {
				return []string{"*uint64"}, "", nil
			}
			return []string{"uint64"}, "", nil
		}
		if schema.isNullable() {
			return []string{"*int64", "sql.NullInt64"}, "", nil
		}
		return []string{"int64"}, "", nil
	case "varchar", "text", "mediumtext", "longtext", "char":
		if schema.isNullable() {
			return []string{"*string", "sql.NullString", "[]byte"}, "", nil
		}
		return []string{"string", "[]byte"}, "", nil
	case "datetime":
		if schema.isNullable() {
			return []string{"*time.Time"}, "", nil
		}
		return []string{"time.Time"}, "", nil
	case "double", "float":
		if schema.isNullable() {
			return []string{"*float64", "sql.NullFloat64", "*float32"}, "", nil
		}
		return []string{"float64", "float32"}, "", nil
	case "decimal":
		if schema.isNullable() {
			return []string{"*float64", "sql.NullFloat64", "*float32"}, "decimal", nil
		}
		return []string{"float64", "float32"}, "decimal", nil
	default:
		return nil, "", fmt.Errorf("BUG: unexpected data type: %s", schema.DataType)
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

func (schema *columnSchema) hasAutoIncrement() bool {
	return schema.Extra == "auto_increment"
}

func (schema *columnSchema) hasExtra() bool {
	return schema.Extra != "" && !schema.hasAutoIncrement()
}

func (schema *columnSchema) hasIndex() bool {
	return schema.IndexName != "" && !schema.hasPrimaryKey() && schema.NonUnique != 0
}

func (schema *columnSchema) hasUniqueKey() bool {
	return schema.IndexName != "" && !schema.hasPrimaryKey() && schema.NonUnique == 0
}

func (schema *columnSchema) hasSize() bool {
	return (schema.DataType == "varchar" || schema.DataType == "char") && schema.CharacterMaximumLength != nil
}

func (schema *columnSchema) hasPrecision() bool {
	return schema.DataType == "decimal" && schema.NumericPrecision.Valid && schema.NumericPrecision.Int64 > 0
}

func (schema *columnSchema) hasScale() bool {
	switch schema.DataType {
	case "decimal":
		return schema.NumericScale.Valid && schema.NumericScale.Int64 > 0
	case "double":
		return schema.NumericScale.Valid
	}
	return false
}

func (schema *columnSchema) hasDatetimePrecision() bool {
	return inStrings([]string{"datetime", "timestamp", "time"}, schema.DataType) && schema.DatetimePrecision.Valid && schema.DatetimePrecision.Int64 > 0

}
