# Migu [![Build Status](https://travis-ci.org/naoina/migu.png?branch=master)](https://travis-ci.org/naoina/migu)

Database schema migration tool for [Go](http://golang.org).

Migu has idempotence like Chef or Puppet.

This tool is inspired by [Ridgepole](https://github.com/winebarrel/ridgepole).

## Installation

    go get -u github.com/naoina/migu/cmd/migu

## Usage

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
% mysqladmin create migu_test
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

## Supported database

* MySQL

## TODO

* Struct Tag support for some ORM
* PostgreSQL and SQLite3 support

## License

MIT
