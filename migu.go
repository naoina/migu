package migu

import (
	"bufio"
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
	d := dialect.NewMySQL(db)
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
	tableMap, err := getTableMap(d, names...)
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
				oldFieldAST, err := fieldAST(c)
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
	if ret.IsEmbedded() {
		return ret, nil
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
	if colType == "" {
		return nil, fmt.Errorf("unsupported Go data type `%s'. You can use `type' struct tag if you use a user-defined type. See https://github.com/naoina/migu#type", ret.GoType)
	}
	ret.Unsigned = unsigned
	if !ret.Nullable {
		ret.Nullable = null
	}
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
		f.Column != another.Column ||
		f.Extra != another.Extra ||
		f.Comment != another.Comment ||
		f.AutoIncrement != another.AutoIncrement
}

func (f *field) IsEmbedded() bool {
	return f.Name == ""
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
	d := dialect.NewMySQL(db)
	tableMap, err := getTableMap(d)
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
	tagNull          = "null"
	tagExtra         = "extra"
	tagPrecision     = "precision"
	tagScale         = "scale"
	tagIgnore        = "-"
)

func getTableMap(d dialect.Dialect, tables ...string) (map[string][]dialect.ColumnSchema, error) {
	schemas, err := d.ColumnSchema(tables...)
	if err != nil {
		return nil, err
	}
	tableMap := map[string][]dialect.ColumnSchema{}
	for _, s := range schemas {
		tableMap[s.TableName()] = append(tableMap[s.TableName()], s)
	}
	return tableMap, nil
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

func hasDatetimeColumn(t map[string][]dialect.ColumnSchema) bool {
	for _, schemas := range t {
		for _, schema := range schemas {
			if schema.IsDatetime() {
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

func makeStructAST(d dialect.Dialect, name string, schemas []dialect.ColumnSchema) (ast.Decl, error) {
	var fields []*ast.Field
	for _, schema := range schemas {
		f, err := fieldAST(schema)
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

func nextStructTag(bs []byte) string {
	s := string(bs)
	for _, tag := range []string{
		tagDefault,
		tagPrimaryKey,
		tagAutoIncrement,
		tagIndex,
		tagUnique,
		tagSize,
		tagColumn,
		tagType,
		tagNull,
		tagExtra,
		tagPrecision,
		tagScale,
		tagIgnore,
	} {
		if strings.HasSuffix(s, ","+tag) {
			return ","+tag;
		}
	}
	return ""
}

func splitStructTags(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF {
		return 0, nil, nil
	}
	var buf []byte
	for ; advance < len(data); advance++ {
		buf = append(buf, data[advance])
		if n := nextStructTag(buf); n != "" {
			advance = advance - len(n) + 1
			break
		}
	}
	token = data[:advance]
	if len(token) == 0 {
		return advance + 1, token, nil
	}
	if len(data[advance:]) > 1 {
		return advance + 1, token, nil
	} else {
		return advance, token, nil
	}
}

func parseStructTag(d dialect.Dialect, f *field, tag reflect.StructTag) error {
	migu := tag.Get("migu")
	if migu == "" {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(migu))
	scanner.Split(splitStructTags)
	for scanner.Scan() {
		opt := scanner.Text()
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
		case tagNull:
			f.Nullable = true
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

func fieldAST(schema dialect.ColumnSchema) (*ast.Field, error) {
	field := &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(stringutil.ToUpperCamelCase(schema.ColumnName())),
		},
		Type: ast.NewIdent(schema.GoType()),
	}
	var tags []string
	tags = append(tags, fmt.Sprintf("%s:%s", tagType, schema.DataType()))
	if v, ok := schema.Default(); ok {
		tags = append(tags, tagDefault+":"+v)
	}
	if schema.IsPrimaryKey() {
		tags = append(tags, tagPrimaryKey)
	}
	if schema.IsAutoIncrement() {
		tags = append(tags, tagAutoIncrement)
	}
	if v, unique, ok := schema.Index(); ok {
		var tag string
		if unique {
			tag = tagUnique
		} else {
			tag = tagIndex
		}
		if v == schema.ColumnName() {
			tags = append(tags, tag)
		} else {
			tags = append(tags, fmt.Sprintf("%s:%s", tag, v))
		}
	}
	if v, ok := schema.Size(); ok {
		tags = append(tags, fmt.Sprintf("%s:%d", tagSize, v))
	}
	if v, ok := schema.Precision(); ok {
		tags = append(tags, fmt.Sprintf("%s:%d", tagPrecision, v))
	}
	if v, ok := schema.Scale(); ok {
		tags = append(tags, fmt.Sprintf("%s:%d", tagScale, v))
	}
	if schema.IsNullable() {
		tags = append(tags, tagNull)
	}
	if v, ok := schema.Extra(); ok {
		tags = append(tags, fmt.Sprintf("%s:%s", tagExtra, v))
	}
	if len(tags) > 0 {
		field.Tag = &ast.BasicLit{
			Kind:     token.STRING,
			Value:    fmt.Sprintf("`migu:\"%s\"`", strings.Join(tags, ",")),
			ValuePos: 1,
		}
	}
	if v, ok := schema.Comment(); ok {
		field.Comment = &ast.CommentGroup{
			List: []*ast.Comment{
				{Text: "// " + v},
			},
		}
	}
	return field, nil
}
