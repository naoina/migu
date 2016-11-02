package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "github.com/go-sql-driver/mysql"
	"github.com/howeyc/gopass"
	"github.com/jessevdk/go-flags"
)

var (
	progName = filepath.Base(os.Args[0])
	option   struct {
		Help bool `long:"help"`
	}
	usage = fmt.Sprintf(`Usage: %s [OPTIONS] COMMAND [ARG...]

Commands:
  sync      synchronize the database schema
  dump      dump the database schema as Go code

Options:
      --help             Display this help and exit
`, progName)
)

type GeneralOption struct {
	User     string `short:"u" long:"user"`
	Host     string `short:"h" long:"host"`
	Password string `short:"p" long:"password" optional:"true" optional-value:"\x00"`
	Help     bool   `long:"help"`
}

func (o *GeneralOption) Usage() string {
	return "" +
		"  -u, --user=NAME        User for login to database if not current user\n" +
		"  -h, --host=HOST        Connect to host of database\n" +
		"  -u, --user=NAME        User for login to database if not current user\n" +
		"  -p, --password[=PASS]  Password to use when connecting to server.\n" +
		"                         If password is not given, it's asked from the tty\n" +
		"      --help             Display this help and exit\n"
}

func (o *GeneralOption) ShowHelp() bool {
	return o.Help
}

type Command interface {
	Execute(args []string) error
	Usage() string
	ShowHelp() bool
}

type usageError struct {
	usage string
	err   error
}

func (u *usageError) Error() string {
	return fmt.Sprintf("%v\n%v", u.err, u.usage)
}

func run(args []string) error {
	if len(args) < 1 {
		return &usageError{
			usage: usage,
			err:   fmt.Errorf("too few arguments"),
		}
	}
	var cmd Command
	switch c := args[0]; c {
	case "sync":
		cmd = &sync{}
	case "dump":
		cmd = &dump{}
	default:
		return &usageError{
			usage: usage,
			err:   fmt.Errorf("unknown command: %s", c),
		}
	}
	parser, err := newParser(cmd)
	if err != nil {
		return err
	}
	args, err = parser.ParseArgs(args[1:])
	if err != nil {
		return err
	}
	if cmd.ShowHelp() {
		fmt.Fprintln(os.Stderr, cmd.Usage())
		return nil
	}
	if err := cmd.Execute(args); err != nil {
		if err, ok := err.(*usageError); ok && err.usage == "" {
			err.usage = cmd.Usage()
		}
		return err
	}
	return nil
}

func database(host, user, password, dbname string) (db *sql.DB, err error) {
	if user == "" {
		if user = os.Getenv("USERNAME"); user == "" {
			if user = os.Getenv("USER"); user == "" {
				return nil, fmt.Errorf("user is not specified and current user cannot be detected")
			}
		}
	}
	dsn := []byte(user)
	if password != "" {
		if password == "\x00" {
			fmt.Printf("Enter password: ")
			p, err := gopass.GetPasswd()
			if err != nil {
				return nil, err
			}
			password = string(p)
		}
		dsn = append(append(dsn, ':'), password...)
	}
	if len(dsn) > 0 {
		dsn = append(dsn, '@')
	}
	if host != "" {
		dsn = append(append(append(dsn, "tcp("...), host...), ')')
	}
	dsn = append(append(dsn, '/'), dbname...)
	return sql.Open("mysql", string(dsn))
}

func newParser(option interface{}) (*flags.Parser, error) {
	parser := flags.NewNamedParser(progName, flags.PrintErrors|flags.PassDoubleDash|flags.PassAfterNonOption)
	if _, err := parser.AddGroup("", "", option); err != nil {
		return nil, err
	}
	return parser, nil
}

func main() {
	parser, err := newParser(&option)
	if err != nil {
		panic(err)
	}
	args, err := parser.Parse()
	if err != nil {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	if option.Help {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(0)
	}
	if err := run(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", progName, err)
		os.Exit(1)
	}
}
