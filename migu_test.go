package migu_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
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
}

func TestDiff(t *testing.T) {
	before(t)
	t.Run("idempotency", func(t *testing.T) {
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
		"sql.NullString":  "VARCHAR(255)",
		"bool":            "TINYINT NOT NULL",
		"*bool":           "TINYINT",
		"sql.NullBool":    "TINYINT",
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
		expect = []string{"ALTER TABLE `user` MODIFY `a` " + s2}
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
	for _, v := range []struct {
		sqls   []string
		expect string
	}{
		{[]string{`
CREATE TABLE user (
  name VARCHAR(255)
)`}, `//+migu
type User struct {
	Name *string
}

`},
		{[]string{`
CREATE TABLE user (
  name VARCHAR(255),
  age  INT
)`}, `//+migu
type User struct {
	Name *string
	Age  *int
}

`},
		{[]string{`
CREATE TABLE user (
  name VARCHAR(255)
)`, `

CREATE TABLE post (
  title   VARCHAR(255),
  content VARCHAR(255)
)`}, `//+migu
type Post struct {
	Title   *string
	Content *string
}

//+migu
type User struct {
	Name *string
}

`},
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
