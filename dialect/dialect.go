package dialect

type Dialect interface {
	ColumnType(name string, size uint64) (typ string, null, autoIncrementable bool)
	Quote(s string) string
}
