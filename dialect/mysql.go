package dialect

import (
	"fmt"
	"strings"
)

type MySQL struct {
}

func (d *MySQL) ColumnType(name string) (typ string, null, autoIncrementable bool) {
	switch name {
	case "string":
		return "VARCHAR(255)", false, false
	case "sql.NullString", "*string":
		return "VARCHAR(255)", true, false
	case "int", "int32":
		return "INT", false, true
	case "*int", "*int32":
		return "INT", true, true
	case "int8":
		return "TINYINT", false, true
	case "*int8":
		return "TINYINT", true, true
	case "bool":
		return "TINYINT", false, false
	case "*bool", "sql.NullBool":
		return "TINYINT", true, false
	case "int16":
		return "SMALLINT", false, true
	case "*int16":
		return "SMALLINT", true, true
	case "int64":
		return "BIGINT", false, true
	case "sql.NullInt64", "*int64":
		return "BIGINT", true, true
	case "uint", "uint32":
		return "INT UNSIGNED", false, true
	case "*uint", "*uint32":
		return "INT UNSIGNED", true, true
	case "uint8":
		return "TINYINT UNSIGNED", false, true
	case "*uint8":
		return "TINYINT UNSIGNED", true, true
	case "uint16":
		return "SMALLINT UNSIGNED", false, true
	case "*uint16":
		return "SMALLINT UNSIGNED", true, true
	case "uint64":
		return "BIGINT UNSIGNED", false, true
	case "*uint64":
		return "BIGINT UNSIGNED", true, true
	case "float32", "float64":
		return "DOUBLE", false, true
	case "sql.NullFloat64", "*float32", "*float64":
		return "DOUBLE", true, true
	case "time.Time":
		return "DATETIME", false, false
	case "*time.Time":
		return "DATETIME", true, false
	default:
		return "VARCHAR(255)", true, false
	}
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}
