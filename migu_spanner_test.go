//+build spanner

package migu_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"github.com/google/go-cmp/cmp"
	"github.com/naoina/migu"
	"github.com/naoina/migu/dialect"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	databasepb "google.golang.org/genproto/googleapis/spanner/admin/database/v1"
	"google.golang.org/grpc"
)

var dsn string
var client *spanner.Client
var adminClient *database.DatabaseAdminClient

func exec(queries []string) (err error) {
	if len(queries) == 0 {
		return nil
	}
	stmts := make([]spanner.Statement, len(queries))
	for i, query := range queries {
		stmts[i] = spanner.NewStatement(query)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	op, err := adminClient.UpdateDatabaseDdl(ctx, &databasepb.UpdateDatabaseDdlRequest{
		Database:   dsn,
		Statements: queries,
	})
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func cleanup(t *testing.T) {
	iter := client.Single().Query(context.Background(), spanner.NewStatement(`SELECT index_name FROM information_schema.indexes WHERE index_name != "PRIMARY_KEY"`))
	var indexes []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatalf("%+v\n", err)
		}
		var index string
		if err := row.ColumnByName("index_name", &index); err != nil {
			t.Fatalf("%+v\n", err)
		}
		indexes = append(indexes, index)
	}
	iter = client.Single().Query(context.Background(), spanner.NewStatement("SELECT table_name FROM information_schema.tables WHERE TABLE_SCHEMA = ''"))
	var tables []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			t.Fatalf("%+v\n", err)
		}
		var table string
		if err := row.ColumnByName("table_name", &table); err != nil {
			t.Fatalf("%+v\n", err)
		}
		tables = append(tables, table)
	}
	queries := make([]string, 0, len(indexes)+len(tables))
	for _, index := range indexes {
		queries = append(queries, fmt.Sprintf("DROP INDEX `%s`", index))
	}
	for _, table := range tables {
		queries = append(queries, fmt.Sprintf("DROP TABLE `%s`", table))
	}
	if err := exec(queries); err != nil {
		t.Fatalf("%+v\n", err)
	}
}

func TestMain(m *testing.M) {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	if err := os.Setenv("SPANNER_EMULATOR_HOST", dbHost+":9010"); err != nil {
		panic(err)
	}
	project := os.Getenv("SPANNER_PROJECT_ID")
	instance := os.Getenv("SPANNER_INSTANCE_ID")
	dbname := os.Getenv("SPANNER_DATABASE_ID")
	dsn = path.Join("projects", project, "instances", instance, "databases", dbname)
	c, err := spanner.NewClient(context.Background(), dsn,
		option.WithGRPCDialOption(grpc.WithBlock()),
		option.WithGRPCDialOption(grpc.WithTimeout(1*time.Second)),
		option.WithGRPCDialOption(grpc.WithDefaultCallOptions(grpc.WaitForReady(false))),
	)
	if err != nil {
		panic(err)
	}
	client = c
	ac, err := database.NewDatabaseAdminClient(context.Background(),
		option.WithGRPCDialOption(grpc.WithBlock()),
		option.WithGRPCDialOption(grpc.WithTimeout(1*time.Second)),
		option.WithGRPCDialOption(grpc.WithDefaultCallOptions(grpc.WaitForReady(false))),
	)
	if err != nil {
		panic(err)
	}
	adminClient = ac
	os.Exit(func() int {
		return m.Run()
	}())
}

func TestDiff(t *testing.T) {
	d := dialect.NewSpanner(dsn)
	t.Run("idempotency", func(t *testing.T) {
		for _, v := range []struct {
			column string
		}{
			{"Name string `migu:\"pk\"`"},
			{"Name string `migu:\"pk,type:STRING(255)\"`"},
		} {
			v := v
			t.Run(fmt.Sprintf("%v", v.column), func(t *testing.T) {
				defer cleanup(t)
				src := fmt.Sprintf("package migu_test\n"+
					"//+migu\n"+
					"type User struct {\n"+
					"	%s\n"+
					"}", v.column)
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				if results == nil {
					t.Fatalf("results must be not nil; got %#v", results)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
				actual, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				expect := []string(nil)
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Errorf("(-got +want)\n%v", diff)
				}
			})
		}
	})

	t.Run("single primary key", func(t *testing.T) {
		defer cleanup(t)
		src := strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	ID uint64 `migu:\"pk\"`",
			"}",
		}, "\n")
		results, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		var actual interface{} = results
		var expect interface{} = []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `id` INT64 NOT NULL",
				") PRIMARY KEY (`id`)",
			}, "\n"),
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
		if err := exec(results); err != nil {
			t.Fatal(err)
		}
		actual, err = migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect = []string(nil)
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("multiple column primary key", func(t *testing.T) {
		defer cleanup(t)
		src := strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	UserID uint64 `migu:\"pk\"`",
			"	ProfileID uint64 `migu:\"pk\"`",
			"}",
		}, "\n")
		results, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		var actual interface{} = results
		var expect interface{} = []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `user_id` INT64 NOT NULL,",
				"  `profile_id` INT64 NOT NULL",
				") PRIMARY KEY (`user_id`, `profile_id`)",
			}, "\n"),
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
		if err := exec(results); err != nil {
			t.Fatal(err)
		}
		actual, err = migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect = []string(nil)
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("index", func(t *testing.T) {
		defer cleanup(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `id` INT64 NOT NULL,\n" +
					"  `age` INT64 NOT NULL,\n" +
					"  `created_at` TIMESTAMP NOT NULL\n" +
					") PRIMARY KEY (`id`)",
				"CREATE INDEX `user_age` ON `user` (`age`)",
			}},
			{2, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int64 `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"CREATE INDEX `user_created_at` ON `user` (`created_at`)",
			}},
			{3, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"DROP INDEX `user_age`",
				"CREATE INDEX `age_index` ON `user` (`age`)",
			}},
			{4, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_created_at`",
			}},
			{5, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index:age_created_at_index\"`",
				"CreatedAt time.Time `migu:\"index:age_created_at_index\"`",
			}, []string{
				"DROP INDEX `age_index`",
				"CREATE INDEX `age_created_at_index` ON `user` (`age`,`created_at`)",
			}},
			{6, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_created_at_index`",
			}},
			{7, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
			}},
			{8, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"CREATE UNIQUE INDEX `user_created_at` ON `user` (`created_at`)",
			}},
			{9, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"DROP INDEX `user_age`",
				"CREATE INDEX `user_age` ON `user` (`age`)",
			}},
			{10, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_age`",
				"DROP INDEX `user_created_at`",
				"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
			}},
			{11, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"unique:age_unique_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_age`",
				"CREATE UNIQUE INDEX `age_unique_index` ON `user` (`age`)",
			}},
			{12, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_unique_index`",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := fmt.Sprintf("package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
					strings.Join(v.columns, "\n") + "\n" +
					"}")
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				actual := results
				expect := v.expect
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Errorf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
			}) {
				return
			}
		}
	})

	t.Run("unique index at table creation", func(t *testing.T) {
		defer cleanup(t)
		src := fmt.Sprintf("package migu_test\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	ID int64 `migu:\"pk\"`\n" +
			"	Age int `migu:\"unique\"`\n" +
			"}")
		actual, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `id` INT64 NOT NULL,\n" +
				"  `age` INT64 NOT NULL\n" +
				") PRIMARY KEY (`id`)",
			"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("multiple unique indexes", func(t *testing.T) {
		defer cleanup(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"unique:age_created_at_unique_index\"`",
				"CreatedAt time.Time `migu:\"unique:age_created_at_unique_index\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `id` INT64 NOT NULL,\n" +
					"  `age` INT64 NOT NULL,\n" +
					"  `created_at` TIMESTAMP NOT NULL\n" +
					") PRIMARY KEY (`id`)",
				"CREATE UNIQUE INDEX `age_created_at_unique_index` ON `user` (`age`,`created_at`)",
			}},
			{2, []string{
				"ID int64 `migu:\"pk\"`",
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique:created_at_unique_index\"`",
			}, []string{
				"DROP INDEX `age_created_at_unique_index`",
				"CREATE INDEX `user_age` ON `user` (`age`)",
				"CREATE UNIQUE INDEX `created_at_unique_index` ON `user` (`created_at`)",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := fmt.Sprintf("package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
					strings.Join(v.columns, "\n") + "\n" +
					"}")
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				actual := results
				expect := v.expect
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Errorf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
			}) {
				return
			}
		}
	})

	t.Run("ALTER TABLE", func(t *testing.T) {
		defer cleanup(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Age int `migu:\"pk\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT64 NOT NULL\n" +
					") PRIMARY KEY (`age`)",
			}},
			{2, []string{
				"Age uint8 `migu:\"pk\"`",
				"Old uint8 `migu:\"column:col_b\"`",
			}, []string{
				"ALTER TABLE `user` ADD COLUMN `col_b` INT64",
				"ALTER TABLE `user` ALTER COLUMN `col_b` INT64 NOT NULL",
			}},
			{3, []string{
				"Age int `migu:\"pk\"`",
				"Old int `migu:\"column:col_b\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD COLUMN `created_at` TIMESTAMP",
				"ALTER TABLE `user` ALTER COLUMN `created_at` TIMESTAMP NOT NULL",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := fmt.Sprintf("package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
					strings.Join(v.columns, "\n") + "\n" +
					"}")
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				actual := results
				expect := v.expect
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Fatalf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
			}) {
				return
			}
		}
	})

	t.Run("ALTER TABLE with multiple tables", func(t *testing.T) {
		defer cleanup(t)
		if err := exec([]string{
			"CREATE TABLE `user` (`age` INT64 NOT NULL, `gender` INT64 NOT NULL) PRIMARY KEY (`age`)",
			"CREATE TABLE `guest` (`age` INT64 NOT NULL, `sex` INT64 NOT NULL) PRIMARY KEY (`age`)",
		}); err != nil {
			t.Fatal(err)
		}
		src := "package migu_test\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Age int\n" +
			"	Gender int\n" +
			"}\n" +
			"//+migu\n" +
			"type Guest struct {\n" +
			"	Age int\n" +
			"	Sex int\n" +
			"}"
		results, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		actual := results
		expect := []string(nil)
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Fatalf("(-got +want)\n%v", diff)
		}
		if err := exec(results); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("embedded field", func(t *testing.T) {
		defer cleanup(t)
		src := fmt.Sprintf("package migu_test\n" +
			"type Timestamp struct {\n" +
			"	CreatedAt time.Time\n" +
			"}\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Age int `migu:\"pk\"`\n" +
			"	Timestamp\n" +
			"}")
		actual, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `age` INT64 NOT NULL\n" +
				") PRIMARY KEY (`age`)",
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("extra tag", func(t *testing.T) {
		defer cleanup(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"CreatedAt time.Time `migu:\"extra:allow_commit_timestamp = true\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `id` INT64 NOT NULL,\n" +
					"  `created_at` TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true)\n" +
					") PRIMARY KEY (`id`)",
			}},
			{2, []string{
				"CreatedAt time.Time `migu:\"extra:allow_commit_timestamp = true\"`",
				"UpdatedAt time.Time `migu:\"extra:allow_commit_timestamp = true\"`",
			}, []string{
				"ALTER TABLE `user` ADD COLUMN `updated_at` TIMESTAMP OPTIONS (allow_commit_timestamp = true)",
				"ALTER TABLE `user` ALTER COLUMN `updated_at` TIMESTAMP NOT NULL",
			}},
			{3, []string{
				"CreatedAt time.Time",
				"UpdatedAt time.Time `migu:\"extra:allow_commit_timestamp = true\"`",
			}, []string{
				"ALTER TABLE `user` ALTER COLUMN `created_at` SET OPTIONS (allow_commit_timestamp = null)",
			}},
			{4, []string{
				"CreatedAt time.Time `migu:\"extra:allow_commit_timestamp = true\"`",
				"UpdatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ALTER COLUMN `created_at` SET OPTIONS (allow_commit_timestamp = true)",
				"ALTER TABLE `user` ALTER COLUMN `updated_at` SET OPTIONS (allow_commit_timestamp = null)",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := "package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
					"ID int64 `migu:\"pk\"`\n" +
					strings.Join(v.columns, "\n") + "\n" +
					"}"
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				actual := results
				expect := v.expect
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Errorf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
			}) {
				return
			}
		}
	})

	t.Run("type tag", func(t *testing.T) {
		t.Run("sequential", func(t *testing.T) {
			defer cleanup(t)
			for _, v := range []struct {
				i       int
				columns []string
				expect  []string
			}{
				{1, []string{
					"Name string `migu:\"type:bytes(MAX)\"`",
				}, []string{
					"CREATE TABLE `user` (\n" +
						"  `id` INT64 NOT NULL,\n" +
						"  `name` BYTES(MAX) NOT NULL\n" +
						") PRIMARY KEY (`id`)",
				}},
				{2, []string{
					"Name string `migu:\"type:string(MAX)\"`",
				}, []string{
					"ALTER TABLE `user` ALTER COLUMN `name` STRING(MAX) NOT NULL",
				}},
				{3, []string{
					"Name []byte",
					"Note []byte `migu:\"type:string(255)\"`",
				}, []string{
					"ALTER TABLE `user` ALTER COLUMN `name` BYTES(MAX) NOT NULL",
					"ALTER TABLE `user` ADD COLUMN `note` STRING(255)",
					"ALTER TABLE `user` ALTER COLUMN `note` STRING(255) NOT NULL",
				}},
				{4, []string{
					"Name []byte",
					"Note string",
				}, []string{
					"ALTER TABLE `user` ALTER COLUMN `note` STRING(MAX) NOT NULL",
				}},
			} {
				v := v
				if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
					src := "package migu_test\n" +
						"//+migu\n" +
						"type User struct {\n" +
						"  ID int64 `migu:\"pk\"`\n" +
						strings.Join(v.columns, "\n") + "\n" +
						"}"
					results, err := migu.Diff(d, "", src)
					if err != nil {
						t.Fatal(err)
					}
					actual := results
					expect := v.expect
					if diff := cmp.Diff(actual, expect); diff != "" {
						t.Fatalf("(-got +want)\n%v", diff)
					}
					if err := exec(results); err != nil {
						t.Fatal(err)
					}
				}) {
					return
				}
			}
		})

		t.Run("all types", func(t *testing.T) {
			for _, v := range []struct {
				name string
			}{
				{"int64"},
				{"float64"},
				{"date"},
				{"timestamp"},
				{"string(max)"},
				{"bytes(max)"},
				{"bool"},
				{"array<int64>"},
				{"array<float64>"},
				{"array<date>"},
				{"array<timestamp>"},
				{"array<string(max)>"},
				{"array<bytes(max)>"},
				{"array<bool>"},
			} {
				v := v
				t.Run(fmt.Sprintf("type:%v", v.name), func(t *testing.T) {
					defer cleanup(t)
					src := "package migu_test\n" +
						"//+migu\n" +
						"type User struct {\n" +
						"  ID int64 `migu:\"pk\"`\n" +
						fmt.Sprintf("	A string `migu:\"type:%s\"`\n", v.name) +
						"}"
					results, err := migu.Diff(d, "", src)
					if err != nil {
						t.Fatal(err)
					}
					if err := exec(results); err != nil {
						t.Fatal(err)
					}
					results, err = migu.Diff(d, "", src)
					if err != nil {
						t.Fatal(err)
					}
					var actual interface{} = results
					var expect interface{} = []string(nil)
					if diff := cmp.Diff(actual, expect); diff != "" {
						t.Errorf("(-got +want)\n%v", diff)
					}
				})
			}
		})
	})

	t.Run("null tag", func(t *testing.T) {
		defer cleanup(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Fee *float64",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `id` INT64 NOT NULL,\n" +
					"  `fee` FLOAT64\n" +
					") PRIMARY KEY (`id`)",
			}},
			{2, []string{
				"Fee *float64 `migu:\"null\"`",
			}, []string(nil)},
			{3, []string{
				"Fee float64 `migu:\"null\"`",
			}, []string(nil)},
			{4, []string{
				"Fee float64",
			}, []string{
				"ALTER TABLE `user` ALTER COLUMN `fee` FLOAT64 NOT NULL",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := "package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
					"  ID int64 `migu:\"pk\"`\n" +
					strings.Join(v.columns, "\n") + "\n" +
					"}"
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				actual := results
				expect := v.expect
				if diff := cmp.Diff(actual, expect); diff != "" {
					t.Fatalf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatal(err)
				}
			}) {
				return
			}
		}
	})

	t.Run("user-defined type", func(t *testing.T) {
		defer cleanup(t)
		src := strings.Join([]string{
			"package migu_test",
			"type UUID struct {}",
			"//+migu",
			"type User struct {",
			"	UUID UUID `migu:\"pk,type:string(36)\"`",
			"}",
		}, "\n")
		results, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		var actual interface{} = results
		var expect interface{} = []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `uuid` STRING(36) NOT NULL",
				") PRIMARY KEY (`uuid`)",
			}, "\n"),
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Fatalf("(-got +want)\n%v", diff)
		}
		if err := exec(results); err != nil {
			t.Fatal(err)
		}
		actual, err = migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect = []string(nil)
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("go types", func(t *testing.T) {
		for t1, t2 := range map[string]string{
			"string":                 "STRING(MAX) NOT NULL",
			"*string":                "STRING(MAX)",
			"[]string":               "ARRAY<STRING(MAX)> NOT NULL",
			"[]*string":              "ARRAY<STRING(MAX)> NOT NULL",
			"*[]string":              "ARRAY<STRING(MAX)>",
			"spanner.NullString":     "STRING(MAX)",
			"[]spanner.NullString":   "ARRAY<STRING(MAX)> NOT NULL",
			"[]*spanner.NullString":  "ARRAY<STRING(MAX)> NOT NULL",
			"*[]spanner.NullString":  "ARRAY<STRING(MAX)>",
			"[]byte":                 "BYTES(MAX) NOT NULL",
			"*[]byte":                "BYTES(MAX)",
			"[][]byte":               "ARRAY<BYTES(MAX)> NOT NULL",
			"*[][]byte":              "ARRAY<BYTES(MAX)>",
			"bool":                   "BOOL NOT NULL",
			"*bool":                  "BOOL",
			"[]bool":                 "ARRAY<BOOL> NOT NULL",
			"[]*bool":                "ARRAY<BOOL> NOT NULL",
			"*[]bool":                "ARRAY<BOOL>",
			"spanner.NullBool":       "BOOL",
			"[]spanner.NullBool":     "ARRAY<BOOL> NOT NULL",
			"*[]spanner.NullBool":    "ARRAY<BOOL>",
			"int":                    "INT64 NOT NULL",
			"*int":                   "INT64",
			"[]int":                  "ARRAY<INT64> NOT NULL",
			"[]*int":                 "ARRAY<INT64> NOT NULL",
			"*[]int":                 "ARRAY<INT64>",
			"int8":                   "INT64 NOT NULL",
			"*int8":                  "INT64",
			"[]int8":                 "ARRAY<INT64> NOT NULL",
			"[]*int8":                "ARRAY<INT64> NOT NULL",
			"*[]int8":                "ARRAY<INT64>",
			"int16":                  "INT64 NOT NULL",
			"*int16":                 "INT64",
			"[]int16":                "ARRAY<INT64> NOT NULL",
			"[]*int16":               "ARRAY<INT64> NOT NULL",
			"*[]int16":               "ARRAY<INT64>",
			"int32":                  "INT64 NOT NULL",
			"*int32":                 "INT64",
			"[]int32":                "ARRAY<INT64> NOT NULL",
			"[]*int32":               "ARRAY<INT64> NOT NULL",
			"*[]int32":               "ARRAY<INT64>",
			"int64":                  "INT64 NOT NULL",
			"*int64":                 "INT64",
			"[]int64":                "ARRAY<INT64> NOT NULL",
			"[]*int64":               "ARRAY<INT64> NOT NULL",
			"*[]int64":               "ARRAY<INT64>",
			"uint8":                  "INT64 NOT NULL",
			"*uint8":                 "INT64",
			"[]uint8":                "ARRAY<INT64> NOT NULL",
			"[]*uint8":               "ARRAY<INT64> NOT NULL",
			"*[]uint8":               "ARRAY<INT64>",
			"uint16":                 "INT64 NOT NULL",
			"*uint16":                "INT64",
			"[]uint16":               "ARRAY<INT64> NOT NULL",
			"[]*uint16":              "ARRAY<INT64> NOT NULL",
			"*[]uint16":              "ARRAY<INT64>",
			"uint32":                 "INT64 NOT NULL",
			"*uint32":                "INT64",
			"[]uint32":               "ARRAY<INT64> NOT NULL",
			"[]*uint32":              "ARRAY<INT64> NOT NULL",
			"*[]uint32":              "ARRAY<INT64>",
			"uint64":                 "INT64 NOT NULL",
			"*uint64":                "INT64",
			"[]uint64":               "ARRAY<INT64> NOT NULL",
			"[]*uint64":              "ARRAY<INT64> NOT NULL",
			"*[]uint64":              "ARRAY<INT64>",
			"spanner.NullInt64":      "INT64",
			"[]spanner.NullInt64":    "ARRAY<INT64> NOT NULL",
			"*[]spanner.NullInt64":   "ARRAY<INT64>",
			"float32":                "FLOAT64 NOT NULL",
			"*float32":               "FLOAT64",
			"[]float32":              "ARRAY<FLOAT64> NOT NULL",
			"[]*float32":             "ARRAY<FLOAT64> NOT NULL",
			"*[]float32":             "ARRAY<FLOAT64>",
			"float64":                "FLOAT64 NOT NULL",
			"*float64":               "FLOAT64",
			"[]float64":              "ARRAY<FLOAT64> NOT NULL",
			"[]*float64":             "ARRAY<FLOAT64> NOT NULL",
			"*[]float64":             "ARRAY<FLOAT64>",
			"spanner.NullFloat64":    "FLOAT64",
			"[]spanner.NullFloat64":  "ARRAY<FLOAT64> NOT NULL",
			"*[]spanner.NullFloat64": "ARRAY<FLOAT64>",
			"time.Time":              "TIMESTAMP NOT NULL",
			"*time.Time":             "TIMESTAMP",
			"[]time.Time":            "ARRAY<TIMESTAMP> NOT NULL",
			"[]*time.Time":           "ARRAY<TIMESTAMP> NOT NULL",
			"*[]time.Time":           "ARRAY<TIMESTAMP>",
			"spanner.NullTime":       "TIMESTAMP",
			"[]spanner.NullTime":     "ARRAY<TIMESTAMP> NOT NULL",
			"*[]spanner.NullTime":    "ARRAY<TIMESTAMP>",
			"civil.Date":             "DATE NOT NULL",
			"*civil.Date":            "DATE",
			"[]civil.Date":           "ARRAY<DATE> NOT NULL",
			"[]*civil.Date":          "ARRAY<DATE> NOT NULL",
			"*[]civil.Date":          "ARRAY<DATE>",
			"spanner.NullDate":       "DATE",
			"[]spanner.NullDate":     "ARRAY<DATE> NOT NULL",
			"*[]spanner.NullDate":    "ARRAY<DATE>",
			// "big.Rat":                "NUMERIC NOT NULL",
			// "*big.Rat":               "NUMERIC",
			// "[]big.Rat":              "ARRAY<NUMERIC> NOT NULL",
			// "[]*big.Rat":             "ARRAY<NUMERIC> NOT NULL",
			// "*[]big.Rat":             "ARRAY<NUMERIC>",
			// "spanner.NullNumeric":    "NUMERIC",
			// "[]spanner.NullNumeric":  "ARRAY<NUMERIC> NOT NULL",
			// "*[]spanner.NullNumeric": "ARRAY<NUMERIC>",
		} {
			t.Run(fmt.Sprintf("%v is converted to %v", t1, t2), func(t *testing.T) {
				defer cleanup(t)
				src := fmt.Sprintf("package migu_test\n"+
					"//+migu\n"+
					"type User struct {\n"+
					"	ID int64 `migu:\"pk\"`\n"+
					"	A %s\n"+
					"}", t1)
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatalf("%+v\n", err)
				}
				var got interface{} = results
				var want interface{} = []string{
					fmt.Sprintf("CREATE TABLE `user` (\n"+
						"  `id` INT64 NOT NULL,\n"+
						"  `a` %s\n"+
						") PRIMARY KEY (`id`)", t2),
				}
				if diff := cmp.Diff(got, want); diff != "" {
					t.Errorf("(-got +want)\n%v", diff)
				}
				if err := exec(results); err != nil {
					t.Fatalf("%+v\n", err)
				}
			})
		}
	})

	t.Run("custom column type", func(t *testing.T) {
		defer cleanup(t)
		d := dialect.NewSpanner(dsn, dialect.WithColumnType([]*dialect.ColumnType{
			{
				Types:           []string{"STRING(MAX)"},
				GoTypes:         []string{"UUID"},
				GoNullableTypes: []string{"NullUUID"},
			},
			{
				Types:           []string{"STRING(256)"},
				GoTypes:         []string{"string"},
				GoNullableTypes: []string{"*string", "sql.NullString"},
			},
			{
				Types:   []string{"INT64"},
				GoTypes: []string{"Status"},
			},
			{
				Types:   []string{"FLOAT64"},
				GoTypes: []string{"int16"},
			},
		}))
		got, err := migu.Diff(d, "", strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	ID UUID `migu:\"pk\"`",
			"	Name string",
			"	Nickname sql.NullString",
			"	Status Status",
			"	Child NullUUID",
			"	Amount int",
			"	Views int16",
			"}",
		}, "\n"))
		if err != nil {
			t.Fatalf("%+v\n", err)
		}
		want := []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `id` STRING(MAX) NOT NULL,",
				"  `name` STRING(256) NOT NULL,",
				"  `nickname` STRING(256),",
				"  `status` INT64 NOT NULL,",
				"  `child` STRING(MAX),",
				"  `amount` INT64 NOT NULL,",
				"  `views` FLOAT64 NOT NULL",
				") PRIMARY KEY (`id`)",
			}, "\n"),
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})
}

func TestFprint(t *testing.T) {
	d := dialect.NewSpanner(dsn)
	for _, v := range []struct {
		i    int
		sqls []string
		want string
	}{
		{1, []string{
			"CREATE TABLE user (" +
				strings.Join([]string{
					"id INT64 NOT NULL",
					"s1 STRING(MAX)",
					"s2 STRING(MAX) NOT NULL",
					"s3 STRING(255) NOT NULL",
					"b1 BYTES(MAX)",
					"b2 BYTES(MAX) NOT NULL",
					"b3 BYTES(128)",
					"bo1 BOOL",
					"bo2 BOOL NOT NULL",
					"i1 INT64",
					"i2 INT64 NOT NULL",
					"f1 FLOAT64",
					"f2 FLOAT64 NOT NULL",
					"sa1 ARRAY<STRING(MAX)>",
					"sa2 ARRAY<STRING(MAX)> NOT NULL",
					"sa3 ARRAY<STRING(255)> NOT NULL",
					"ba1 ARRAY<BYTES(MAX)>",
					"ba2 ARRAY<BYTES(MAX)> NOT NULL",
					"ba3 ARRAY<BYTES(128)> NOT NULL",
					"boa1 ARRAY<BOOL>",
					"boa2 ARRAY<BOOL> NOT NULL",
					"ia1 ARRAY<INT64>",
					"ia2 ARRAY<INT64> NOT NULL",
					"fa1 ARRAY<FLOAT64>",
					"fa2 ARRAY<FLOAT64> NOT NULL",
				}, ",\n") + "\n" +
				") PRIMARY KEY (id)",
		}, "//+migu\n" +
			"type User struct {\n" +
			strings.Join([]string{
				"	ID   int64     `migu:\"type:INT64,pk\"`",
				"	S1   *string   `migu:\"type:STRING(MAX),null\"`",
				"	S2   string    `migu:\"type:STRING(MAX)\"`",
				"	S3   string    `migu:\"type:STRING(255)\"`",
				"	B1   []byte    `migu:\"type:BYTES(MAX),null\"`",
				"	B2   []byte    `migu:\"type:BYTES(MAX)\"`",
				"	B3   []byte    `migu:\"type:BYTES(128),null\"`",
				"	Bo1  *bool     `migu:\"type:BOOL,null\"`",
				"	Bo2  bool      `migu:\"type:BOOL\"`",
				"	I1   *int64    `migu:\"type:INT64,null\"`",
				"	I2   int64     `migu:\"type:INT64\"`",
				"	F1   *float64  `migu:\"type:FLOAT64,null\"`",
				"	F2   float64   `migu:\"type:FLOAT64\"`",
				"	Sa1  []string  `migu:\"type:ARRAY<STRING(MAX)>,null\"`",
				"	Sa2  []string  `migu:\"type:ARRAY<STRING(MAX)>\"`",
				"	Sa3  []string  `migu:\"type:ARRAY<STRING(255)>\"`",
				"	Ba1  [][]byte  `migu:\"type:ARRAY<BYTES(MAX)>,null\"`",
				"	Ba2  [][]byte  `migu:\"type:ARRAY<BYTES(MAX)>\"`",
				"	Ba3  [][]byte  `migu:\"type:ARRAY<BYTES(128)>\"`",
				"	Boa1 []bool    `migu:\"type:ARRAY<BOOL>,null\"`",
				"	Boa2 []bool    `migu:\"type:ARRAY<BOOL>\"`",
				"	Ia1  []int64   `migu:\"type:ARRAY<INT64>,null\"`",
				"	Ia2  []int64   `migu:\"type:ARRAY<INT64>\"`",
				"	Fa1  []float64 `migu:\"type:ARRAY<FLOAT64>,null\"`",
				"	Fa2  []float64 `migu:\"type:ARRAY<FLOAT64>\"`",
			}, "\n") + "\n" +
			"}\n\n",
		},
		{2, []string{
			"CREATE TABLE user (" +
				strings.Join([]string{
					"id INT64 NOT NULL",
					"t1 TIMESTAMP",
					"t2 TIMESTAMP NOT NULL",
					"ta1 ARRAY<TIMESTAMP>",
					"ta2 ARRAY<TIMESTAMP> NOT NULL",
				}, ",\n") + "\n" +
				") PRIMARY KEY (id)",
		}, `import "time"` + "\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			strings.Join([]string{
				"	ID  int64       `migu:\"type:INT64,pk\"`",
				"	T1  *time.Time  `migu:\"type:TIMESTAMP,null\"`",
				"	T2  time.Time   `migu:\"type:TIMESTAMP\"`",
				"	Ta1 []time.Time `migu:\"type:ARRAY<TIMESTAMP>,null\"`",
				"	Ta2 []time.Time `migu:\"type:ARRAY<TIMESTAMP>\"`",
			}, "\n") + "\n" +
			"}\n\n",
		},
		{3, []string{
			"CREATE TABLE user (" +
				strings.Join([]string{
					"id INT64 NOT NULL",
					"d1 DATE",
					"d2 DATE NOT NULL",
					"da1 ARRAY<DATE>",
					"da2 ARRAY<DATE> NOT NULL",
				}, ",\n") + "\n" +
				") PRIMARY KEY (id)",
		}, `import "cloud.google.com/go/civil"` + "\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			strings.Join([]string{
				"	ID  int64        `migu:\"type:INT64,pk\"`",
				"	D1  *civil.Date  `migu:\"type:DATE,null\"`",
				"	D2  civil.Date   `migu:\"type:DATE\"`",
				"	Da1 []civil.Date `migu:\"type:ARRAY<DATE>,null\"`",
				"	Da2 []civil.Date `migu:\"type:ARRAY<DATE>\"`",
			}, "\n") + "\n" +
			"}\n\n",
		},
		{4, []string{
			"CREATE TABLE user (" +
				strings.Join([]string{
					"id INT64 NOT NULL",
					"t1 TIMESTAMP NOT NULL",
					"d1 DATE NOT NULL",
				}, ",\n") + "\n" +
				") PRIMARY KEY (id)",
		}, "import (\n" +
			`	"cloud.google.com/go/civil"` + "\n" +
			`	"time"` + "\n" +
			")\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			strings.Join([]string{
				"	ID int64      `migu:\"type:INT64,pk\"`",
				"	T1 time.Time  `migu:\"type:TIMESTAMP\"`",
				"	D1 civil.Date `migu:\"type:DATE\"`",
			}, "\n") + "\n" +
			"}\n\n",
		},
	} {
		t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
			if err := exec(v.sqls); err != nil {
				t.Fatalf("%+v\n", err)
			}
			defer cleanup(t)
			var buf bytes.Buffer
			if err := migu.Fprint(&buf, d); err != nil {
				t.Fatalf("%+v\n", err)
			}
			got := buf.String()
			want := v.want
			if diff := cmp.Diff(got, want); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}
}
