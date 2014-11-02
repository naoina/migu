package dialect

type Dialect interface {
	ColumnType(name string) (typ string, null, autoIncrementable bool)
	Quote(s string) string
}
