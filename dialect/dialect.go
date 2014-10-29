package dialect

type Dialect interface {
	ColumnType(name string) (typ string, null bool)
	Quote(s string) string
}
