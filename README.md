# Migu [![Build Status](https://travis-ci.org/naoina/migu.svg?branch=master)](https://travis-ci.org/naoina/migu)

Migu is an idempotent database schema migration tool for [Go](http://golang.org).

Migu is inspired by [Ridgepole](https://github.com/winebarrel/ridgepole).

## Installation

```bash
go get -u github.com/naoina/migu/cmd/migu
```

## Basic usage

First, you write Go code to `schema.go` like below.

```go
package yourownpackagename

type User struct {
	Name string
	Age  int
}
```

You enter the following commands to execute the first migration.

```
% mysqladmin -u root create migu_test
% migu -u root sync migu_test schema.go
% mysql -u root migu_test -e 'desc user'
+-------+--------------+------+-----+---------+-------+
| Field | Type         | Null | Key | Default | Extra |
+-------+--------------+------+-----+---------+-------+
| name  | varchar(255) | NO   |     | NULL    |       |
| age   | int(11)      | NO   |     | NULL    |       |
+-------+--------------+------+-----+---------+-------+
```

If `user` table does not exist on the database, `migu sync` command will create `user` table into the database.

Second, You modify `schema.go` as follows.

```go
package yourownpackagename

type User struct {
	Name string
	Age  uint
}
```

Then, run `migu sync` command again.

```
% migu -u root sync migu_test schema.go
% mysql -u root migu_test -e 'desc user'
+-------+------------------+------+-----+---------+-------+
| Field | Type             | Null | Key | Default | Extra |
+-------+------------------+------+-----+---------+-------+
| name  | varchar(255)     | NO   |     | NULL    |       |
| age   | int(10) unsigned | NO   |     | NULL    |       |
+-------+------------------+------+-----+---------+-------+
```

If a type of field of `User` struct is changed, `migu sync` command will change a type of `age` field on the database.
In above case, a type of `Age` field of `User` struct was changed from `int` to `uint`, so a type of `age` field of `user` table on the database has been changed from `int` to `int unsigned` by `migu sync` command.

See `migu --help` for more options.

## Detailed definition of the column by the struct field tag

You can specify the detailed definition of the column by some struct field tags.

#### PRIMARY KEY

```go
ID int64 `migu:"pk"`
```

#### AUTOINCREMENT

```go
ID int64 `migu:"autoincrement"`
```

#### UNIQUE INDEX

```go
Email string `migu:"unique"`
```

#### DEFAULT

```go
Active bool `migu:"default:true"`
```

If a field type is string, Migu surrounds a string value by dialect-specific quotes.

```go
Active string `migu:"default:yes"`
```

#### SIZE

```go
Body string `migu:"size:512"` // VARCHAR(512)
```

#### COLUMN

You can specify the column name on the database.

```go
Body string `migu:"column:content"`
```

#### IGNORE

```go
Body string `migu:"-"` // This field does not affect the migration.
```

### Specify the multiple struct field tags

To specify multiple struct field tags to a single column, join tags with commas.

```go
Email string `migu:"unique,size:512"`
```

## Supported database

* MariaDB/MySQL

## TODO

* Struct Tag support for some ORM
* PostgreSQL and SQLite3 support

## License

MIT
