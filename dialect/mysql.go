package dialect

import (
	"database/sql"
	"fmt"
	"strings"
)

type MySQL struct {
	db       *sql.DB
	dbName   string
	indexMap map[string]map[string]mysqlIndexInfo
}

func NewMySQL(db *sql.DB) Dialect {
	return &MySQL{
		db: db,
	}
}

func (d *MySQL) ColumnSchema(tables ...string) ([]ColumnSchema, error) {
	dbname, err := d.currentDBName()
	if err != nil {
		return nil, err
	}
	indexMap, err := d.getIndexMap()
	if err != nil {
		return nil, err
	}
	parts := []string{
		"SELECT",
		"  TABLE_NAME,",
		"  COLUMN_NAME,",
		"  COLUMN_DEFAULT,",
		"  IS_NULLABLE,",
		"  DATA_TYPE,",
		"  CHARACTER_MAXIMUM_LENGTH,",
		"  CHARACTER_OCTET_LENGTH,",
		"  NUMERIC_PRECISION,",
		"  NUMERIC_SCALE,",
		"  DATETIME_PRECISION,",
		"  COLUMN_TYPE,",
		"  COLUMN_KEY,",
		"  EXTRA,",
		"  COLUMN_COMMENT",
		"FROM information_schema.COLUMNS",
		"WHERE TABLE_SCHEMA = ?",
	}
	args := []interface{}{dbname}
	if len(tables) > 0 {
		placeholder := strings.Repeat(",?", len(tables))
		placeholder = placeholder[1:] // truncate the heading comma.
		parts = append(parts, fmt.Sprintf("AND TABLE_NAME IN (%s)", placeholder))
		for _, t := range tables {
			args = append(args, t)
		}
	}
	parts = append(parts, "ORDER BY TABLE_NAME, ORDINAL_POSITION")
	query := strings.Join(parts, "\n")
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schemas []ColumnSchema
	for rows.Next() {
		schema := &mysqlColumnSchema{}
		if err := rows.Scan(
			&schema.tableName,
			&schema.columnName,
			&schema.columnDefault,
			&schema.isNullable,
			&schema.dataType,
			&schema.characterMaximumLength,
			&schema.characterOctetLength,
			&schema.numericPrecision,
			&schema.numericScale,
			&schema.datetimePrecision,
			&schema.columnType,
			&schema.columnKey,
			&schema.extra,
			&schema.columnComment,
		); err != nil {
			return nil, err
		}
		if tableIndex, exists := indexMap[schema.tableName]; exists {
			if info, exists := tableIndex[schema.columnName]; exists {
				schema.nonUnique = info.NonUnique
				schema.indexName = info.IndexName
			}
		}
		schemas = append(schemas, schema)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return schemas, nil
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
	}
	return "", false, true
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

func (d *MySQL) currentDBName() (string, error) {
	if d.dbName != "" {
		return d.dbName, nil
	}
	err := d.db.QueryRow(`SELECT DATABASE()`).Scan(&d.dbName)
	return d.dbName, err
}

func (d *MySQL) getIndexMap() (map[string]map[string]mysqlIndexInfo, error) {
	if d.indexMap != nil {
		return d.indexMap, nil
	}
	dbname, err := d.currentDBName()
	if err != nil {
		return nil, err
	}
	query := strings.Join([]string{
		"SELECT",
		"  TABLE_NAME,",
		"  COLUMN_NAME,",
		"  NON_UNIQUE,",
		"  INDEX_NAME",
		"FROM information_schema.STATISTICS",
		"WHERE TABLE_SCHEMA = ?",
	}, "\n")
	rows, err := d.db.Query(query, dbname)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	indexMap := make(map[string]map[string]mysqlIndexInfo)
	for rows.Next() {
		var (
			tableName  string
			columnName string
			index      mysqlIndexInfo
		)
		if err := rows.Scan(&tableName, &columnName, &index.NonUnique, &index.IndexName); err != nil {
			return nil, err
		}
		if _, exists := indexMap[tableName]; !exists {
			indexMap[tableName] = make(map[string]mysqlIndexInfo)
		}
		indexMap[tableName][columnName] = index
	}
	d.indexMap = indexMap
	return indexMap, rows.Err()
}

type mysqlIndexInfo struct {
	NonUnique int64
	IndexName string
}
