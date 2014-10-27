package dialect

type Dialect interface {
	ColumnType(name string) string
}
