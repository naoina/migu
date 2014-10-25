package migu_test

import (
	"bytes"
	"database/sql"
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

func TestDiffWithSrc(t *testing.T) {
	src := `package migu_test
type User struct {
	Name string
	Age  int
}`
	results, err := migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual := results
	expect := []string{`CREATE TABLE user (
  name VARCHAR(255) NOT NULL,
  age INT NOT NULL
)`}
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	if _, err := db.Exec(actual[0]); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DROP TABLE IF EXISTS user`)

	results, err = migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string(nil)
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}

	src = `package migu_test
type User struct {
	Name string
	Age  uint
}`
	results, err = migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string{`ALTER TABLE user MODIFY age INT UNSIGNED NOT NULL`}
	sort.Strings(actual)
	sort.Strings(expect)
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	if _, err := db.Exec(actual[0]); err != nil {
		t.Fatal(err)
	}

	src = `package migu_test
type User struct {
	Age uint
}`
	results, err = migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string{`ALTER TABLE user DROP name`}
	sort.Strings(actual)
	sort.Strings(expect)
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	if _, err := db.Exec(actual[0]); err != nil {
		t.Fatal(err)
	}

	src = "package migu_test"
	results, err = migu.Diff(db, "", src)
	if err != nil {
		t.Fatal(err)
	}
	actual = results
	expect = []string{`DROP TABLE user`}
	sort.Strings(actual)
	sort.Strings(expect)
	if !reflect.DeepEqual(actual, expect) {
		t.Errorf(`migu.Diff(db, "", %q) => %#v, nil; want %#v, nil`, src, actual, expect)
	}
	if _, err := db.Exec(actual[0]); err != nil {
		t.Fatal(err)
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
)`}, `type User struct {
	Name string
}

`},
		{[]string{`
CREATE TABLE user (
  name VARCHAR(255),
  age  INT
)`}, `type User struct {
	Name string
	Age  int
}

`},
		{[]string{`
CREATE TABLE user (
  name VARCHAR(255)
)`, `

CREATE TABLE post (
  title   VARCHAR(255),
  content VARCHAR(65533)
)`}, `type Post struct {
	Title   string
	Content string
}

type User struct {
	Name string
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
