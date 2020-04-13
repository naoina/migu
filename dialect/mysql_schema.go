package dialect

import (
	"database/sql"
	"strings"
)

var _ ColumnSchema = &mysqlColumnSchema{}

type mysqlColumnSchema struct {
	tableName              string
	columnName             string
	ordinalPosition        int64
	columnDefault          sql.NullString
	isNullable             string
	dataType               string
	characterMaximumLength *uint64
	characterOctetLength   sql.NullInt64
	numericPrecision       sql.NullInt64
	numericScale           sql.NullInt64
	datetimePrecision      sql.NullInt64
	columnType             string
	columnKey              string
	extra                  string
	columnComment          string
	nonUnique              int64
	indexName              string

	version *mysqlVersion
}

func (schema *mysqlColumnSchema) TableName() string {
	return schema.tableName
}

func (schema *mysqlColumnSchema) ColumnName() string {
	return schema.columnName
}

func (schema *mysqlColumnSchema) GoType() string {
	switch schema.dataType {
	case "tinyint":
		if schema.isUnsigned() {
			if schema.IsNullable() {
				return "*uint8"
			}
			return "uint8"
		}
		if schema.columnType == "tinyint(1)" {
			if schema.IsNullable() {
				return "*bool"
			}
			return "bool"
		}
		if schema.IsNullable() {
			return "*int8"
		}
		return "int8"
	case "smallint":
		if schema.isUnsigned() {
			if schema.IsNullable() {
				return "*uint16"
			}
			return "uint16"
		}
		if schema.IsNullable() {
			return "*int16"
		}
		return "int16"
	case "mediumint", "int":
		if schema.isUnsigned() {
			if schema.IsNullable() {
				return "*uint"
			}
			return "uint"
		}
		if schema.IsNullable() {
			return "*int"
		}
		return "int"
	case "bigint":
		if schema.isUnsigned() {
			if schema.IsNullable() {
				return "*uint64"
			}
			return "uint64"
		}
		if schema.IsNullable() {
			return "*int64"
		}
		return "int64"
	case "varchar", "text", "mediumtext", "longtext", "char":
		if schema.IsNullable() {
			return "*string"
		}
		return "string"
	case "varbinary", "binary":
		return "[]byte"
	case "datetime":
		if schema.IsNullable() {
			return "*time.Time"
		}
		return "time.Time"
	case "double", "float", "decimal":
		if schema.IsNullable() {
			return "*float64"
		}
		return "float64"
	}
	return "interface{}"
}

func (schema *mysqlColumnSchema) ColumnType() string {
	typ := schema.columnType
	switch schema.dataType {
	case "tinyint", "smallint", "mediumint", "int", "bigint":
		if typ == "tinyint(1)" {
			return typ
		}
		// NOTE: As of MySQL 8.0.17, the display width attribute is deprecated for integer data types.
		//		 See https://dev.mysql.com/doc/refman/8.0/en/numeric-type-syntax.html
		start, end := -1, -1
		for i := 0; i < len(typ); i++ {
			c := typ[i]
			if c == '(' {
				start = i
				continue
			}
			if c == ')' {
				end = i
				break
			}
		}
		if start < 0 || end < 0 {
			return typ
		}
		return typ[:start] + typ[end+1:]
	}
	return typ
}

func (schema *mysqlColumnSchema) DataType() string {
	return schema.dataType
}

func (schema *mysqlColumnSchema) IsPrimaryKey() bool {
	return schema.columnKey == "PRI" && strings.ToUpper(schema.indexName) == "PRIMARY"
}

func (schema *mysqlColumnSchema) IsAutoIncrement() bool {
	return schema.extra == "auto_increment"
}

func (schema *mysqlColumnSchema) Index() (name string, unique bool, ok bool) {
	if schema.indexName != "" && !schema.IsPrimaryKey() {
		return schema.indexName, schema.nonUnique == 0, true
	}
	return "", false, false
}

func (schema *mysqlColumnSchema) Default() (string, bool) {
	if !schema.columnDefault.Valid {
		return "", false
	}
	def := schema.columnDefault.String
	v := schema.version
	// See https://mariadb.com/kb/en/library/information-schema-columns-table/
	if v.Name == "MariaDB" && v.Major >= 10 && v.Minor >= 2 && v.Patch >= 7 {
		// unquote string
		if len(def) > 0 && def[0] == '\'' {
			def = def[1:]
		}
		if len(def) > 0 && def[len(def)-1] == '\'' {
			def = def[:len(def)-1]
		}
		def = strings.Replace(def, "''", "'", -1) // unescape string
	}
	if def == "NULL" {
		return "", false
	}
	if schema.dataType == "datetime" && def == "0000-00-00 00:00:00" {
		return "", false
	}
	// Trim parenthesis from like "on update current_timestamp()".
	def = strings.TrimSuffix(def, "()")
	return def, true
}

func (schema *mysqlColumnSchema) IsNullable() bool {
	return strings.ToUpper(schema.isNullable) == "YES"
}

func (schema *mysqlColumnSchema) Extra() (string, bool) {
	if schema.extra == "" || schema.IsAutoIncrement() {
		return "", false
	}
	// Trim parenthesis from like "on update current_timestamp()".
	extra := strings.TrimSuffix(schema.extra, "()")
	extra = strings.ToUpper(extra)
	return extra, true
}

func (schema *mysqlColumnSchema) Comment() (string, bool) {
	return schema.columnComment, schema.columnComment != ""
}

func (schema *mysqlColumnSchema) isUnsigned() bool {
	return strings.Contains(schema.columnType, "unsigned")
}
