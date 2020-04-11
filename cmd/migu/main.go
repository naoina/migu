package main

import (
	"database/sql"
	"fmt"
	"net"
	"os"

	"github.com/go-sql-driver/mysql"
	"github.com/howeyc/gopass"
	"github.com/spf13/cobra"
)

var (
	progName = os.Args[0]
	rootCmd  = &cobra.Command{
		Use: progName,
	}
	generalOption struct {
		User     string
		Host     string
		Password string
		Port     int
		Protocol string
	}
	protocolMap = map[string]string{
		"tcp":    "tcp",
		"socket": "unix",
	}
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&generalOption.Host, "host", "h", "", "Connect to host of database")
	rootCmd.PersistentFlags().StringVarP(&generalOption.User, "user", "u", "", "User for login to database if not current user")
	rootCmd.PersistentFlags().StringVarP(&generalOption.Password, "password", "p", "", "Password to use when connecting to server.\nIf password is not given, it's asked from the tty")
	rootCmd.PersistentFlags().Lookup("password").NoOptDefVal = "PASS"
	rootCmd.PersistentFlags().IntVarP(&generalOption.Port, "port", "P", 0, "Port number to use for connection")
	rootCmd.PersistentFlags().StringVar(&generalOption.Protocol, "protocol", "tcp", "The protocol to use for connection (tcp, socket)")
	rootCmd.PersistentFlags().Bool("help", false, "Display this help and exit")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true
	rootCmd.SetUsageTemplate(usageTemplate)
	cobra.OnInitialize(func() {
		for _, cmd := range rootCmd.Commands() {
			cmd.DisableFlagsInUseLine = true
		}
	})
}

func database(dbname string) (db *sql.DB, err error) {
	config := mysql.NewConfig()
	config.User = generalOption.User
	if config.User == "" {
		if config.User = os.Getenv("USERNAME"); config.User == "" {
			if config.User = os.Getenv("USER"); config.User == "" {
				return nil, fmt.Errorf("user is not specified and current user cannot be detected")
			}
		}
	}
	config.Passwd = generalOption.Password
	if config.Passwd != "" {
		if config.Passwd == "PASS" {
			p, err := gopass.GetPasswdPrompt("Enter password: ", false, os.Stdin, os.Stderr)
			if err != nil {
				return nil, err
			}
			config.Passwd = string(p)
		}
	}
	protocol, ok := protocolMap[generalOption.Protocol]
	if !ok {
		return nil, fmt.Errorf("protocol must be 'tcp' or 'socket'")
	}
	config.Net = protocol
	config.Addr = generalOption.Host
	if generalOption.Port > 0 {
		config.Addr = net.JoinHostPort(config.Addr, fmt.Sprintf("%d", generalOption.Port))
	}
	config.DBName = dbname
	return sql.Open("mysql", config.FormatDSN())
}

func main() {
	rootCmd.Execute()
}
