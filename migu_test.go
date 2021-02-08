//+build !spanner

package migu_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/go-cmp/cmp"
	"github.com/naoina/migu"
	"github.com/naoina/migu/dialect"
)

var db *sql.DB

func TestMain(m *testing.M) {
	dbHost := os.Getenv("DB_HOST")
	if dbHost == "" {
		dbHost = "localhost"
	}
	var err error
	if db, err = sql.Open("mysql", fmt.Sprintf("root@tcp(%s)/migu_test", dbHost)); err != nil {
		panic(err)
	}
	os.Exit(func() int {
		defer db.Close()
		return m.Run()
	}())
}

func exec(queries []string) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	for _, query := range queries {
		if _, err := tx.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func before(t *testing.T) {
	t.Helper()
	if err := exec([]string{
		`DROP TABLE IF EXISTS user`,
		"DROP TABLE IF EXISTS guest",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDiff(t *testing.T) {
	d := dialect.NewMySQL(db)
	t.Run("idempotency", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			column string
		}{
			{"Name string"},
			{"Name string `migu:\"type:varchar(255)\"`"},
		} {
			v := v
			t.Run(fmt.Sprintf("%v", v.column), func(t *testing.T) {
				src := fmt.Sprintf("package migu_test\n"+
					"//+migu\n"+
					"type User struct {\n"+
					"	%s\n"+
					"}", v.column)
				results, err := migu.Diff(d, "", src)
				if err != nil {
					t.Fatal(err)
				}
				defer exec([]string{"DROP TABLE `user`"})
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
		before(t)
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
				"  `id` BIGINT UNSIGNED NOT NULL,",
				"  PRIMARY KEY (`id`)",
				")",
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

	t.Run("multiple-column primary key", func(t *testing.T) {
		before(t)
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
				"  `user_id` BIGINT UNSIGNED NOT NULL,",
				"  `profile_id` BIGINT UNSIGNED NOT NULL,",
				"  PRIMARY KEY (`user_id`, `profile_id`)",
				")",
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
		before(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL,\n" +
					"  `created_at` DATETIME NOT NULL\n" +
					")",
				"CREATE INDEX `user_age` ON `user` (`age`)",
			}},
			{2, []string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"CREATE INDEX `user_created_at` ON `user` (`created_at`)",
			}},
			{3, []string{
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"DROP INDEX `user_age` ON `user`",
				"CREATE INDEX `age_index` ON `user` (`age`)",
			}},
			{4, []string{
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_created_at` ON `user`",
			}},
			{5, []string{
				"Age int `migu:\"index:age_created_at_index\"`",
				"CreatedAt time.Time `migu:\"index:age_created_at_index\"`",
			}, []string{
				"DROP INDEX `age_index` ON `user`",
				"CREATE INDEX `age_created_at_index` ON `user` (`age`,`created_at`)",
			}},
			{6, []string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_created_at_index` ON `user`",
			}},
			{7, []string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
			}},
			{8, []string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"CREATE UNIQUE INDEX `user_created_at` ON `user` (`created_at`)",
			}},
			{9, []string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"DROP INDEX `user_age` ON `user`",
				"CREATE INDEX `user_age` ON `user` (`age`)",
			}},
			{10, []string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_age` ON `user`",
				"DROP INDEX `user_created_at` ON `user`",
				"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
			}},
			{11, []string{
				"Age int `migu:\"unique:age_unique_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `user_age` ON `user`",
				"CREATE UNIQUE INDEX `age_unique_index` ON `user` (`age`)",
			}},
			{12, []string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_unique_index` ON `user`",
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
		before(t)
		src := fmt.Sprintf("package migu_test\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Age int `migu:\"unique\"`\n" +
			"}")
		actual, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `age` INT NOT NULL\n" +
				")",
			"CREATE UNIQUE INDEX `user_age` ON `user` (`age`)",
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("multiple unique indexes", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Age int `migu:\"unique:age_created_at_unique_index\"`",
				"CreatedAt time.Time `migu:\"unique:age_created_at_unique_index\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL,\n" +
					"  `created_at` DATETIME NOT NULL\n" +
					")",
				"CREATE UNIQUE INDEX `age_created_at_unique_index` ON `user` (`age`,`created_at`)",
			}},
			{2, []string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique:created_at_unique_index\"`",
			}, []string{
				"DROP INDEX `age_created_at_unique_index` ON `user`",
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
		before(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Age int",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL\n" +
					")",
			}},
			{2, []string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `created_at` DATETIME NOT NULL",
			}},
			{3, []string{
				"Age uint8 `migu:\"column:col_a\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` CHANGE `age` `col_a` TINYINT UNSIGNED NOT NULL",
			}},
			{4, []string{
				"Age uint8 `migu:\"column:col_b\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `col_b` TINYINT UNSIGNED NOT NULL",
				"ALTER TABLE `user` DROP `col_a`",
			}},
			{5, []string{
				"Age uint8",
				"Old uint8 `migu:\"column:col_b\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `age` TINYINT UNSIGNED NOT NULL",
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
		before(t)
		if err := exec([]string{
			"CREATE TABLE `user` (`age` INT NOT NULL, `gender` INT NOT NULL)",
			"CREATE TABLE `guest` (`age` INT NOT NULL, `sex` INT NOT NULL)",
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
		before(t)
		src := fmt.Sprintf("package migu_test\n" +
			"type Timestamp struct {\n" +
			"	CreatedAt time.Time\n" +
			"}\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Age int\n" +
			"	Timestamp\n" +
			"}")
		actual, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `age` INT NOT NULL\n" +
				")",
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})

	t.Run("extra tag", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `created_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP\n" +
					")",
			}},
			{2, []string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
				"UpdatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"ALTER TABLE `user` ADD `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP",
			}},
			{3, []string{
				"CreatedAt time.Time",
				"UpdatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `created_at` `created_at` DATETIME NOT NULL",
			}},
			{4, []string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
				"UpdatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` CHANGE `created_at` `created_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP",
				"ALTER TABLE `user` CHANGE `updated_at` `updated_at` DATETIME NOT NULL",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := "package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
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
			before(t)
			for _, v := range []struct {
				i       int
				columns []string
				expect  []string
			}{
				{1, []string{
					"Fee float64 `migu:\"type:tinyint\"`",
				}, []string{
					"CREATE TABLE `user` (\n" +
						"  `fee` TINYINT NOT NULL\n" +
						")",
				}},
				{2, []string{
					"Fee float64 `migu:\"type:int\"`",
				}, []string{
					"ALTER TABLE `user` CHANGE `fee` `fee` INT NOT NULL",
				}},
				{3, []string{
					"Fee float64",
					"Point int `migu:\"type:smallint\"`",
				}, []string{
					"ALTER TABLE `user` CHANGE `fee` `fee` DOUBLE NOT NULL",
					"ALTER TABLE `user` ADD `point` SMALLINT NOT NULL",
				}},
				{4, []string{
					"Fee float64",
					"Point int     `migu:\"type:smallint\"`",
					"Verified bool `migu:\"type:tinyint(1)\"`",
				}, []string{
					"ALTER TABLE `user` ADD `verified` TINYINT(1) NOT NULL",
				}},
			} {
				v := v
				if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
					src := "package migu_test\n" +
						"//+migu\n" +
						"type User struct {\n" +
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
				{"int"},
				{"tinyint"},
				{"smallint"},
				{"mediumint"},
				{"bigint"},
				{"decimal"},
				{"double"},
				{"float"},
				{"date"},
				{"datetime"},
				// {"timestamp"},
				{"time"},
				{"year"},
				{"char"},
				{"varchar"},
				{"binary"},
				{"varbinary"},
				{"tinyblob"},
				{"tinytext"},
				{"blob"},
				{"text"},
				{"mediumblob"},
				{"mediumtext"},
				{"longblob"},
				{"longtext"},
			} {
				v := v
				t.Run(fmt.Sprintf("type:%v", v.name), func(t *testing.T) {
					before(t)
					src := "package migu_test\n" +
						"//+migu\n" +
						"type User struct {\n" +
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
		before(t)
		for _, v := range []struct {
			i       int
			columns []string
			expect  []string
		}{
			{1, []string{
				"Fee *float64",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `fee` DOUBLE\n" +
					")",
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
				"ALTER TABLE `user` CHANGE `fee` `fee` DOUBLE NOT NULL",
			}},
		} {
			v := v
			if !t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
				src := "package migu_test\n" +
					"//+migu\n" +
					"type User struct {\n" +
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

	t.Run("column with comment", func(t *testing.T) {
		before(t)
		src := strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	UUID string `migu:\"type:varchar(36)\"` // Maximum length is 36",
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
				"  `uuid` VARCHAR(36) NOT NULL COMMENT 'Maximum length is 36'",
				")",
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

	t.Run("user-defined type", func(t *testing.T) {
		before(t)
		src := strings.Join([]string{
			"package migu_test",
			"type UUID struct {}",
			"//+migu",
			"type User struct {",
			"	UUID UUID `migu:\"type:varbinary(36)\"`",
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
				"  `uuid` VARBINARY(36) NOT NULL",
				")",
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

	t.Run("custom column type", func(t *testing.T) {
		d := dialect.NewMySQL(db, dialect.WithColumnType([]*dialect.ColumnType{
			{
				Types:           []string{"VARCHAR"},
				GoTypes:         []string{"UUID"},
				GoNullableTypes: []string{"NullUUID"},
			},
			{
				Types:           []string{"TEXT"},
				GoTypes:         []string{"string"},
				GoNullableTypes: []string{"*string", "sql.NullString"},
			},
			{
				Types:   []string{"TINYINT"},
				GoTypes: []string{"Status"},
			},
			{
				Types:   []string{"BIGINT"},
				GoTypes: []string{"int"},
			},
		}))
		before(t)
		got, err := migu.Diff(d, "", strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	ID UUID",
			"	Name string",
			"	Nickname sql.NullString",
			"	Status Status",
			"	Child NullUUID",
			"	Amount int",
			"	Views int64",
			"}",
		}, "\n"))
		if err != nil {
			t.Fatalf("%+v\n", err)
		}
		want := []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `id` VARCHAR(255) NOT NULL,",
				"  `name` TEXT NOT NULL,",
				"  `nickname` TEXT,",
				"  `status` TINYINT NOT NULL,",
				"  `child` VARCHAR(255),",
				"  `amount` BIGINT NOT NULL,",
				"  `views` BIGINT NOT NULL",
				")",
			}, "\n"),
		}
		if diff := cmp.Diff(got, want); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})
}

func TestDiffWithSrc(t *testing.T) {
	before(t)
	types := map[string]string{
		"int":             "INT NOT NULL",
		"int8":            "TINYINT NOT NULL",
		"int16":           "SMALLINT NOT NULL",
		"int32":           "INT NOT NULL",
		"int64":           "BIGINT NOT NULL",
		"*int":            "INT",
		"*int8":           "TINYINT",
		"*int16":          "SMALLINT",
		"*int32":          "INT",
		"*int64":          "BIGINT",
		"uint":            "INT UNSIGNED NOT NULL",
		"uint8":           "TINYINT UNSIGNED NOT NULL",
		"uint16":          "SMALLINT UNSIGNED NOT NULL",
		"uint32":          "INT UNSIGNED NOT NULL",
		"uint64":          "BIGINT UNSIGNED NOT NULL",
		"*uint":           "INT UNSIGNED",
		"*uint8":          "TINYINT UNSIGNED",
		"*uint16":         "SMALLINT UNSIGNED",
		"*uint32":         "INT UNSIGNED",
		"*uint64":         "BIGINT UNSIGNED",
		"sql.NullInt64":   "BIGINT",
		"string":          "VARCHAR(255) NOT NULL",
		"*string":         "VARCHAR(255)",
		"[]byte":          "VARBINARY(255) NOT NULL",
		"sql.NullString":  "VARCHAR(255)",
		"bool":            "TINYINT(1) NOT NULL",
		"*bool":           "TINYINT(1)",
		"sql.NullBool":    "TINYINT(1)",
		"float32":         "DOUBLE NOT NULL",
		"float64":         "DOUBLE NOT NULL",
		"*float32":        "DOUBLE",
		"*float64":        "DOUBLE",
		"sql.NullFloat64": "DOUBLE",
		"time.Time":       "DATETIME NOT NULL",
		"*time.Time":      "DATETIME",
	}
	for t1, s1 := range types {
		for t2, s2 := range types {
			t1 := t1
			s1 := s1
			t2 := t2
			s2 := s2
			t.Run(fmt.Sprintf("from %v to %v", t1, t2), func(t *testing.T) {
				testDiffWithSrc(t, t1, s1, t2, s2)
			})
		}
	}
}

func testDiffWithSrc(t *testing.T, t1, s1, t2, s2 string) {
	d := dialect.NewMySQL(db)
	src := fmt.Sprintf("package migu_test\n"+
		"//+migu\n"+
		"type User struct {\n"+
		"	A %s\n"+
		"}", t1)
	results, err := migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual := results
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n"+
			"  `a` %s\n"+
			")", s1),
	}
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Fatalf("(-got +want)\n%v", diff)
	}
	if err := exec(actual); err != nil {
		t.Fatal(err)
	}
	defer exec([]string{"DROP TABLE IF EXISTS `user`"})

	results, err = migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string(nil)
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Fatalf("(-got +want)\n%v", diff)
	}

	src = fmt.Sprintf("package migu_test\n"+
		"//+migu\n"+
		"type User struct {\n"+
		"	A %s\n"+
		"}", t2)
	results, err = migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	if s1 == s2 {
		expect = []string(nil)
	} else {
		expect = []string{"ALTER TABLE `user` CHANGE `a` `a` " + s2}
	}
	sort.Strings(actual)
	sort.Strings(expect)
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Fatalf("(-got +want)\n%v", diff)
	}
	if err := exec(actual); err != nil {
		t.Fatal(err)
	}

	src = "package migu_test"
	results, err = migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string{"DROP TABLE `user`"}
	sort.Strings(actual)
	sort.Strings(expect)
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Fatalf("(-got +want)\n%v", diff)
	}
	if err := exec(actual); err != nil {
		t.Fatal(err)
	}
}

func TestDiffWithColumn(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	src := fmt.Sprintf("package migu_test\n" +
		"//+migu\n" +
		"type User struct {\n" +
		"	ThisIsColumn string `migu:\"column:aColumn\"`" +
		"}")
	actual, err := migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n" +
			"  `aColumn` VARCHAR(255) NOT NULL\n" +
			")"),
	}
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Errorf("(-got +want)\n%v", diff)
	}
}

func TestDiffWithExtraField(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	src := fmt.Sprintf("package migu_test\n" +
		"//+migu\n" +
		"type User struct {\n" +
		"	a int\n" +
		"	_ int `migu:\"column:extra\"`\n" +
		"	_ int `migu:\"column:another_extra\"`\n" +
		"	_ int `migu:\"default:yes\"`\n" +
		"}")
	actual, err := migu.Diff(d, "", src)
	if err != nil {
		t.Fatal(err)
	}
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n" +
			"  `extra` INT NOT NULL,\n" +
			"  `another_extra` INT NOT NULL\n" +
			")"),
	}
	if diff := cmp.Diff(actual, expect); diff != "" {
		t.Errorf("(-got +want)\n%v", diff)
	}
}

func TestDiffMarker(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	for _, v := range []struct {
		comment string
	}{
		{"//+migu"},
		{"// +migu"},
		{"// +migu "},
		{"//+migu\n//hoge"},
		{"// +migu \n //hoge"},
		{"//hoge\n//+migu"},
		{"//hoge\n// +migu"},
		{"//foo\n//+migu\n//bar"},
	} {
		v := v
		t.Run(fmt.Sprintf("valid marker(%#v)", v.comment), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
			actual, err := migu.Diff(d, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("CREATE TABLE `user` (\n" +
					"  `a` INT NOT NULL\n" +
					")"),
			}
			if diff := cmp.Diff(actual, expect); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}

	for _, v := range []struct {
		comment string
	}{
		{"//migu"},
		{"//a+migu"},
		{"/*+migu*/"},
		{"/* +migu*/"},
		{"/* +migu */"},
		{"/*\n+migu\n*/"},
	} {
		v := v
		t.Run(fmt.Sprintf("invalid marker(%#v)", v.comment), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
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

	t.Run("multiple struct", func(t *testing.T) {
		src := fmt.Sprintf("package migu_test\n" +
			"type Timestamp struct {\n" +
			"	T time.Time\n" +
			"}\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	A int\n" +
			"}")
		actual, err := migu.Diff(d, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			fmt.Sprintf("CREATE TABLE `user` (\n" +
				"  `a` INT NOT NULL\n" +
				")"),
		}
		if diff := cmp.Diff(actual, expect); diff != "" {
			t.Errorf("(-got +want)\n%v", diff)
		}
	})
}

func TestDiffAnnotation(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	for _, v := range []struct {
		i         int
		comment   string
		tableName string
		option    string
	}{
		{1, `//+migu table:guest`, "guest", ""},
		{2, `//+migu table:"guest table"`, "guest table", ""},
		{3, `//+migu table:GuestTable`, "GuestTable", ""},
		{4, `//+migu table:guest\ntable`, `guest\ntable`, ""},
		{5, `//+migu table:"\"guest\""`, `"guest"`, ""},
		{6, `//+migu table:"hoge\"guest\""`, `hoge"guest"`, ""},
		{7, `//+migu table:"\"guest\"hoge"`, `"guest"hoge`, ""},
		{8, `//+migu table:"\"\"guest\""`, `""guest"`, ""},
		{9, `//+migu table:"\"\"guest\"\""`, `""guest""`, ""},
		{10, `//+migu table:"a\nb"`, "a\nb", ""},
		{11, `//+migu table:a"`, `a"`, ""},
		{12, `//+migu table:a""`, `a""`, ""},
		{13, `//+migu option:ENGINE=InnoDB`, "user", " ENGINE=InnoDB"},
		{14, `//+migu option:"ROW_FORMAT = DYNAMIC"`, "user", " ROW_FORMAT = DYNAMIC"},
		{15, `//+migu table:"guest" option:"ROW_FORMAT = DYNAMIC"`, "guest", " ROW_FORMAT = DYNAMIC"},
		{16, `//+migu option:"ROW_FORMAT = DYNAMIC" table:"guest"`, "guest", " ROW_FORMAT = DYNAMIC"},
	} {
		v := v
		t.Run(fmt.Sprintf("valid annotation/%v", v.i), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
			actual, err := migu.Diff(d, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("CREATE TABLE `" + v.tableName + "` (\n" +
					"  `a` INT NOT NULL\n" +
					")" + v.option),
			}
			if diff := cmp.Diff(actual, expect); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}

	for _, v := range []struct {
		i       int
		comment string
		expect  string
	}{
		{1, "//+migu a", "migu: invalid annotation: //+migu a"},
		{2, "// +migu a", "migu: invalid annotation: // +migu a"},
		{3, "// +migu a ", "migu: invalid annotation: // +migu a "},
		{4, `//+migu table:"a" a`, `migu: invalid annotation: //+migu table:"a" a`},
		{5, `//+migu table:"a"a`, `migu: invalid annotation: //+migu table:"a"a`},
		{6, `//+migu table:"a":a`, `migu: invalid annotation: //+migu table:"a":a`},
		{7, `//+migu table:"a" :a`, `migu: invalid annotation: //+migu table:"a" :a`},
		{8, `//+migu table:"a" a:`, `migu: invalid annotation: //+migu table:"a" a:`},
		{9, `//+migu table:"a`, `migu: invalid annotation: string not terminated: //+migu table:"a`},
		{10, `//+migu table: "a"`, `migu: invalid annotation: value not given: //+migu table: "a"`},
	} {
		v := v
		t.Run(fmt.Sprintf("invalid annotation/%v", v.i), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
			_, err := migu.Diff(d, "", src)
			actual := fmt.Sprint(err)
			expect := v.expect
			if diff := cmp.Diff(actual, expect); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}
}

func TestDiffDropTable(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	for _, v := range []struct {
		table string
	}{
		{"userHoge"},
	} {
		v := v
		t.Run(fmt.Sprintf("DROP TABLE %#v", v.table), func(t *testing.T) {
			if err := exec([]string{`CREATE TABLE ` + v.table + `(id int)`}); err != nil {
				t.Fatal(err)
			}
			defer exec([]string{`DROP TABLE ` + v.table})
			src := "package migu_test\n"
			actual, err := migu.Diff(d, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("DROP TABLE `" + v.table + "`"),
			}
			if diff := cmp.Diff(actual, expect); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}
}

func TestFprint(t *testing.T) {
	d := dialect.NewMySQL(db)
	before(t)
	for _, v := range []struct {
		i      int
		sqls   []string
		expect string
	}{
		{1, []string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255)\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Name *string `migu:\"type:varchar(255),null\"`\n" +
			"}\n\n",
		},
		{2, []string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255),\n" +
				"  age  INT\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Name *string `migu:\"type:varchar(255),null\"`\n" +
			"	Age  *int    `migu:\"type:int,null\"`\n" +
			"}\n\n",
		},
		{3, []string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255)\n" +
				")",
			"CREATE TABLE post (\n" +
				"  title   VARCHAR(255),\n" +
				"  content VARCHAR(255)\n" +
				")",
		}, "//+migu\n" +
			"type Post struct {\n" +
			"	Title   *string `migu:\"type:varchar(255),null\"`\n" +
			"	Content *string `migu:\"type:varchar(255),null\"`\n" +
			"}\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Name *string `migu:\"type:varchar(255),null\"`\n" +
			"}\n\n",
		},
		{4, []string{
			"CREATE TABLE user (\n" +
				"  encrypted_name VARBINARY(255)\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	EncryptedName []byte `migu:\"type:varbinary(255),null\"`\n" +
			"}\n\n",
		},
		{5, []string{
			"CREATE TABLE user (\n" +
				"  encrypted_name BINARY(4)\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	EncryptedName []byte `migu:\"type:binary(4),null\"`\n" +
			"}\n\n",
		},
		{6, []string{
			"CREATE TABLE user (\n" +
				"  Active BOOL NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Active bool `migu:\"type:tinyint(1)\"`\n" +
			"}\n\n",
		},
		{7, []string{
			"CREATE TABLE user (\n" +
				"  Active BOOL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Active *bool `migu:\"type:tinyint(1),null\"`\n" +
			"}\n\n",
		},
		{8, []string{
			"CREATE TABLE user (\n" +
				"  created_at DATETIME NOT NULL\n" +
				")",
		}, "import \"time\"\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	CreatedAt time.Time `migu:\"type:datetime\"`\n" +
			"}\n\n",
		},
		{9, []string{
			"CREATE TABLE user (\n" +
				"  uuid CHAR(36) NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	UUID string `migu:\"type:char(36)\"`\n" +
			"}\n\n",
		},
		{10, []string{
			"CREATE TABLE user (\n" +
				"balance DECIMAL(65,2) NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Balance float64 `migu:\"type:decimal(65,2)\"`\n" +
			"}\n\n",
		},
		{11, []string{
			"CREATE TABLE user (\n" +
				"brightness FLOAT NOT NULL DEFAULT '0.1'\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Brightness float64 `migu:\"type:float,default:0.1\"`\n" +
			"}\n\n",
		},
		{12, []string{
			"CREATE TABLE user (\n" +
				"uuid VARCHAR(36) NOT NULL COMMENT 'Maximum length is 36'\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	UUID string `migu:\"type:varchar(36)\"` // Maximum length is 36\n" +
			"}\n\n",
		},
		{13, []string{
			"CREATE TABLE user (\n" +
				"id BIGINT UNSIGNED NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	ID uint64 `migu:\"type:bigint unsigned\"`\n" +
			"}\n\n",
		},
		{14, []string{
			"CREATE TABLE user (\n" +
				"  created_at DATETIME NOT NULL,\n" +
				"  updated_at DATETIME NOT NULL\n" +
				")",
		}, `import "time"` + "\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	CreatedAt time.Time `migu:\"type:datetime\"`\n" +
			"	UpdatedAt time.Time `migu:\"type:datetime\"`\n" +
			"}\n\n",
		},
	} {
		v := v
		t.Run(fmt.Sprintf("%v", v.i), func(t *testing.T) {
			if err := exec(v.sqls); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := exec([]string{
					`DROP TABLE IF EXISTS user`,
					`DROP TABLE IF EXISTS post`,
				}); err != nil {
					t.Fatal(err)
				}
			}()
			var buf bytes.Buffer
			if err := migu.Fprint(&buf, d); err != nil {
				t.Fatal(err)
			}
			actual := buf.String()
			expect := v.expect
			if diff := cmp.Diff(actual, expect); diff != "" {
				t.Errorf("(-got +want)\n%v", diff)
			}
		})
	}
}
