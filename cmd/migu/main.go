package main

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/go-sql-driver/mysql"
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

	protocolMap = map[string]string{
		"tcp":    "tcp",
		"socket": "unix",
	}
)

type GeneralOption struct {
	User     string `short:"u" long:"user"`
	Host     string `short:"h" long:"host"`
	Password string `short:"p" long:"password" optional:"true" optional-value:"\x00"`
	Port     uint16 `short:"P" long:"port"`
	Protocol string `long:"protocol" choice:"tcp" choice:"socket" default:"tcp"`
	Help     bool   `long:"help"`
}

func (o *GeneralOption) Usage() string {
	return "" +
		"  -u, --user=NAME        User for login to database if not current user\n" +
		"  -h, --host=HOST        Connect to host of database\n" +
		"  -u, --user=NAME        User for login to database if not current user\n" +
		"  -p, --password[=PASS]  Password to use when connecting to server.\n" +
		"                         If password is not given, it's asked from the tty\n" +
		"  -P, --port=#           Port number to use for connection\n" +
		"      --protocol=name    The protocol to use for connection (tcp, socket)\n" +
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

func selectCommand(args []string) (Command, error) {
	if len(args) < 1 {
		return nil, &usageError{
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
		return nil, &usageError{
			usage: usage,
			err:   fmt.Errorf("unknown command: %s", c),
		}
	}
	return cmd, nil
}

func database(dbname string, opt GeneralOption) (db *sql.DB, err error) {
	config := mysql.NewConfig()
	config.User = opt.User
	if config.User == "" {
		if config.User = os.Getenv("USERNAME"); config.User == "" {
			if config.User = os.Getenv("USER"); config.User == "" {
				return nil, fmt.Errorf("user is not specified and current user cannot be detected")
			}
		}
	}
	config.Passwd = opt.Password
	if config.Passwd != "" {
		if config.Passwd == "\x00" {
			p, err := gopass.GetPasswdPrompt("Enter password: ", false, os.Stdin, os.Stderr)
			if err != nil {
				return nil, err
			}
			config.Passwd = string(p)
		}
	}
	config.Net = protocolMap[opt.Protocol]
	config.Addr = opt.Host
	if opt.Port > 0 {
		config.Addr = net.JoinHostPort(config.Addr, fmt.Sprintf("%d", opt.Port))
	}
	config.DBName = dbname
	return sql.Open("mysql", config.FormatDSN())
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
	cmd, err := selectCommand(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	parser, err = newParser(cmd)
	if err != nil {
		panic(err)
	}
	args, err = parser.ParseArgs(args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(1)
	}
	if cmd.ShowHelp() {
		fmt.Fprintln(os.Stderr, cmd.Usage())
		os.Exit(0)
	}
	if err := cmd.Execute(args); err != nil {
		if err, ok := err.(*usageError); ok && err.usage == "" {
			err.usage = cmd.Usage()
		}
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
