package dialect

type Dialect interface {
	ColumnType(name string, size uint64, autoIncrement bool) (typ string, unsigned, null bool)
	DataType(name string, size uint64, unsigned bool, prec, scale int64) string
	Quote(s string) string
	QuoteString(s string) string
	AutoIncrement() string
}
