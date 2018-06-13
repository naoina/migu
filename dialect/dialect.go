package dialect

type Dialect interface {
	ColumnSchema(tables ...string) ([]ColumnSchema, error)
	ColumnType(name string) string
	Quote(s string) string
	QuoteString(s string) string
	AutoIncrement() string
}
