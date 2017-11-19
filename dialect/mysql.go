package dialect

import (
	"fmt"
	"strings"
)

type MySQL struct {
}

func (d *MySQL) ColumnType(name string, size uint64, autoIncrement bool) (typ string, null bool) {
	if name[0] == '*' {
		null = true
		name = name[1:]
	}
	switch name {
	case "string":
		return d.varchar(size), null
	case "sql.NullString":
		return d.varchar(size), true
	case "int", "int32":
		return "INT", null
	case "int8":
		return "TINYINT", null
	case "bool":
		return "TINYINT(1)", null
	case "sql.NullBool":
		return "TINYINT(1)", true
	case "int16":
		return "SMALLINT", null
	case "int64":
		return "BIGINT", null
	case "sql.NullInt64":
		return "BIGINT", true
	case "uint", "uint32":
		return "INT UNSIGNED", null
	case "uint8":
		return "TINYINT UNSIGNED", null
	case "uint16":
		return "SMALLINT UNSIGNED", null
	case "uint64":
		return "BIGINT UNSIGNED", null
	case "float32", "float64":
		return "DOUBLE", null
	case "sql.NullFloat64":
		return "DOUBLE", true
	case "time.Time":
		return "DATETIME", null
	default:
		return "VARCHAR(255)", true
	}
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
		return fmt.Sprintf("VARCHAR(%d)", size)
	case size < (1<<16)-1-2: // approximate 64KB.
		// 65533 ((2^16) - 1) - (length of prefix)
		// See http://dev.mysql.com/doc/refman/5.5/en/string-type-overview.html#idm140418628949072
		return "TEXT"
	case size < 1<<24: // 16MB.
		return "MEDIUMTEXT"
	}
	return "LONGTEXT"
}
