package dialect

type Dialect interface {
	ColumnType(name string, size uint64, autoIncrement bool) (typ string, null bool)
	Quote(s string) string
	QuoteString(s string) string
	AutoIncrement() string
}
