# Migu [![Build Status](https://travis-ci.org/naoina/migu.svg?branch=master)](https://travis-ci.org/naoina/migu)

Database schema migration tool for [Go](http://golang.org).

Migu has idempotence like Chef or Puppet.

This tool is inspired by [Ridgepole](https://github.com/winebarrel/ridgepole).

## Installation

    go get -u github.com/naoina/migu/cmd/migu

## Basic usage

Save the following Go code as `schema.go`:

```go
package yourownpackagename

type User struct {
	Name string
	Age  int
}
```

Then type the following commands to execute the first migration:

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

`migu sync` command will create the table `user` into database `migu_test` because it still not exist.

Next, modify and save `schema.go` as follows:

```go
package yourownpackagename

type User struct {
	Name string
	Age  uint
}
```

Then type the following commands again:

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

A type of field `age` on `user` table has been changed because type of `Age` in `schema.go` was changed from `int` to `uint`.

See `migu --help` for more options.

## Detailed definition of the column by struct field's tag

You can specify the detailed definition of the column by some struct field's tags.

#### PRIMARY KEY

```go
ID int64 `migu:"pk"`
```

#### AUTOINCREMENT

```go
ID int64 `migu:"autoincrement"`
```

#### UNIQUE

```go
Email string `migu:"unique"`
```

#### DEFAULT

```go
Active bool `migu:"default:true"`
```

If the field type is string, the value doesn't need to be quoted because the value type will be guess by Migu.

```go
Active string `migu:"default:yes"`
```

#### SIZE

```go
Body string `migu:"size:512"` // VARCHAR(512)
```

#### IGNORE

```go
Body string `migu:"-"` // Ignore during migration
```

### Specify the multiple struct field's tags

To specify the multiple struct field's tags to the single column, join with commas.

```go
Email string `migu:"unique,size:512"`
```

## Supported database

* MySQL

## TODO

* Struct Tag support for some ORM
* PostgreSQL and SQLite3 support

## License

MIT
