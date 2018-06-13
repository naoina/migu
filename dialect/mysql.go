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

func (d *MySQL) ColumnType(name string) string {
	name, unsigned := d.columnType(name)
	name = d.defaultColumnType(name)
	if unsigned {
		name += " UNSIGNED"
	}
	return strings.ToUpper(name)
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

func (d *MySQL) columnType(name string) (typ string, unsigned bool) {
	switch name {
	case "string":
		return "VARCHAR", false
	case "sql.NullString":
		return "VARCHAR", false
	case "[]byte":
		return "VARBINARY", false
	case "int", "int32":
		return "INT", false
	case "int8":
		return "TINYINT", false
	case "bool":
		return "TINYINT(1)", false
	case "sql.NullBool":
		return "TINYINT(1)", false
	case "int16":
		return "SMALLINT", false
	case "int64":
		return "BIGINT", false
	case "sql.NullInt64":
		return "BIGINT", false
	case "uint", "uint32":
		return "INT", true
	case "uint8":
		return "TINYINT", true
	case "uint16":
		return "SMALLINT", true
	case "uint64":
		return "BIGINT", true
	case "float32", "float64":
		return "DOUBLE", false
	case "sql.NullFloat64":
		return "DOUBLE", false
	case "time.Time":
		return "DATETIME", false
	case "mysql.NullTime", "gorp.NullTime":
		return "DATETIME", false
	}
	return name, false
}

func (d *MySQL) defaultColumnType(name string) string {
	switch name := strings.ToUpper(name); name {
	case "TINYINT":
		return "TINYINT(4)"
	case "SMALLINT":
		return "SMALLINT(6)"
	case "MEDIUMINT":
		return "MEDIUMINT(9)"
	case "INT":
		return "INT(11)"
	case "BIGINT":
		return "BIGINT(20)"
	case "BIT":
		return "BIT(1)"
	case "DECIMAL":
		return "DECIMAL(10,0)"
	case "VARCHAR":
		return "VARCHAR(255)"
	case "VARBINARY":
		return "VARBINARY(255)"
	case "CHAR":
		return "CHAR(1)"
	case "BINARY":
		return "BINARY(1)"
	case "YEAR":
		return "YEAR(4)"
	}
	return name
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
