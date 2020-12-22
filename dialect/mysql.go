package dialect

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
)

var _ PrimaryKeyModifier = &MySQL{}

type MySQL struct {
	db      *sql.DB
	dbName  string
	version *mysqlVersion
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
	version, err := d.dbVersion()
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
		schema := &mysqlColumnSchema{
			version: version,
		}
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

func (d *MySQL) ImportPackage(schema ColumnSchema) string {
	switch schema.DataType() {
	case "datetime":
		return "time"
	}
	return ""
}

func (d *MySQL) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}

func (d *MySQL) QuoteString(s string) string {
	return fmt.Sprintf("'%s'", strings.Replace(s, "'", "''", -1))
}

func (d *MySQL) CreateTableSQL(table Table) []string {
	columns := make([]string, len(table.Fields))
	for i, f := range table.Fields {
		columns[i] = d.columnSQL(f)
	}
	if len(table.PrimaryKeys) > 0 {
		pkColumns := make([]string, len(table.PrimaryKeys))
		for i, pk := range table.PrimaryKeys {
			pkColumns[i] = d.Quote(pk)
		}
		columns = append(columns, fmt.Sprintf("PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
	}
	query := fmt.Sprintf("CREATE TABLE %s (\n"+
		"  %s\n"+
		")", d.Quote(table.Name), strings.Join(columns, ",\n  "))
	if table.Option != "" {
		query += " " + table.Option
	}
	return []string{query}
}

func (d *MySQL) AddColumnSQL(field Field) []string {
	return []string{fmt.Sprintf("ALTER TABLE %s ADD %s", d.Quote(field.Table), d.columnSQL(field))}
}

func (d *MySQL) DropColumnSQL(field Field) []string {
	return []string{fmt.Sprintf("ALTER TABLE %s DROP %s", d.Quote(field.Table), d.Quote(field.Name))}
}

func (d *MySQL) ModifyColumnSQL(oldField, newField Field) []string {
	return []string{fmt.Sprintf("ALTER TABLE %s CHANGE %s %s", d.Quote(newField.Table), d.Quote(oldField.Name), d.columnSQL(newField))}
}

func (d *MySQL) ModifyPrimaryKeySQL(oldPrimaryKeys, newPrimaryKeys []Field) []string {
	var tableName string
	if len(newPrimaryKeys) > 0 {
		tableName = newPrimaryKeys[0].Table
	} else {
		tableName = oldPrimaryKeys[0].Table
	}
	var specs []string
	if len(oldPrimaryKeys) > 0 {
		specs = append(specs, "DROP PRIMARY KEY")
	}
	pkColumns := make([]string, len(newPrimaryKeys))
	for i, pk := range newPrimaryKeys {
		pkColumns[i] = d.Quote(pk.Name)
	}
	specs = append(specs, fmt.Sprintf("ADD PRIMARY KEY (%s)", strings.Join(pkColumns, ", ")))
	return []string{fmt.Sprintf("ALTER TABLE %s %s", d.Quote(tableName), strings.Join(specs, ", "))}
}

func (d *MySQL) CreateIndexSQL(index Index) []string {
	columns := make([]string, len(index.Columns))
	for i, c := range index.Columns {
		columns[i] = d.Quote(c)
	}
	indexName := d.Quote(index.Name)
	tableName := d.Quote(index.Table)
	column := strings.Join(columns, ",")
	if index.Unique {
		return []string{fmt.Sprintf("CREATE UNIQUE INDEX %s ON %s (%s)", indexName, tableName, column)}
	}
	return []string{fmt.Sprintf("CREATE INDEX %s ON %s (%s)", indexName, tableName, column)}
}

func (d *MySQL) DropIndexSQL(index Index) []string {
	return []string{fmt.Sprintf("DROP INDEX %s ON %s", d.Quote(index.Name), d.Quote(index.Table))}
}

func (d *MySQL) columnSQL(f Field) string {
	column := []string{d.Quote(f.Name), f.Type}
	if !f.Nullable {
		column = append(column, "NOT NULL")
	}
	if def := f.Default; def != "" {
		if d.isTextType(f) {
			def = d.QuoteString(def)
		}
		column = append(column, "DEFAULT", def)
	}
	if f.AutoIncrement {
		column = append(column, "AUTO_INCREMENT")
	}
	if f.Extra != "" {
		column = append(column, f.Extra)
	}
	if f.Comment != "" {
		column = append(column, "COMMENT", d.QuoteString(f.Comment))
	}
	return strings.Join(column, " ")
}

func (d *MySQL) isTextType(f Field) bool {
	typ := strings.ToUpper(f.Type)
	for _, t := range []string{"VARCHAR", "CHAR", "TEXT", "MIDIUMTEXT", "LONGTEXt"} {
		if strings.HasPrefix(typ, t) {
			return true
		}
	}
	return false
}

func (d *MySQL) Begin() (Transactioner, error) {
	tx, err := d.db.Begin()
	if err != nil {
		return nil, err
	}
	return &mysqlTransaction{
		tx: tx,
	}, nil
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

func (d *MySQL) dbVersion() (*mysqlVersion, error) {
	if d.version != nil {
		return d.version, nil
	}
	var version string
	if err := d.db.QueryRow(`SELECT VERSION()`).Scan(&version); err != nil {
		return nil, err
	}
	vs := strings.Split(version, "-")
	vStr := vs[0]
	var v mysqlVersion
	if len(vs) > 1 {
		v.Name = vs[1]
	}
	versions := strings.Split(vStr, ".")
	var err error
	if v.Major, err = strconv.Atoi(versions[0]); err != nil {
		return nil, err
	}
	if v.Minor, err = strconv.Atoi(versions[1]); err != nil {
		return nil, err
	}
	if v.Patch, err = strconv.Atoi(versions[2]); err != nil {
		return nil, err
	}
	d.version = &v
	return d.version, err
}

func (d *MySQL) getIndexMap() (map[string]map[string]mysqlIndexInfo, error) {
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
	return indexMap, rows.Err()
}

type mysqlIndexInfo struct {
	NonUnique int64
	IndexName string
}

type mysqlVersion struct {
	Major int
	Minor int
	Patch int
	Name  string
}

type mysqlTransaction struct {
	tx *sql.Tx
}

func (m *mysqlTransaction) Exec(sql string, args ...interface{}) error {
	_, err := m.tx.Exec(sql, args...)
	return err
}

func (m *mysqlTransaction) Commit() error {
	return m.tx.Commit()
}

func (m *mysqlTransaction) Rollback() error {
	return m.tx.Rollback()
}
