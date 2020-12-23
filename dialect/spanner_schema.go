package dialect

import (
	"fmt"
	"strings"

	"cloud.google.com/go/spanner"
)

var _ ColumnSchema = &spannerColumnSchema{}

type spannerColumnSchema struct {
	// information_schema.COLUMNS
	tableCatalog    string
	tableSchema     string
	tableName       string
	columnName      string
	ordinalPosition int64
	columnDefault   spanner.NullString
	dataType        spanner.NullString
	isNullable      string
	spannerType     string

	// information_schema.INDEX_COLUMNS
	columnOrdering spanner.NullString `spanner:"COLUMN_ORDERING"`

	// information_schema.INDEXES
	indexName        spanner.NullString `spanner:"INDEX_NAME"`
	indexType        spanner.NullString `spanner:"INDEX_TYPE"`
	parentTableName  spanner.NullString `spanner:"PARENT_TABLE_NAME"`
	isUnique         spanner.NullBool   `spanner:"IS_UNIQUE"`
	isNullFiltered   spanner.NullBool   `spanner:"IS_NULL_FILTERED"`
	indexState       spanner.NullString `spanner:"INDEX_STATE"`
	spannerIsManaged spanner.NullBool   `spanner:"SPANNER_IS_MANAGED"`

	// information_schema.COLUMN_OPTIONS
	optionName  spanner.NullString `spanner:"OPTION_NAME"`
	optionType  spanner.NullString `spanner:"OPTION_TYPE"`
	optionValue spanner.NullString `spanner:"OPTION_VALUE"`
}

func (s *spannerColumnSchema) TableName() string {
	return s.tableName
}

func (s *spannerColumnSchema) ColumnName() string {
	return s.columnName
}

func (s *spannerColumnSchema) ColumnType() string {
	return s.spannerType
}

func (s *spannerColumnSchema) DataType() string {
	return s.dataType.StringVal
}

func (s *spannerColumnSchema) GoType() string {
	return s.goType(s.spannerType, s.IsNullable())
}

func (s *spannerColumnSchema) IsPrimaryKey() bool {
	return s.indexType.Valid && s.indexType.StringVal == "PRIMARY_KEY"
}

func (s *spannerColumnSchema) IsAutoIncrement() bool {
	// Cloud Spanner have no auto_increment feature.
	return false
}

func (s *spannerColumnSchema) Index() (name string, unique bool, ok bool) {
	if !s.indexType.Valid || s.IsPrimaryKey() {
		return "", false, false
	}
	return s.indexName.StringVal, s.isUnique.Bool, true
}

func (s *spannerColumnSchema) Default() (string, bool) {
	// Cloud Spanner have no DEFAULT column value.
	return "", false
}

func (s *spannerColumnSchema) IsNullable() bool {
	return strings.ToUpper(s.isNullable) == "YES"
}

func (s *spannerColumnSchema) Extra() (string, bool) {
	if !(s.optionName.Valid && s.optionType.Valid && s.optionValue.Valid) {
		return "", false
	}
	return fmt.Sprintf("%s = %s", s.optionName.StringVal, strings.ToLower(s.optionValue.StringVal)), true
}

func (s *spannerColumnSchema) Comment() (string, bool) {
	// Cloud Spanner does not store any comments on a database table.
	return "", false
}

func (s *spannerColumnSchema) goType(typ string, nullable bool) string {
	if prefix := "ARRAY<"; strings.HasPrefix(typ, prefix) {
		start := len(prefix)
		end := strings.LastIndexByte(typ, '>')
		return fmt.Sprintf("[]%s", s.goType(typ[start:end], false))
	}
	if end := strings.IndexByte(typ, '('); end >= 0 {
		typ = typ[:end]
	}
	switch typ {
	case "STRING":
		if nullable {
			return "*string"
		}
		return "string"
	case "BYTES":
		return "[]byte"
	case "BOOL":
		if nullable {
			return "*bool"
		}
		return "bool"
	case "INT64":
		if nullable {
			return "*int64"
		}
		return "int64"
	case "FLOAT64":
		if nullable {
			return "*float64"
		}
		return "float64"
	case "TIMESTAMP":
		if nullable {
			return "*time.Time"
		}
		return "time.Time"
	case "DATE":
		if nullable {
			return "*civil.Date"
		}
		return "civil.Date"
	case "NUMERIC":
		if nullable {
			return "*big.Rat"
		}
		return "big.Rat"
	}
	return "interface{}"
}
