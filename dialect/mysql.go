package dialect

import (
	"fmt"
	"strings"
)

type MySQL struct {
}

func (d *MySQL) ColumnType(name string) string {
	switch name {
	case "string":
		return "VARCHAR(255) NOT NULL"
	case "int":
		return "INT NOT NULL"
	case "int64":
		return "BIGINT NOT NULL"
	case "uint":
		return "INT UNSIGNED NOT NULL"
	case "bool":
		return "BOOLEAN NOT NULL"
	case "float32", "float64":
		return "DOUBLE NOT NULL"
	case "sql.NullString", "*string":
		return "VARCHAR(255)"
	case "sql.NullBool", "*bool":
		return "BOOLEAN"
	case "sql.NullInt64", "*int64":
		return "BIGINT"
	case "sql.NullFloat64", "*float64":
		return "DOUBLE"
	default:
		return "VARCHAR(255)"
	}
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}
