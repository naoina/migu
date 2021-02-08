package dialect

// Option configures settings for computing differences of schemas.
type Option func(*option)

type option struct {
	columnTypes []*ColumnType
}

func newOption() *option {
	return &option{}
}

// WithColumnType appends custom column types definition for computing differences of schemas.
func WithColumnType(columnTypes []*ColumnType) Option {
	return func(o *option) {
		o.columnTypes = columnTypes
	}
}
