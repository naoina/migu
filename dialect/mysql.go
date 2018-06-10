package dialect

import (
	"fmt"
	"strings"
)

type MySQL struct {
}

func (d *MySQL) ColumnType(name string, size uint64, autoIncrement bool) (typ string, unsigned, null bool) {
	if name[0] == '*' {
		null = true
		name = name[1:]
	}
	switch name {
	case "string":
		return d.varchar(size), false, null
	case "sql.NullString":
		return d.varchar(size), false, true
	case "[]byte":
		return "VARBINARY", false, true
	case "int", "int32":
		return "INT", false, null
	case "int8":
		return "TINYINT", false, null
	case "bool":
		return "TINYINT(1)", false, null
	case "sql.NullBool":
		return "TINYINT(1)", false, true
	case "int16":
		return "SMALLINT", false, null
	case "int64":
		return "BIGINT", false, null
	case "sql.NullInt64":
		return "BIGINT", false, true
	case "uint", "uint32":
		return "INT", true, null
	case "uint8":
		return "TINYINT", true, null
	case "uint16":
		return "SMALLINT", true, null
	case "uint64":
		return "BIGINT", true, null
	case "float32", "float64":
		return "DOUBLE", false, null
	case "sql.NullFloat64":
		return "DOUBLE", false, true
	case "time.Time":
		return "DATETIME", false, null
	case "mysql.NullTime", "gorp.NullTime":
		return "DATETIME", false, true
	default:
		return "VARCHAR", false, true
	}
}

func (d *MySQL) DataType(name string, size uint64, unsigned bool, prec, scale int64) string {
	switch name = strings.ToUpper(name); name {
	case "TINYINT", "SMALLINT", "MEDIUMINT", "INT", "INTEGER", "BIGINT":
		if unsigned {
			name += " UNSIGNED"
		}
		return name
	case "DECIMAL", "DEC", "FLOAT":
		if prec > 0 {
			if scale > 0 {
				name += fmt.Sprintf("(%d,%d)", prec, scale)
			} else {
				name += fmt.Sprintf("(%d)", prec)
			}
			if unsigned {
				name += " UNSIGNED"
			}
		}
		return name
	case "DOUBLE":
		if prec > 0 {
			name += fmt.Sprintf("(%d,%d)", prec, scale)
		}
		if unsigned {
			name += " UNSIGNED"
		}
		return name
	case "VARCHAR", "BINARY", "VARBINARY":
		if size < 1 {
			size = 255
		}
		return fmt.Sprintf("%s(%d)", name, size)
	case "CHAR", "BLOB", "TEXT":
		if size > 0 {
			return fmt.Sprintf("%s(%d)", name, size)
		}
		return name
	case "DATETIME", "TIMESTAMP", "TIME", "YEAR":
		if prec > 0 {
			name += fmt.Sprintf("(%d)", prec)
		}
		return name
	case "BIT", "BOOL", "BOOLEAN", "TINYINT(1)", "DATE", "TINYBLOB", "TINYTEXT", "MEDIUMBLOB", "MEDIUMTEXT", "LONGBLOB", "LONGTEXT":
		return name
	case "ENUM":
		return name
	case "SET":
		return name
	}
	return ""
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}

func (d *MySQL) QuoteString(s string) string {
	return fmt.Sprintf("'%s'", strings.Replace(s, "'", "''", -1))
}

func (d *MySQL) AutoIncrement() string {
	return "AUTO_INCREMENT"
}

func (d *MySQL) varchar(size uint64) string {
	switch {
	case size < 21846:
		return "VARCHAR"
	case size < (1<<16)-1-2: // approximate 64KB.
		// 65533 ((2^16) - 1) - (length of prefix)
		// See http://dev.mysql.com/doc/refman/5.5/en/string-type-overview.html#idm140418628949072
		return "TEXT"
	case size < 1<<24: // 16MB.
		return "MEDIUMTEXT"
	}
	return "LONGTEXT"
}
