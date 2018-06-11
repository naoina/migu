package dialect

type Dialect interface {
	ColumnSchema(tables ...string) ([]ColumnSchema, error)
	ColumnType(name string, size uint64, autoIncrement bool) (typ string, unsigned, null bool)
	DataType(name string, size uint64, unsigned bool, prec, scale int64) string
	Quote(s string) string
	QuoteString(s string) string
	AutoIncrement() string
}

type Index struct {
	Name   string
	Unique bool
}
