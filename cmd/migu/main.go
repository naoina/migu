package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/howeyc/gopass"
	"github.com/jessevdk/go-flags"
	"github.com/naoina/migu"
)

var (
	progName = filepath.Base(os.Args[0])
	option   struct {
		User     string `short:"u" long:"user"`
		Host     string `short:"h" long:"host"`
		Password string `short:"p" long:"password" optional:"true" optional-value:"\x00"`
		DryRun   bool   `long:"dry-run"`
		Quiet    bool   `short:"q" long:"quiet"`
		Help     bool   `long:"help"`

		cmd    string
		file   string
		dbname string
	}
	dryRunMarker = "dry-run "
	printf       = fmt.Printf
)

type usageError struct {
	error
}

func usage(code int) {
	fmt.Fprintf(os.Stderr, `Usage: %s [OPTIONS] COMMAND DATABASE [FILE]

Commands:
  sync      synchronize the database schema

Options:
  -u, --user=NAME        User for login to database if not current user
  -h, --host=HOST        Connect to host of database
  -p, --password[=PASS]  Password to use when connecting to server.
                         If password is not given, it's asked from the tty
      --dry-run          Print the results with no changes
  -q, --quiet            Suppress non-error messages
      --help             Display this help and exit

With no FILE, or when FILE is -, read standard input.

`, progName)
	os.Exit(code)
}

func run(args []string) error {
	switch len(args) {
	case 0, 1:
		return &usageError{
			error: fmt.Errorf("too few arguments"),
		}
	case 2:
		option.cmd, option.dbname = args[0], args[1]
	case 3:
		option.cmd, option.dbname, option.file = args[0], args[1], args[2]
	default:
		return &usageError{
			error: fmt.Errorf("too many arguments"),
		}
	}
	db, err := database()
	if err != nil {
		return err
	}
	defer db.Close()
	switch option.cmd {
	case "sync":
		return sync(db)
	default:
		return &usageError{
			error: fmt.Errorf("unknown command: %s", option.cmd),
		}
	}
}

func sync(db *sql.DB) error {
	var src io.Reader
	switch option.file {
	case "", "-":
		option.file = ""
		src = os.Stdin
	}
	sqls, err := migu.Diff(db, option.file, src)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, sql := range sqls {
		printf("--------%sapplying--------\n", dryRunMarker)
		printf("  %s\n", strings.Replace(sql, "\n", "\n  ", -1))
		start := time.Now()
		if !option.DryRun {
			if _, err := tx.Exec(sql); err != nil {
				tx.Rollback()
				return err
			}
		}
		d := time.Since(start)
		printf("--------%sdone %.3fs--------\n", dryRunMarker, d.Seconds()/time.Second.Seconds())
	}
	if option.DryRun {
		return nil
	} else {
		return tx.Commit()
	}
}

func database() (db *sql.DB, err error) {
	if option.User == "" {
		if option.User = os.Getenv("USERNAME"); option.User == "" {
			if option.User = os.Getenv("USER"); option.User == "" {
				return nil, fmt.Errorf("user is not specified and current user cannot be detected")
			}
		}
	}
	dsn := bytes.NewBufferString(option.User)
	if option.Password != "" {
		if option.Password == "\x00" {
			fmt.Printf("Enter password: ")
			option.Password = string(gopass.GetPasswd())
		}
		dsn.WriteByte(':')
		dsn.WriteString(option.Password)
	}
	if dsn.Len() > 0 {
		dsn.WriteByte('@')
	}
	if option.Host != "" {
		dsn.WriteString("tcp(")
		dsn.WriteString(option.Host)
		dsn.WriteByte(')')
	}
	dsn.WriteByte('/')
	dsn.WriteString(option.dbname)
	return sql.Open("mysql", dsn.String())
}

func main() {
	parser := flags.NewNamedParser(progName, flags.PrintErrors|flags.PassDoubleDash|flags.PassAfterNonOption)
	if _, err := parser.AddGroup("", "", &option); err != nil {
		panic(err)
	}
	args, err := parser.Parse()
	if err != nil {
		usage(1)
	}
	if option.Help {
		usage(0)
	}
	if !option.DryRun {
		dryRunMarker = ""
	}
	if option.Quiet {
		printf = func(string, ...interface{}) (int, error) { return 0, nil }
	}
	if err := run(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", progName, err)
		if _, ok := err.(*usageError); ok {
			usage(1)
		} else {
			os.Exit(1)
		}
	}
}
