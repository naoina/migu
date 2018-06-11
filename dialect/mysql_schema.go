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

func (schema *mysqlColumnSchema) DataType() string {
	if schema.dataType == "tinyint" && schema.columnType == "tinyint(1)" {
		return "tinyint(1)"
	}
	return schema.dataType
}

func (schema *mysqlColumnSchema) IsDatetime() bool {
	return schema.dataType == "datetime"
}

func (schema *mysqlColumnSchema) IsPrimaryKey() bool {
	return schema.columnKey == "PRI" && strings.ToUpper(schema.indexName) == "PRIMARY"
}

func (schema *mysqlColumnSchema) IsAutoIncrement() bool {
	return schema.extra == "auto_increment"
}

func (schema *mysqlColumnSchema) Index() *Index {
	if schema.indexName != "" && !schema.IsPrimaryKey() {
		return &Index{
			Name:   schema.indexName,
			Unique: schema.nonUnique == 0,
		}
	}
	return nil
}

func (schema *mysqlColumnSchema) Default() (string, bool) {
	return schema.columnDefault.String, schema.columnDefault.Valid && (schema.columnType != "datetime" || schema.columnDefault.String != "0000-00-00 00:00:00")
}

func (schema *mysqlColumnSchema) Size() (int64, bool) {
	if (schema.dataType == "varchar" || schema.dataType == "char") && schema.characterMaximumLength != nil {
		return int64(*schema.characterMaximumLength), true
	}
	if (schema.dataType == "varbinary" || schema.dataType == "binary") && schema.characterOctetLength.Valid {
		return schema.characterOctetLength.Int64, true
	}
	return 0, false
}

func (schema *mysqlColumnSchema) Precision() (int64, bool) {
	switch schema.dataType {
	case "decimal":
		return schema.numericPrecision.Int64, schema.numericPrecision.Valid
	case "datetime", "timestamp", "time":
		return schema.datetimePrecision.Int64, schema.datetimePrecision.Valid && schema.datetimePrecision.Int64 > 0
	}
	return 0, false
}

func (schema *mysqlColumnSchema) Scale() (int64, bool) {
	switch schema.dataType {
	case "decimal":
		return schema.numericScale.Int64, schema.numericScale.Valid && schema.numericScale.Int64 > 0
	case "double":
		return schema.numericScale.Int64, schema.numericScale.Valid
	}
	return 0, false
}

func (schema *mysqlColumnSchema) IsNullable() bool {
	return strings.ToUpper(schema.isNullable) == "YES"
}

func (schema *mysqlColumnSchema) Extra() (string, bool) {
	return strings.ToUpper(schema.extra), schema.extra != "" && !schema.IsAutoIncrement()
}

func (schema *mysqlColumnSchema) Comment() (string, bool) {
	return schema.columnComment, schema.columnComment != ""
}

func (schema *mysqlColumnSchema) isUnsigned() bool {
	return strings.Contains(schema.columnType, "unsigned")
}
