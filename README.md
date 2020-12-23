# Migu [![build-and-test](https://github.com/naoina/migu/workflows/build-and-test/badge.svg)](https://github.com/naoina/migu/actions?query=workflow%3Abuild-and-test) [![Go Reference](https://pkg.go.dev/badge/github.com/naoina/migu.svg)](https://pkg.go.dev/github.com/naoina/migu)

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

//+migu
type User struct {
    Name string
    Age  int
}
```

Migu uses Go structs for migration that has annotation tag of `+migu` in struct comments.

Second, you enter the following commands to execute the first migration.

```
% mysqladmin -u root create migu_test
% migu sync -u root migu_test schema.go
% mysql -u root migu_test -e 'desc user'
+-------+--------------+------+-----+---------+-------+
| Field | Type         | Null | Key | Default | Extra |
+-------+--------------+------+-----+---------+-------+
| name  | varchar(255) | NO   |     | NULL    |       |
| age   | int(11)      | NO   |     | NULL    |       |
+-------+--------------+------+-----+---------+-------+
```

If `user` table does not exist on the database, `migu sync` command will create `user` table into the database.

Finally, You modify `schema.go` as follows.

```go
package yourownpackagename

//+migu
type User struct {
    Name string
    Age  uint
}
```

Then, run `migu sync` command again.

```
% migu sync -u root migu_test schema.go
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

You can specify `pk` struct field tag to multiple field to define the multiple-column primary key.

```go
UserID    int64 `migu:"pk"`
ProfileID int64 `migu:"pk"`
```

#### AUTOINCREMENT

```go
ID int64 `migu:"autoincrement"`
```

#### INDEX

```go
Email string `migu:"index"`
```

If you want to give another index name, specify the index name as follows.

```go
Email string `migu:"index:email_index"`
```

You can also define multiple-column indexes by specifying the same index name to multiple fields.

```go
Name  string `migu:"index:name_email_index"`
Email string `migu:"index:name_email_index"`
```

#### UNIQUE INDEX

```go
Email string `migu:"unique"`
```

If you want to give another unique index name, specify the unique index name as follows.

```go
Email string `migu:"unique:email_unique_index"`
```

You can also define multiple-column unique indexes by specifying the same unique index name to multiple fields.

```go
Name  string `migu:"unique:name_email_unique_index"`
Email string `migu:"unique:name_email_unique_index"`
```

#### DEFAULT

```go
Active bool `migu:"default:true"`
```

If a field type is string, Migu surrounds a string value by dialect-specific quotes.

```go
Active string `migu:"default:yes"`
```

#### COLUMN

You can specify the column name on the database.

```go
Body string `migu:"column:content"`
```

#### TYPE

To specify the type of column, please use `type` struct tag.

```go
Balance float64 `migu:"type:decimal"`
```

You can also use `type` struct tag to specify the different size of `VARCHAR`, `VARBINARY`, `DECIMAL` and so on.

```go
Balance float64 `migu:"type:decimal(20,2)"`
UUID    string  `migu:"type:varchar(36)"`
```

#### NULL

By default, A user-defined type will be `NOT NULL`. If you don't want to specify `NOT NULL`, you can use `null` struct tag like below.

```go
Amount CustomType `migu:"type:int,null"`
```

#### EXTRA

If you want to add an extra clause to column definition such as `ON UPDATE CURRENT_TIMESTAMP`, you can use `extra` field tag.

```go
UpdatedAt time.Time `migu:"extra:ON UPDATE CURRENT_TIMESTAMP"`
```

The clause specified by `extra` field tag will be added to trailing the column definition like below.

```sql
CREATE TABLE `user` (
  `updated_at` DATETIME NOT NULL ON UPDATE CURRENT_TIMESTAMP
)
```

For Cloud Spanner,

```go
ID int64 `migu:"pk"` // Every table of Cloud Spanner must have a primary key.
UpdatedAt time.Time `migu:"extra:allow_commit_timestamp = true"`
```

```sql
CREATE TABLE `user` (
  `id` INT64 NOT NULL,
  `updated_at` TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true)
) PRIMARY KEY (`id`)
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

## Define extra columns that is not related to struct fields

If you want to define extra columns for the database table that is not related to struct fields, you can use `_` field and `column` struct tag.

```go
package model

import "time"

//+migu
type User struct {
    Name string

    _ time.Time `migu:"column:created_at"`
    _ time.Time `migu:"column:updated_at"`
}
```

This feature can be used for workaround that Migu cannot collect the columns information from fields of embedded fields.
For example, `Timestamp` struct is embedded to `User` struct.

```go
package model

import "time"

type Timestamp struct {
    CreatedAt time.Time
    UpdatedAt time.Time
}

//+migu
type User struct {
    Name string

    Timestamp
}
```

```bash
migu sync -u root --dry-run migu_test
```

```
--------dry-run applying--------
  CREATE TABLE `user` (
    `name` VARCHAR(255) NOT NULL
  )
--------dry-run done 0.000s--------
```

`Timestamp` embedded field does not appear in DDL. The reason for this restriction is that Migu uses Go AST to collect the struct information.
A way to avoid this restriction, you can add definition of some columns of `Timestamp` to `_` fields in `User` struct.

```go
package model

import "time"

type Timestamp struct {
    CreatedAt time.Time
    UpdatedAt time.Time
}

//+migu
type User struct {
    Name string

    Timestamp

    _ time.Time `migu:"column:created_at"`
    _ time.Time `migu:"column:updated_at"`
}
```

```
--------dry-run applying--------
  CREATE TABLE `user` (
    `name` VARCHAR(255) NOT NULL,
    `created_at` DATETIME NOT NULL,
    `updated_at` DATETIME NOT NULL
  )
--------dry-run done 0.000s--------
```

## Annotation

You can specify the some options to the table of database by annotation tags.

### Table name

By default, Migu will decide the table name of the database from the name of Go struct. If you want to specify the different table name, use `table` annotation tag.

```go
package model

//+migu table:"guest"
type User struct {
    Name string
}
```

```
--------dry-run applying--------
CREATE TABLE `guest` (
  `name` VARCHAR(255) NOT NULL
)
--------dry-run done 0.000s--------
```

### Table option

If you want to specify a table option such as `ENGINE`, `DEFAULT CHARSET`, `ROW_FORMAT`, and so on, use `option` annotation tag.

```go
package model

//+migu option:"ENGINE=InnoDB ROW_FORMAT=DYNAMIC"
type User struct {
    Name string
}
```

```
--------dry-run applying--------
CREATE TABLE `user` (
  `name` VARCHAR(255) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 ROW_FORMAT=DYNAMIC
--------dry-run done 0.000s--------
```

## Supported database

* MariaDB/MySQL
* Cloud Spanner

## FAQ

### When does Migu support PostgreSQL and SQLite3?

It is when a Pull Request comes from you!

## License

MIT
