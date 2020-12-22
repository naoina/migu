package dialect

type Dialect interface {
	ColumnSchema(tables ...string) ([]ColumnSchema, error)
	ColumnType(name string) string
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
