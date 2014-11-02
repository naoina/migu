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
	case "int":
		return "INT", false, true
	case "int64":
		return "BIGINT", false, true
	case "uint":
		return "INT UNSIGNED", false, true
	case "bool":
		return "BOOLEAN", false, false
	case "float32", "float64":
		return "DOUBLE", false, true
	case "sql.NullString", "*string":
		return "VARCHAR(255)", true, false
	case "sql.NullBool", "*bool":
		return "BOOLEAN", true, false
	case "sql.NullInt64", "*int64":
		return "BIGINT", true, true
	case "sql.NullFloat64", "*float64":
		return "DOUBLE", true, true
	default:
		return "VARCHAR(255)", true, false
	}
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}
