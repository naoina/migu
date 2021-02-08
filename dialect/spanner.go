package dialect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"google.golang.org/api/iterator"
	apioption "google.golang.org/api/option"
	databasepb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	"google.golang.org/grpc"
)

var (
	spannerColumnTypes = []*ColumnType{
		{
			Types:           []string{"STRING(MAX)"},
			GoTypes:         []string{"string"},
			GoNullableTypes: []string{"*string", "spanner.NullString"},
		},
		{
			Types:           []string{"BYTES(MAX)"},
			GoTypes:         []string{"[]byte"},
			GoNullableTypes: []string{"[]byte"},
		},
		{
			Types:           []string{"BOOL"},
			GoTypes:         []string{"bool"},
			GoNullableTypes: []string{"*bool", "spanner.NullBool"},
		},
		{
			Types:           []string{"INT64"},
			GoTypes:         []string{"int64", "int", "int8", "int16", "int32", "uint8", "uint16", "uint32", "uint64"},
			GoNullableTypes: []string{"*int64", "spanner.NullInt64"},
		},
		{
			Types:           []string{"FLOAT64"},
			GoTypes:         []string{"float64", "float32"},
			GoNullableTypes: []string{"*float64", "spanner.NullFloat64"},
		},
		{
			Types:           []string{"TIMESTAMP"},
			GoTypes:         []string{"time.Time"},
			GoNullableTypes: []string{"*time.Time", "spanner.NullTime"},
		},
		{
			Types:           []string{"DATE"},
			GoTypes:         []string{"civil.Date"},
			GoNullableTypes: []string{"*civil.Date", "spanner.NullDate"},
		},
		{
			Types:           []string{"NUMERIC"},
			GoTypes:         []string{"big.Rat"},
			GoNullableTypes: []string{"*big.Rat", "spanner.NullNumeric"},
		},
	}
)

type Spanner struct {
	ac              *database.DatabaseAdminClient
	c               *spanner.Client
	database        string
	opt             *option
	columnTypeMap   map[string]*ColumnType
	nullableTypeMap map[string]struct{}
}

func NewSpanner(database string, opts ...Option) Dialect {
	d := &Spanner{
		database:        database,
		opt:             newOption(),
		columnTypeMap:   map[string]*ColumnType{},
		nullableTypeMap: map[string]struct{}{},
	}
	for _, o := range opts {
		o(d.opt)
	}
	for _, types := range [][]*ColumnType{spannerColumnTypes, d.opt.columnTypes} {
		for _, t := range types {
			for _, tt := range t.allGoTypes() {
				d.columnTypeMap[tt] = t
			}
			for _, tt := range t.filteredNullableGoTypes() {
				d.nullableTypeMap[tt] = struct{}{}
			}
		}
	}
	return d
}

func (s *Spanner) ColumnSchema(tables ...string) ([]ColumnSchema, error) {
	parts := []string{
		"SELECT",
		"  C.table_catalog,",
		"  C.table_schema,",
		"  C.table_name,",
		"  C.column_name,",
		"  C.ordinal_position,",
		// "  C.column_default,",
		// "  C.data_type,",
		"  C.is_nullable,",
		"  C.spanner_type,",
		"  CO.option_name,",
		"  CO.option_type,",
		"  CO.option_value,",
		"  I.index_name,",
		"  I.index_type,",
		"  I.parent_table_name,",
		"  I.is_unique,",
		"  I.is_null_filtered,",
		"  I.index_state,",
		// "  I.spanner_is_managed",
		"FROM information_schema.columns AS c",
		"LEFT OUTER JOIN information_schema.column_options AS co",
		"  ON co.table_name = c.table_name AND co.column_name = c.column_name",
		"LEFT OUTER JOIN information_schema.index_columns AS ic",
		"  ON ic.table_name = c.table_name AND ic.column_name = c.column_name",
		"LEFT OUTER JOIN information_schema.indexes AS i",
		"  ON i.table_name = ic.table_name AND i.index_name = ic.index_name",
		"WHERE",
		"  c.table_schema = ''",
	}
	params := map[string]interface{}{}
	if len(tables) > 0 {
		parts = append(parts, "AND c.table_name IN UNNEST(@tables)")
		params["tables"] = tables
	}
	parts = append(parts, "ORDER BY c.table_name, c.ordinal_position")
	query := strings.Join(parts, "\n")
	stmt := spanner.Statement{
		SQL:    query,
		Params: params,
	}
	client, err := s.client()
	if err != nil {
		return nil, err
	}
	iter := client.Single().Query(context.Background(), stmt)
	defer iter.Stop()
	var schemas []ColumnSchema
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var schema spannerColumnSchema
		if err := row.Columns(
			&schema.tableCatalog,
			&schema.tableSchema,
			&schema.tableName,
			&schema.columnName,
			&schema.ordinalPosition,
			&schema.isNullable,
			&schema.spannerType,
			&schema.optionName,
			&schema.optionType,
			&schema.optionValue,
			&schema.indexName,
			&schema.indexType,
			&schema.parentTableName,
			&schema.isUnique,
			&schema.isNullFiltered,
			&schema.indexState,
		); err != nil {
			return nil, err
		}
		schemas = append(schemas, &schema)
	}
	return schemas, nil
}

func (s *Spanner) ColumnType(name string) string {
	name = strings.TrimLeft(name, "*")
	if t, ok := s.columnTypeMap[name]; ok {
		n, _, _, _ := t.findType(name)
		return n
	}
	if strings.HasPrefix(name, "[]") {
		return fmt.Sprintf("ARRAY<%s>", s.ColumnType(name[2:]))
	}
	return strings.ToUpper(name)
}

func (s *Spanner) GoType(name string, nullable bool) string {
	name = strings.ToUpper(name)
	if prefix := "ARRAY<"; strings.HasPrefix(name, prefix) {
		start := len(prefix)
		end := strings.LastIndexByte(name, '>')
		return fmt.Sprintf("[]%s", s.GoType(name[start:end], false))
	}
	for _, t := range spannerColumnTypes {
		if typ, found := t.findGoType(name, nullable, false); found {
			return typ
		}
	}
	if end := strings.IndexByte(name, '('); end >= 0 {
		return s.GoType(name[:end]+"(MAX)", nullable)
	}
	return "interface{}"
}

func (s *Spanner) IsNullable(name string) bool {
	_, ok := s.nullableTypeMap[name]
	return ok
}

func (s *Spanner) ImportPackage(schema ColumnSchema) string {
	t := schema.ColumnType()
	if strings.Contains(t, "TIMESTAMP") {
		return "time"
	}
	if strings.Contains(t, "DATE") {
		return "cloud.google.com/go/civil"
	}
	if strings.Contains(t, "NUMERIC") {
		return "math/big"
	}
	return ""
}

func (d *Spanner) Quote(s string) string {
	return fmt.Sprintf("`%s`", strings.Replace(s, "`", "``", -1))
}

func (d *Spanner) QuoteString(s string) string {
	return fmt.Sprintf("'%s'", strings.Replace(s, "'", `\'`, -1))
}

func (d *Spanner) CreateTableSQL(table Table) []string {
	columns := make([]string, len(table.Fields))
	for i, f := range table.Fields {
		columns[i] = d.columnSQL(f)
		if !f.Nullable {
			columns[i] += " NOT NULL"
		}
		if s := f.Extra; s != "" {
			columns[i] += fmt.Sprintf(" OPTIONS (%s)", s)
		}
	}
	pks := make([]string, len(table.PrimaryKeys))
	for i, pk := range table.PrimaryKeys {
		pks[i] = d.Quote(pk)
	}
	return []string{
		fmt.Sprintf("CREATE TABLE %s (\n"+
			"  %s\n"+
			") PRIMARY KEY (%s)", d.Quote(table.Name), strings.Join(columns, ",\n  "), strings.Join(pks, ", ")),
	}
}

func (d *Spanner) AddColumnSQL(field Field) []string {
	tableName := d.Quote(field.Table)
	ret := []string{
		fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", tableName, d.columnSQL(field)),
	}
	if s := field.Extra; s != "" {
		ret[0] += fmt.Sprintf(" OPTIONS (%s)", s)
	}
	if !field.Nullable {
		ret = append(ret, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s NOT NULL", tableName, d.columnSQL(field)))
	}
	return ret
}

func (d *Spanner) DropColumnSQL(field Field) []string {
	return []string{fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", d.Quote(field.Table), d.Quote(field.Name))}
}

func (d *Spanner) ModifyColumnSQL(oldField, newField Field) []string {
	ret := make([]string, 0, 2)
	switch {
	case (oldField.Nullable && !newField.Nullable) || (oldField.Type != newField.Type && !newField.Nullable):
		ret = append(ret, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s NOT NULL", d.Quote(newField.Table), d.columnSQL(newField)))
	case (!oldField.Nullable && newField.Nullable) || (oldField.Type != newField.Type && newField.Nullable):
		ret = append(ret, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s", d.Quote(newField.Table), d.columnSQL(newField)))
	}
	switch {
	case oldField.Extra == "" && newField.Extra != "":
		ret = append(ret, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET OPTIONS (%s)", d.Quote(newField.Table), d.Quote(newField.Name), newField.Extra))
	case oldField.Extra != "" && newField.Extra == "":
		optName := strings.TrimSpace(oldField.Extra[:strings.IndexByte(oldField.Extra, '=')])
		ret = append(ret, fmt.Sprintf("ALTER TABLE %s ALTER COLUMN %s SET OPTIONS (%s = null)", d.Quote(newField.Table), d.Quote(newField.Name), optName))
	}
	return ret
}

func (d *Spanner) CreateIndexSQL(index Index) []string {
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

func (d *Spanner) DropIndexSQL(index Index) []string {
	return []string{fmt.Sprintf("DROP INDEX %s", d.Quote(index.Name))}
}

func (d *Spanner) columnSQL(f Field) string {
	return strings.Join([]string{d.Quote(f.Name), f.Type}, " ")
}

func (d *Spanner) Begin() (Transactioner, error) {
	return &spannerTransaction{
		d: d,
	}, nil
}

func (d *Spanner) client() (*spanner.Client, error) {
	if d.c != nil {
		return d.c, nil
	}
	c, err := spanner.NewClient(context.Background(), d.database,
		apioption.WithGRPCDialOption(grpc.WithBlock()),
		apioption.WithGRPCDialOption(grpc.WithTimeout(1*time.Second)),
		apioption.WithGRPCDialOption(grpc.WithDefaultCallOptions(grpc.WaitForReady(false))),
	)
	if err != nil {
		return nil, err
	}
	d.c = c
	return c, nil
}

func (d *Spanner) adminClient() (*database.DatabaseAdminClient, error) {
	if d.ac != nil {
		return d.ac, nil
	}
	c, err := database.NewDatabaseAdminClient(context.Background(),
		apioption.WithGRPCDialOption(grpc.WithBlock()),
		apioption.WithGRPCDialOption(grpc.WithTimeout(1*time.Second)),
		apioption.WithGRPCDialOption(grpc.WithDefaultCallOptions(grpc.WaitForReady(false))),
	)
	if err != nil {
		return nil, err
	}
	d.ac = c
	return c, nil
}

type spannerTransaction struct {
	d *Spanner
}

func (s *spannerTransaction) Exec(sql string, args ...interface{}) error {
	ctx := context.Background()
	ac, err := s.d.adminClient()
	if err != nil {
		return err
	}
	op, err := ac.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:   s.d.database,
		Statements: []string{sql},
	})
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func (s *spannerTransaction) Commit() error {
	return s.close()
}

func (s *spannerTransaction) Rollback() error {
	return s.close()
}

func (s *spannerTransaction) close() error {
	if s.d.c != nil {
		s.d.c.Close()
		s.d.c = nil
	}
	if s.d.ac != nil {
		err := s.d.ac.Close()
		s.d.ac = nil
		return err
	}
	return nil
}

var _ ColumnSchema = &spannerColumnSchema{}

type spannerColumnSchema struct {
	// information_schema.COLUMNS
	tableCatalog    string
	tableSchema     string
	tableName       string
	columnName      string
	ordinalPosition int64
	columnDefault   spanner.NullString
	dataType        spanner.NullString
	isNullable      string
	spannerType     string

	// information_schema.INDEX_COLUMNS
	columnOrdering spanner.NullString `spanner:"COLUMN_ORDERING"`

	// information_schema.INDEXES
	indexName        spanner.NullString `spanner:"INDEX_NAME"`
	indexType        spanner.NullString `spanner:"INDEX_TYPE"`
	parentTableName  spanner.NullString `spanner:"PARENT_TABLE_NAME"`
	isUnique         spanner.NullBool   `spanner:"IS_UNIQUE"`
	isNullFiltered   spanner.NullBool   `spanner:"IS_NULL_FILTERED"`
	indexState       spanner.NullString `spanner:"INDEX_STATE"`
	spannerIsManaged spanner.NullBool   `spanner:"SPANNER_IS_MANAGED"`

	// information_schema.COLUMN_OPTIONS
	optionName  spanner.NullString `spanner:"OPTION_NAME"`
	optionType  spanner.NullString `spanner:"OPTION_TYPE"`
	optionValue spanner.NullString `spanner:"OPTION_VALUE"`
}

func (s *spannerColumnSchema) TableName() string {
	return s.tableName
}

func (s *spannerColumnSchema) ColumnName() string {
	return s.columnName
}

func (s *spannerColumnSchema) ColumnType() string {
	return s.spannerType
}

func (s *spannerColumnSchema) DataType() string {
	return s.dataType.StringVal
}

func (s *spannerColumnSchema) IsPrimaryKey() bool {
	return s.indexType.Valid && s.indexType.StringVal == "PRIMARY_KEY"
}

func (s *spannerColumnSchema) IsAutoIncrement() bool {
	// Cloud Spanner have no auto_increment feature.
	return false
}

func (s *spannerColumnSchema) Index() (name string, unique bool, ok bool) {
	if !s.indexType.Valid || s.IsPrimaryKey() {
		return "", false, false
	}
	return s.indexName.StringVal, s.isUnique.Bool, true
}

func (s *spannerColumnSchema) Default() (string, bool) {
	// Cloud Spanner have no DEFAULT column value.
	return "", false
}

func (s *spannerColumnSchema) IsNullable() bool {
	return strings.ToUpper(s.isNullable) == "YES"
}

func (s *spannerColumnSchema) Extra() (string, bool) {
	if !(s.optionName.Valid && s.optionType.Valid && s.optionValue.Valid) {
		return "", false
	}
	return fmt.Sprintf("%s = %s", s.optionName.StringVal, strings.ToLower(s.optionValue.StringVal)), true
}

func (s *spannerColumnSchema) Comment() (string, bool) {
	// Cloud Spanner does not store any comments on a database table.
	return "", false
}
