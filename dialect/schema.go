package dialect

type ColumnSchema interface {
	TableName() string
	ColumnName() string
	ColumnType() string
	GoType() string
	IsDatetime() bool
	IsPrimaryKey() bool
	IsAutoIncrement() bool
	Index() (name string, unique bool, ok bool)
	Default() (string, bool)
	IsNullable() bool
	Extra() (string, bool)
	Comment() (string, bool)
}
