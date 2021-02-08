package dialect

type Dialect interface {
	ColumnSchema(tables ...string) ([]ColumnSchema, error)
	ColumnType(name string) string
	GoType(name string, nullable bool) string
	IsNullable(name string) bool
	ImportPackage(schema ColumnSchema) string
	Quote(s string) string
	QuoteString(s string) string

	CreateTableSQL(table Table) []string
	AddColumnSQL(field Field) []string
	DropColumnSQL(field Field) []string
	ModifyColumnSQL(oldField, newfield Field) []string
	CreateIndexSQL(index Index) []string
	DropIndexSQL(index Index) []string

	Begin() (Transactioner, error)
}

type ColumnSchema interface {
	TableName() string
	ColumnName() string
	ColumnType() string
	DataType() string
	IsPrimaryKey() bool
	IsAutoIncrement() bool
	Index() (name string, unique bool, ok bool)
	Default() (string, bool)
	IsNullable() bool
	Extra() (string, bool)
	Comment() (string, bool)
}

type Transactioner interface {
	Exec(sql string, args ...interface{}) error
	Commit() error
	Rollback() error
}

type PrimaryKeyModifier interface {
	ModifyPrimaryKeySQL(oldPrimaryKeys, newPrimaryKeys []Field) []string
}

type Table struct {
	Name        string
	Fields      []Field
	PrimaryKeys []string
	Option      string
}

type Field struct {
	Table         string
	Name          string
	Type          string
	Comment       string
	AutoIncrement bool
	Default       string
	Extra         string
	Nullable      bool
}

type Index struct {
	Table   string
	Name    string
	Columns []string
	Unique  bool
}

type ColumnType struct {
	Types           []string `yaml:"types"`
	GoTypes         []string `yaml:"goTypes"`
	GoNullableTypes []string `yaml:"goNullableTypes"`
	GoUnsignedTypes []string `yaml:"goUnsignedTypes"`
}

func (c *ColumnType) findType(t string) (name string, nullable, unsigned, found bool) {
	for _, v := range c.GoTypes {
		if v == t {
			if name == "" {
				name = c.Types[0]
			}
			break
		}
	}
	for _, v := range c.GoNullableTypes {
		if nullable = v == t; nullable {
			if name == "" {
				name = c.Types[0]
			}
			break
		}
	}
	for _, v := range c.GoUnsignedTypes {
		if unsigned = v == t; unsigned {
			if name == "" {
				name = c.Types[0]
			}
			break
		}
	}
	return name, nullable, unsigned, name != ""
}

func (c *ColumnType) findGoType(name string, nullable, unsigned bool) (typ string, found bool) {
	var candidate string
	for _, t := range c.Types {
		if t != name || (unsigned && len(c.GoUnsignedTypes) == 0) {
			continue
		}
		if unsigned {
			return c.GoUnsignedTypes[0], true
		}
		if nullable && len(c.GoNullableTypes) != 0 {
			return c.GoNullableTypes[0], true
		}
		candidate = c.GoTypes[0]
	}
	if candidate != "" && nullable {
		return "*" + candidate, true
	}
	return candidate, candidate != ""
}

func (c *ColumnType) allGoTypes() []string {
	ret := make([]string, 0, len(c.GoTypes)+len(c.GoNullableTypes)+len(c.GoUnsignedTypes))
	return append(append(append(ret, c.GoTypes...), c.GoNullableTypes...), c.GoUnsignedTypes...)
}

func (c *ColumnType) filteredNullableGoTypes() []string {
	ret := make([]string, 0, len(c.GoNullableTypes))
	for _, t := range c.GoNullableTypes {
		if c := t[0]; c != '*' && c != '[' {
			ret = append(ret, t)
		}
	}
	return ret
}
