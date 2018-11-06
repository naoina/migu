package dialect

type ColumnSchema interface {
	TableName() string
	ColumnName() string
	DataType() string
	GoType() string
	IsDatetime() bool
	IsPrimaryKey() bool
	IsAutoIncrement() bool
	Index() (name string, unique bool, ok bool)
	Default() (string, bool)
	Size() (int64, bool)
	Precision() (int64, bool)
	Scale() (int64, bool)
	IsNullable() bool
	Extra() (string, bool)
	Comment() (string, bool)
	IsEnumerated() bool
}
