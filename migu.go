package migu

import (
	"bufio"
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
func Sync(d dialect.Dialect, filename string, src interface{}) error {
	sqls, err := Diff(d, filename, src)
	if err != nil {
		return err
	}
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	for _, sql := range sqls {
		if err := tx.Exec(sql); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Diff returns SQLs for schema synchronous between database and Go's struct.
func Diff(d dialect.Dialect, filename string, src interface{}) ([]string, error) {
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
	structMap := map[string]*table{}
	for name, structAST := range structASTMap {
		for _, fld := range structAST.StructType.Fields.List {
			typeName, err := detectTypeName(fld)
			if err != nil {
				return nil, err
			}
			f, err := newField(d, name, typeName, fld)
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
		var oldFields []*field
		if columns, ok := tableMap[name]; ok {
			for _, c := range columns {
				oldFieldAST, err := fieldAST(d, c)
				if err != nil {
					return nil, err
				}
				f, err := newField(d, name, fmt.Sprint(oldFieldAST.Type), oldFieldAST)
				if err != nil {
					return nil, err
				}
				oldFields = append(oldFields, f)
			}
			fields := makeAlterTableFields(oldFields, tbl.Fields)
			for _, f := range fields {
				switch {
				case f.IsAdded():
					migrations = append(migrations, d.AddColumnSQL(f.new.ToField())...)
				case f.IsDropped():
					migrations = append(migrations, d.DropColumnSQL(f.old.ToField())...)
				case f.IsModified():
					migrations = append(migrations, d.ModifyColumnSQL(f.old.ToField(), f.new.ToField())...)
				}
			}
			if d, ok := d.(dialect.PrimaryKeyModifier); ok {
				oldPks, newPks := makePrimaryKeyColumns(oldFields, tbl.Fields)
				if len(oldPks) > 0 || len(newPks) > 0 {
					oldPrimaryKeyFields := make([]dialect.Field, len(oldPks))
					for i, pk := range oldPks {
						oldPrimaryKeyFields[i] = pk.ToField()
					}
					newPrimaryKeyFields := make([]dialect.Field, len(newPks))
					for i, pk := range newPks {
						newPrimaryKeyFields[i] = pk.ToField()
					}
					migrations = append(migrations, d.ModifyPrimaryKeySQL(oldPrimaryKeyFields, newPrimaryKeyFields)...)
				}
			}
			for _, f := range fields {
				if f.IsDropped() {
					droppedColumn[f.old.Column] = struct{}{}
				}
			}
		} else {
			fields := make([]dialect.Field, len(tbl.Fields))
			for i, f := range tbl.Fields {
				fields[i] = f.ToField()
			}
			_, newPks := makePrimaryKeyColumns(oldFields, tbl.Fields)
			pkColumns := make([]string, len(newPks))
			for i, pk := range newPks {
				pkColumns[i] = pk.ToField().Name
			}
			migrations = append(migrations, d.CreateTableSQL(dialect.Table{
				Name:        name,
				Fields:      fields,
				PrimaryKeys: pkColumns,
				Option:      tbl.Option,
			})...)
		}
		addIndexes, dropIndexes := makeIndexes(oldFields, tbl.Fields)
		for _, index := range dropIndexes {
			// If the column which has the index will be deleted, Migu will not delete the index related to the column
			// because the index will be deleted when the column which related to the index will be deleted.
			if _, ok := droppedColumn[index.Columns[0]]; !ok {
				migrations = append(migrations, d.DropIndexSQL(index.ToIndex())...)
			}
		}
		for _, index := range addIndexes {
			migrations = append(migrations, d.CreateIndexSQL(index.ToIndex())...)
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
	Table   string
	Name    string
	Columns []string
	Unique  bool
}

func (i *index) ToIndex() dialect.Index {
	return dialect.Index{
		Table:   i.Table,
		Name:    i.Name,
		Columns: i.Columns,
		Unique:  i.Unique,
	}
}

type field struct {
	Table         string
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
	Extra         string
	Nullable      bool
}

func newField(d dialect.Dialect, tableName string, typeName string, f *ast.Field) (*field, error) {
	ret := &field{
		Table:  tableName,
		GoType: typeName,
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
	if !ret.Nullable {
		if ret.GoType[0] == '*' {
			ret.Nullable = true
		} else {
			ret.Nullable = d.IsNullable(strings.TrimLeft(ret.GoType, "*"))
		}
	}
	var colType string
	if ret.Type == "" {
		colType = strings.TrimLeft(ret.GoType, "*")
	} else {
		colType = ret.Type
	}
	ret.Type = d.ColumnType(colType)
	return ret, nil
}

func (f *field) Indexes() []string {
	indexes := make([]string, 0, len(f.RawIndexes))
	for _, index := range f.RawIndexes {
		if index == "" {
			index = stringutil.ToSnakeCase(f.Table) + "_" + f.Column
		}
		indexes = append(indexes, index)
	}
	return indexes
}

func (f *field) UniqueIndexes() []string {
	uniques := make([]string, 0, len(f.RawUniques))
	for _, u := range f.RawUniques {
		if u == "" {
			u = stringutil.ToSnakeCase(f.Table) + "_" + f.Column
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

func (f *field) ToField() dialect.Field {
	return dialect.Field{
		Table:         f.Table,
		Name:          f.Column,
		Type:          f.Type,
		Comment:       f.Comment,
		AutoIncrement: f.AutoIncrement,
		Default:       f.Default,
		Extra:         f.Extra,
		Nullable:      f.Nullable,
	}
}

func makePrimaryKeyColumns(oldFields, newFields []*field) (oldPks, newPks []*field) {
	for _, f := range newFields {
		if f.PrimaryKey {
			newPks = append(newPks, f)
		}
	}
	for _, f := range oldFields {
		if f.PrimaryKey {
			oldPks = append(oldPks, f)
		}
	}
	if len(oldPks) != len(newPks) {
		return oldPks, newPks
	}
	m := make(map[string]struct{}, len(oldPks))
	for _, f := range oldPks {
		m[f.Column] = struct{}{}
	}
	for _, pk := range newPks {
		if _, exists := m[pk.Column]; !exists {
			return oldPks, newPks
		}
	}
	return nil, nil
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
					dropIndexMap[name] = &index{
						Table:  f.Table,
						Name:   name,
						Unique: false,
					}
					dropIndexNames = append(dropIndexNames, name)
				}
				dropIndexMap[name].Columns = append(dropIndexMap[name].Columns, oldField.Column)
			}
		}
		for _, name := range oldUniqueIndexes {
			if !inStrings(newUniqueIndexes, name) {
				if dropIndexMap[name] == nil {
					dropIndexMap[name] = &index{
						Table:  f.Table,
						Name:   name,
						Unique: true,
					}
					dropIndexNames = append(dropIndexNames, name)
				}
				dropIndexMap[name].Columns = append(dropIndexMap[name].Columns, oldField.Column)
			}
		}
		for _, name := range nindexes {
			if !inStrings(oindexes, name) {
				if addIndexMap[name] == nil {
					addIndexMap[name] = &index{
						Table:  f.Table,
						Name:   name,
						Unique: false,
					}
					addIndexNames = append(addIndexNames, name)
				}
				addIndexMap[name].Columns = append(addIndexMap[name].Columns, f.Column)
			}
		}
		for _, name := range newUniqueIndexes {
			if !inStrings(oldUniqueIndexes, name) {
				if addIndexMap[name] == nil {
					addIndexMap[name] = &index{
						Table:  f.Table,
						Name:   name,
						Unique: true,
					}
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
func Fprint(output io.Writer, d dialect.Dialect) error {
	tableMap, err := getTableMap(d)
	if err != nil {
		return err
	}
	pkgMap := map[string]struct{}{}
	for _, schemas := range tableMap {
		for _, schema := range schemas {
			if pkg := d.ImportPackage(schema); pkg != "" {
				pkgMap[pkg] = struct{}{}
			}
		}
	}
	if len(pkgMap) != 0 {
		pkgs := make([]string, 0, len(pkgMap))
		for pkg := range pkgMap {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)
		if err := fprintln(output, importAST(pkgs)); err != nil {
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
	tagColumn        = "column"
	tagType          = "type"
	tagNull          = "null"
	tagExtra         = "extra"
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

func importAST(pkgs []string) ast.Decl {
	decl := &ast.GenDecl{
		Tok: token.IMPORT,
	}
	for _, pkg := range pkgs {
		decl.Specs = append(decl.Specs, &ast.ImportSpec{
			Path: &ast.BasicLit{
				Kind:  token.STRING,
				Value: fmt.Sprintf(`"%s"`, pkg),
			},
		})
	}
	return decl
}

func makeStructAST(d dialect.Dialect, name string, schemas []dialect.ColumnSchema) (ast.Decl, error) {
	var fields []*ast.Field
	for _, schema := range schemas {
		f, err := fieldAST(d, schema)
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
	scanner := bufio.NewScanner(strings.NewReader(migu))
	scanner.Split(tagOptionSplit)
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
		case tagExtra:
			if len(optval) < 2 {
				return fmt.Errorf("`extra` tag must specify the parameter")
			}
			f.Extra = optval[1]
		default:
			return fmt.Errorf("unknown option: `%s'", opt)
		}
	}
	return scanner.Err()
}

func tagOptionSplit(data []byte, atEOF bool) (advance int, token []byte, err error) {
	var inParenthesis bool
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case ',':
			if !inParenthesis {
				return i + 1, data[:i], nil
			}
		case '(':
			inParenthesis = true
		case ')':
			inParenthesis = false
		}
	}
	return 0, data, bufio.ErrFinalToken
}

func fieldAST(d dialect.Dialect, schema dialect.ColumnSchema) (*ast.Field, error) {
	field := &ast.Field{
		Names: []*ast.Ident{
			ast.NewIdent(stringutil.ToUpperCamelCase(schema.ColumnName())),
		},
		Type: ast.NewIdent(d.GoType(schema.ColumnType(), schema.IsNullable())),
	}
	var tags []string
	tags = append(tags, fmt.Sprintf("%s:%s", tagType, schema.ColumnType()))
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
