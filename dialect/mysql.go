package dialect

import (
	"fmt"
	"strings"
)

type MySQL struct {
}

func (d *MySQL) ColumnType(name string) (typ string, null bool) {
	switch name {
	case "string":
		return "VARCHAR(255)", false
	case "int":
		return "INT", false
	case "int64":
		return "BIGINT", false
	case "uint":
		return "INT UNSIGNED", false
	case "bool":
		return "BOOLEAN", false
	case "float32", "float64":
		return "DOUBLE", false
	case "sql.NullString", "*string":
		return "VARCHAR(255)", true
	case "sql.NullBool", "*bool":
		return "BOOLEAN", true
	case "sql.NullInt64", "*int64":
		return "BIGINT", true
	case "sql.NullFloat64", "*float64":
		return "DOUBLE", true
	default:
		return "VARCHAR(255)", true
	}
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}
