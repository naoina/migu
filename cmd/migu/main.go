package main

import (
	"database/sql"
	"fmt"
	"net"
	"os"

	"github.com/go-sql-driver/mysql"
	"github.com/goccy/go-yaml"
	"github.com/howeyc/gopass"
	"github.com/naoina/migu/dialect"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	progName = "migu"

	databaseTypeMySQL   = "mysql"
	databaseTypeMariaDB = "mariadb"
	databaseTypeSpanner = "spanner"
)

var (
	rootCmd = &cobra.Command{
		Use:   progName,
		Short: "An idempotent database schema migration tool",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := validateFlags(option); err != nil {
				return err
			}
			if fname := option.global.columnTypeFile; fname != "" {
				columnTypes, err := readColumnTypeFromFile(option.global.columnTypeFile)
				if err != nil {
					return err
				}
				option.global.ColumnTypes = columnTypes
			}
			return nil
		},
	}
	option      = &Option{}
	protocolMap = map[string]string{
		"tcp":    "tcp",
		"socket": "unix",
	}
)

type Option struct {
	global struct {
		DatabaseType string
		ColumnTypes  []*dialect.ColumnType

		columnTypeFile string
	}
	mysql struct {
		User     string
		Host     string
		Password string
		Port     int
		Protocol string
	}
	spanner struct {
		Project  string
		Instance string
	}
}

func init() {
	flagsForGlobal := pflag.NewFlagSet("Global", pflag.ContinueOnError)
	flagsForGlobal.StringVarP(&option.global.DatabaseType, "type", "t", databaseTypeMySQL, "Specify the database type (mysql|mariadb|spanner)")
	flagsForGlobal.StringVar(&option.global.columnTypeFile, "column-type-file", "", "Use the definition file of custom column types. Supported format is YAML")

	flagsForMySQL := pflag.NewFlagSet("MySQL/MariaDB", pflag.ContinueOnError)
	flagsForMySQL.StringVarP(&option.mysql.Host, "host", "h", "", "Connect to host of database")
	flagsForMySQL.StringVarP(&option.mysql.User, "user", "u", "", "User for login to database if not current user")
	flagsForMySQL.StringVarP(&option.mysql.Password, "password", "p", "", "Password to use when connecting to server.\nIf password is not given, it's asked from the tty")
	flagsForMySQL.Lookup("password").NoOptDefVal = "PASS"
	flagsForMySQL.IntVarP(&option.mysql.Port, "port", "P", 0, "Port number to use for connection")
	flagsForMySQL.StringVar(&option.mysql.Protocol, "protocol", "tcp", "The protocol to use for connection (tcp, socket)")

	flagsForSpanner := pflag.NewFlagSet("Cloud Spanner", pflag.ContinueOnError)
	flagsForSpanner.StringVar(&option.spanner.Project, "project", os.Getenv("SPANNER_PROJECT_ID"), "The Google Cloud Platform project name")
	if flag := flagsForSpanner.Lookup("project"); flag.DefValue == "" {
		flag.DefValue = "$SPANNER_PROJECT_ID"
	} else {
		flag.DefValue += " from $SPANNER_PROJECT_ID"
	}
	flagsForSpanner.StringVar(&option.spanner.Instance, "instance", os.Getenv("SPANNER_INSTANCE_ID"), "The Cloud Spanner instance name")
	if flag := flagsForSpanner.Lookup("instance"); flag.DefValue == "" {
		flag.DefValue = "$SPANNER_INSTANCE_ID"
	} else {
		flag.DefValue += " from $SPANNER_INSTANCE_ID"
	}

	rootCmd.PersistentFlags().AddFlagSet(flagsForGlobal)
	rootCmd.PersistentFlags().AddFlagSet(flagsForMySQL)
	rootCmd.PersistentFlags().AddFlagSet(flagsForSpanner)
	rootCmd.PersistentFlags().Bool("help", false, "Display this help and exit")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true
	rootCmd.SetUsageTemplate(usageTemplate)
	rootCmd.SetHelpTemplate(helpTemplate)
	type flagset struct {
		Name  string
		Flags *pflag.FlagSet
	}
	cobra.AddTemplateFuncs(map[string]interface{}{
		"flagsets": func() []flagset {
			return []flagset{
				{
					Name:  "",
					Flags: flagsForGlobal,
				},
				{
					Name:  "MySQL/MariaDB",
					Flags: flagsForMySQL,
				},
				{
					Name:  "Cloud Spanner",
					Flags: flagsForSpanner,
				},
			}
		},
	})
}

func openDatabase(dbname string) (db *sql.DB, err error) {
	opt := option.mysql
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
		if config.Passwd == "PASS" {
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

func readColumnTypeFromFile(fname string) ([]*dialect.ColumnType, error) {
	f, err := os.Open(fname)
	if err != nil {
		return nil, fmt.Errorf("failed to read column type file: %w", err)
	}
	defer f.Close()
	var columnTypes []*dialect.ColumnType
	if err := yaml.NewDecoder(f, yaml.DisallowDuplicateKey()).Decode(&columnTypes); err != nil {
		return nil, fmt.Errorf("failed to decode column type file: %w", err)
	}
	return columnTypes, nil
}

func validateFlags(opt *Option) error {
	if opt.global.DatabaseType == "" {
		return fmt.Errorf("database type is required")
	}
	switch typ := opt.global.DatabaseType; typ {
	case databaseTypeMySQL, databaseTypeMariaDB, databaseTypeSpanner:
		// do nothing.
	default:
		return fmt.Errorf("unknown database type: %s", opt.global.DatabaseType)
	}
	switch opt.global.DatabaseType {
	case databaseTypeMySQL, databaseTypeMariaDB:
		if opt.mysql.Protocol == "" {
			return fmt.Errorf("protocol is required")
		}
		if _, ok := protocolMap[opt.mysql.Protocol]; !ok {
			return fmt.Errorf("unknown protocol: %s", opt.mysql.Protocol)
		}
	case databaseTypeSpanner:
		if opt.spanner.Project == "" {
			return fmt.Errorf("project is required")
		}
		if opt.spanner.Instance == "" {
			return fmt.Errorf("instance is required")
		}
	}
	return nil
}

func main() {
	for _, cmd := range rootCmd.Commands() {
		cmd.DisableFlagsInUseLine = true
	}
	rootCmd.Execute()
}
