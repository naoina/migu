package migu_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/naoina/migu"
)

var db *sql.DB

func init() {
	var err error
	db, err = sql.Open("mysql", "travis@/migu_test")
	if err != nil {
		panic(err)
	}
}

func before(t *testing.T) {
	if _, err := db.Exec(`DROP TABLE IF EXISTS user`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DROP TABLE IF EXISTS guest"); err != nil {
		t.Fatal(err)
	}
}

func TestDiff(t *testing.T) {
	t.Run("idempotency", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			column string
		}{
			{"Name string"},
			{"Name string `migu:\"size:255\"`"},
		} {
			v := v
			t.Run(fmt.Sprintf("%v", v.column), func(t *testing.T) {
				src := fmt.Sprintf("package migu_test\n"+
					"//+migu\n"+
					"type User struct {\n"+
					"	%s\n"+
					"}", v.column)
				results, err := migu.Diff(db, "", src)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Exec("DROP TABLE `user`")
				if results == nil {
					t.Fatalf("results must be not nil; got %#v", results)
				}
				for _, q := range results {
					if _, err := db.Exec(q); err != nil {
						t.Fatal(err)
					}
				}
				actual, err := migu.Diff(db, "", src)
				if err != nil {
					t.Fatal(err)
				}
				expect := []string(nil)
				if !reflect.DeepEqual(actual, expect) {
					t.Errorf(`2. migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
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
		results, err := migu.Diff(db, "", src)
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
		if !reflect.DeepEqual(actual, expect) {
			t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
		for _, query := range results {
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
		actual, err = migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect = []string(nil)
		if !reflect.DeepEqual(actual, expect) {
			t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
	})

	t.Run("multiple-column primary key and comment", func(t *testing.T) {
		before(t)
		src := strings.Join([]string{
			"package migu_test",
			"//+migu",
			"type User struct {",
			"	UserID uint64 `migu:\"pk\"`",
			"	ProfileID uint64 `migu:\"pk\"` //max 10 digits",
			"}",
		}, "\n")
		results, err := migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		var actual interface{} = results
		var expect interface{} = []string{
			strings.Join([]string{
				"CREATE TABLE `user` (",
				"  `user_id` BIGINT UNSIGNED NOT NULL,",
				"  `profile_id` BIGINT UNSIGNED NOT NULL COMMENT 'max 10 digits',",
				"  PRIMARY KEY (`user_id`, `profile_id`)",
				")",
			}, "\n"),
		}
		if !reflect.DeepEqual(actual, expect) {
			t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
		for _, query := range results {
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}
		actual, err = migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect = []string(nil)
		if !reflect.DeepEqual(actual, expect) {
			t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
	})

	t.Run("index", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL,\n" +
					"  `created_at` DATETIME NOT NULL\n" +
					")",
				"CREATE INDEX `age` ON `user` (`age`)",
			}},
			{[]string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"CREATE INDEX `created_at` ON `user` (`created_at`)",
			}},
			{[]string{
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time `migu:\"index\"`",
			}, []string{
				"DROP INDEX `age` ON `user`",
				"CREATE INDEX `age_index` ON `user` (`age`)",
			}},
			{[]string{
				"Age int `migu:\"index:age_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `created_at` ON `user`",
			}},
			{[]string{
				"Age int `migu:\"index:age_created_at_index\"`",
				"CreatedAt time.Time `migu:\"index:age_created_at_index\"`",
			}, []string{
				"DROP INDEX `age_index` ON `user`",
				"CREATE INDEX `age_created_at_index` ON `user` (`age`,`created_at`)",
			}},
			{[]string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_created_at_index` ON `user`",
			}},
			{[]string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"CREATE UNIQUE INDEX `age` ON `user` (`age`)",
			}},
			{[]string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"CREATE UNIQUE INDEX `created_at` ON `user` (`created_at`)",
			}},
			{[]string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique\"`",
			}, []string{
				"DROP INDEX `age` ON `user`",
				"CREATE INDEX `age` ON `user` (`age`)",
			}},
			{[]string{
				"Age int `migu:\"unique\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age` ON `user`",
				"DROP INDEX `created_at` ON `user`",
				"CREATE UNIQUE INDEX `age` ON `user` (`age`)",
			}},
			{[]string{
				"Age int `migu:\"unique:age_unique_index\"`",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age` ON `user`",
				"CREATE UNIQUE INDEX `age_unique_index` ON `user` (`age`)",
			}},
			{[]string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"DROP INDEX `age_unique_index` ON `user`",
			}},
		} {
			src := fmt.Sprintf("package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}")
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
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
		actual, err := migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `age` INT NOT NULL\n" +
				")",
			"CREATE UNIQUE INDEX `age` ON `user` (`age`)",
		}
		if !reflect.DeepEqual(actual, expect) {
			t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
	})

	t.Run("multiple unique indexes", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Age int `migu:\"unique:age_created_at_unique_index\"`",
				"CreatedAt time.Time `migu:\"unique:age_created_at_unique_index\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL,\n" +
					"  `created_at` DATETIME NOT NULL\n" +
					")",
				"CREATE UNIQUE INDEX `age_created_at_unique_index` ON `user` (`age`,`created_at`)",
			}},
			{[]string{
				"Age int `migu:\"index\"`",
				"CreatedAt time.Time `migu:\"unique:created_at_unique_index\"`",
			}, []string{
				"DROP INDEX `age_created_at_unique_index` ON `user`",
				"CREATE INDEX `age` ON `user` (`age`)",
				"CREATE UNIQUE INDEX `created_at_unique_index` ON `user` (`created_at`)",
			}},
		} {
			src := fmt.Sprintf("package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}")
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	t.Run("ALTER TABLE", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Age int",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `age` INT NOT NULL\n" +
					")",
			}},
			{[]string{
				"Age int",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `created_at` DATETIME NOT NULL",
			}},
			{[]string{
				"Age uint8 `migu:\"column:col_a\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` CHANGE `age` `col_a` TINYINT UNSIGNED NOT NULL",
			}},
			{[]string{
				"Age uint8 `migu:\"column:col_b\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `col_b` TINYINT UNSIGNED NOT NULL, DROP `col_a`",
			}},
			{[]string{
				"Age uint8",
				"Old uint8 `migu:\"column:col_b\"`",
				"CreatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` ADD `age` TINYINT UNSIGNED NOT NULL",
			}},
		} {
			src := fmt.Sprintf("package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}")
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	t.Run("ALTER TABLE with multiple tables", func(t *testing.T) {
		before(t)
		if _, err := db.Exec("CREATE TABLE `user` (`age` INT NOT NULL, `gender` INT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("CREATE TABLE `guest` (`age` INT NOT NULL, `sex` INT NOT NULL)"); err != nil {
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
		results, err := migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		actual := results
		expect := []string(nil)
		if !reflect.DeepEqual(actual, expect) {
			t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
		for _, q := range results {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
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
		actual, err := migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			"CREATE TABLE `user` (\n" +
				"  `age` INT NOT NULL\n" +
				")",
		}
		if !reflect.DeepEqual(actual, expect) {
			t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
	})

	t.Run("extra tag", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `created_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP\n" +
					")",
			}},
			{[]string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
				"UpdatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"ALTER TABLE `user` ADD `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP",
			}},
			{[]string{
				"CreatedAt time.Time",
				"UpdatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `created_at` `created_at` DATETIME NOT NULL",
			}},
			{[]string{
				"CreatedAt time.Time `migu:\"extra:ON UPDATE CURRENT_TIMESTAMP\"`",
				"UpdatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` CHANGE `created_at` `created_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP, " +
					"CHANGE `updated_at` `updated_at` DATETIME NOT NULL",
			}},
		} {
			src := "package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}"
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	t.Run("type tag", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Fee float64 `migu:\"type:tinyint\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `fee` TINYINT NOT NULL\n" +
					")",
			}},
			{[]string{
				"Fee float64 `migu:\"type:int\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` INT NOT NULL",
			}},
			{[]string{
				"Fee float64",
				"Point int `migu:\"type:smallint\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` DOUBLE NOT NULL, ADD `point` SMALLINT NOT NULL",
			}},
		} {
			src := "package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}"
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	t.Run("precision tag", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Fee float64 `migu:\"type:decimal,precision:60\"`",
				"Point int `migu:\"precision:11\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `fee` DECIMAL(60) NOT NULL,\n" +
					"  `point` INT NOT NULL\n" +
					")",
			}},
			{[]string{
				"Fee float64 `migu:\"type:decimal,precision:65\"`",
				"Point int `migu:\"precision:12\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` DECIMAL(65) NOT NULL",
			}},
			{[]string{
				"Fee float64",
				"Point int",
				"Balance float64 `migu:\"type:decimal,precision:20\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` DOUBLE NOT NULL, ADD `balance` DECIMAL(20) NOT NULL",
			}},
			{[]string{
				"Fee float64",
				"Point int",
				"Balance float64 `migu:\"type:decimal,precision:20\"`",
			}, []string(nil)},
			{[]string{
				"Fee float64",
				"Point int",
				"Balance float64 `migu:\"type:decimal,precision:20\"`",
				"UpdatedAt time.Time `migu:\"precision:6\"`",
			}, []string{
				"ALTER TABLE `user` ADD `updated_at` DATETIME(6) NOT NULL",
			}},
			{[]string{
				"Fee float64",
				"Point int",
				"Balance float64 `migu:\"type:decimal,precision:20\"`",
				"UpdatedAt time.Time",
			}, []string{
				"ALTER TABLE `user` CHANGE `updated_at` `updated_at` DATETIME NOT NULL",
			}},
		} {
			src := "package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}"
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	t.Run("scale tag", func(t *testing.T) {
		before(t)
		for _, v := range []struct {
			columns []string
			expect  []string
		}{
			{[]string{
				"Fee float64 `migu:\"type:decimal,precision:60,scale:2\"`",
				"Point int `migu:\"precision:11,scale:3\"`",
			}, []string{
				"CREATE TABLE `user` (\n" +
					"  `fee` DECIMAL(60,2) NOT NULL,\n" +
					"  `point` INT NOT NULL\n" +
					")",
			}},
			{[]string{
				"Fee float64 `migu:\"type:decimal,precision:65,scale:3\"`",
				"Point int `migu:\"precision:12,scale:2\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` DECIMAL(65,3) NOT NULL",
			}},
			{[]string{
				"Fee float64",
				"Point int",
				"Balance float64 `migu:\"type:decimal,precision:20,scale:1\"`",
			}, []string{
				"ALTER TABLE `user` CHANGE `fee` `fee` DOUBLE NOT NULL, ADD `balance` DECIMAL(20,1) NOT NULL",
			}},
		} {
			src := "package migu_test\n" +
				"//+migu\n" +
				"type User struct {\n" +
				strings.Join(v.columns, "\n") + "\n" +
				"}"
			results, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			actual := results
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Fatalf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
			for _, q := range results {
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
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
		"[]byte":          "VARCHAR(255)",
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
			if err := testDiffWithSrc(t, t1, s1, t2, s2); err != nil {
				t.Error(err)
				continue
			}
		}
	}
}

func testDiffWithSrc(t *testing.T, t1, s1, t2, s2 string) error {
	src := fmt.Sprintf("package migu_test\n"+
		"//+migu\n"+
		"type User struct {\n"+
		"	A %s\n"+
		"}", t1)
	results, err := migu.Diff(db, "", src)
	if err != nil {
		return err
	}
	actual := results
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n"+
			"  `a` %s\n"+
			")", s1),
	}
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`create: migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	for _, s := range actual {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	defer db.Exec("DROP TABLE IF EXISTS `user`")

	results, err = migu.Diff(db, "", src)
	if err != nil {
		return err
	}
	actual = results
	expect = []string(nil)
	if !reflect.DeepEqual(actual, expect) {
		return fmt.Errorf(`before: migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}

	src = fmt.Sprintf("package migu_test\n"+
		"//+migu\n"+
		"type User struct {\n"+
		"	A %s\n"+
		"}", t2)
	results, err = migu.Diff(db, "", src)
	if err != nil {
		return err
	}
	actual = results
	if s1 == s2 {
		expect = []string(nil)
	} else {
		expect = []string{"ALTER TABLE `user` CHANGE `a` `a` " + s2}
	}
	sort.Strings(actual)
	sort.Strings(expect)
	if !reflect.DeepEqual(actual, expect) {
		return fmt.Errorf(`diff: %s to %s; migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, t1, t2, src, actual, expect)
	}
	for _, s := range actual {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}

	src = "package migu_test"
	results, err = migu.Diff(db, "", src)
	if err != nil {
		return err
	}
	actual = results
	expect = []string{"DROP TABLE `user`"}
	sort.Strings(actual)
	sort.Strings(expect)
	if !reflect.DeepEqual(actual, expect) {
		return fmt.Errorf(`drop: migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	for _, s := range actual {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

func TestDiffWithColumn(t *testing.T) {
	before(t)
	src := fmt.Sprintf("package migu_test\n" +
		"//+migu\n" +
		"type User struct {\n" +
		"	ThisIsColumn string `migu:\"column:aColumn\"`" +
		"}")
	actual, err := migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n" +
			"  `aColumn` VARCHAR(255) NOT NULL\n" +
			")"),
	}
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
	}
}

func TestDiffWithExtraField(t *testing.T) {
	before(t)
	src := fmt.Sprintf("package migu_test\n" +
		"//+migu\n" +
		"type User struct {\n" +
		"	a int\n" +
		"	_ int `migu:\"column:extra\"`\n" +
		"	_ int `migu:\"column:another_extra\"`\n" +
		"	_ int `migu:\"default:yes\"`\n" +
		"}")
	actual, err := migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	expect := []string{
		fmt.Sprintf("CREATE TABLE `user` (\n" +
			"  `extra` INT NOT NULL,\n" +
			"  `another_extra` INT NOT NULL\n" +
			")"),
	}
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
	}
}

func TestDiffMarker(t *testing.T) {
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
			actual, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("CREATE TABLE `user` (\n" +
					"  `a` INT NOT NULL\n" +
					")"),
			}
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
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
			actual, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string(nil)
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
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
		actual, err := migu.Diff(db, "", src)
		if err != nil {
			t.Fatal(err)
		}
		expect := []string{
			fmt.Sprintf("CREATE TABLE `user` (\n" +
				"  `a` INT NOT NULL\n" +
				")"),
		}
		if !reflect.DeepEqual(actual, expect) {
			t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
		}
	})
}

func TestDiffAnnotation(t *testing.T) {
	before(t)
	for _, v := range []struct {
		comment   string
		tableName string
		option    string
	}{
		{`//+migu table:guest`, "guest", ""},
		{`//+migu table:"guest table"`, "guest table", ""},
		{`//+migu table:GuestTable`, "GuestTable", ""},
		{`//+migu table:guest\ntable`, `guest\ntable`, ""},
		{`//+migu table:"\"guest\""`, `"guest"`, ""},
		{`//+migu table:"hoge\"guest\""`, `hoge"guest"`, ""},
		{`//+migu table:"\"guest\"hoge"`, `"guest"hoge`, ""},
		{`//+migu table:"\"\"guest\""`, `""guest"`, ""},
		{`//+migu table:"\"\"guest\"\""`, `""guest""`, ""},
		{`//+migu table:"a\nb"`, "a\nb", ""},
		{`//+migu table:a"`, `a"`, ""},
		{`//+migu table:a""`, `a""`, ""},
		{`//+migu option:ENGINE=InnoDB`, "user", " ENGINE=InnoDB"},
		{`//+migu option:"ROW_FORMAT = DYNAMIC"`, "user", " ROW_FORMAT = DYNAMIC"},
		{`//+migu table:"guest" option:"ROW_FORMAT = DYNAMIC"`, "guest", " ROW_FORMAT = DYNAMIC"},
		{`//+migu option:"ROW_FORMAT = DYNAMIC" table:"guest"`, "guest", " ROW_FORMAT = DYNAMIC"},
	} {
		v := v
		t.Run(fmt.Sprintf("annotation(%#v)", v.comment), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
			actual, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("CREATE TABLE `" + v.tableName + "` (\n" +
					"  `a` INT NOT NULL\n" +
					")" + v.option),
			}
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
		})
	}

	for _, v := range []struct {
		comment string
		expect  string
	}{
		{"//+migu a", "migu: invalid annotation: //+migu a"},
		{"// +migu a", "migu: invalid annotation: // +migu a"},
		{"// +migu a ", "migu: invalid annotation: // +migu a "},
		{`//+migu table:"a" a`, `migu: invalid annotation: //+migu table:"a" a`},
		{`//+migu table:"a"a`, `migu: invalid annotation: //+migu table:"a"a`},
		{`//+migu table:"a":a`, `migu: invalid annotation: //+migu table:"a":a`},
		{`//+migu table:"a" :a`, `migu: invalid annotation: //+migu table:"a" :a`},
		{`//+migu table:"a" a:`, `migu: invalid annotation: //+migu table:"a" a:`},
		{`//+migu table:"a`, `migu: invalid annotation: string not terminated: //+migu table:"a`},
		{`//+migu table: "a"`, `migu: invalid annotation: value not given: //+migu table: "a"`},
	} {
		v := v
		t.Run(fmt.Sprintf("invalid annotation(%#v)", v.comment), func(t *testing.T) {
			src := fmt.Sprintf("package migu_test\n" +
				v.comment + "\n" +
				"type User struct {\n" +
				"	A int\n" +
				"}")
			_, err := migu.Diff(db, "", src)
			actual := fmt.Sprint(err)
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Diff(db, "", %#v) => _, %#v; want _, %#v`, src, actual, expect)
			}
		})
	}
}

func TestDiffDropTable(t *testing.T) {
	before(t)
	for _, v := range []struct {
		table string
	}{
		{"userHoge"},
	} {
		v := v
		t.Run(fmt.Sprintf("DROP TABLE %#v", v.table), func(t *testing.T) {
			if _, err := db.Exec(`CREATE TABLE ` + v.table + `(id int)`); err != nil {
				t.Fatal(err)
			}
			defer db.Exec(`DROP TABLE ` + v.table)
			src := "package migu_test\n"
			actual, err := migu.Diff(db, "", src)
			if err != nil {
				t.Fatal(err)
			}
			expect := []string{
				fmt.Sprintf("DROP TABLE `" + v.table + "`"),
			}
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Diff(db, "", %#v) => %#v; want %#v`, src, actual, expect)
			}
		})
	}
}

func TestFprint(t *testing.T) {
	before(t)
	for _, v := range []struct {
		sqls   []string
		expect string
	}{
		{[]string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255)\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Name *string\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255),\n" +
				"  age  INT\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Name *string\n" +
			"	Age  *int\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  name VARCHAR(255)\n" +
				")",
			"CREATE TABLE post (\n" +
				"  title   VARCHAR(255),\n" +
				"  content VARCHAR(255)\n" +
				")",
		}, "//+migu\n" +
			"type Post struct {\n" +
			"	Title   *string\n" +
			"	Content *string\n" +
			"}\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	Name *string\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  Active BOOL NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Active bool\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  Active BOOL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Active *bool\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  created_at DATETIME NOT NULL\n" +
				")",
		}, "import \"time\"\n" +
			"\n" +
			"//+migu\n" +
			"type User struct {\n" +
			"	CreatedAt time.Time\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"  uuid CHAR(36) NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	UUID string `migu:\"size:36\"`\n" +
			"}\n\n",
		},
		{[]string{
			"CREATE TABLE user (\n" +
				"balance DECIMAL(65,2) NOT NULL\n" +
				")",
		}, "//+migu\n" +
			"type User struct {\n" +
			"	Balance float64 `migu:\"type:decimal,precision:65,scale:2\"`\n" +
			"}\n\n",
		},
	} {
		v := v
		func() {
			for _, sql := range v.sqls {
				if _, err := db.Exec(sql); err != nil {
					t.Error(err)
					return
				}
			}
			defer func() {
				for _, v := range []string{
					`DROP TABLE IF EXISTS user`,
					`DROP TABLE IF EXISTS post`,
				} {
					if _, err := db.Exec(v); err != nil {
						t.Fatal(err)
					}
				}
			}()
			var buf bytes.Buffer
			if err := migu.Fprint(&buf, db); err != nil {
				t.Error(err)
				return
			}
			actual := buf.String()
			expect := v.expect
			if !reflect.DeepEqual(actual, expect) {
				t.Errorf(`migu.Fprint(buf, db); buf => %#v; want %#v`, actual, expect)
			}
		}()
	}
}
