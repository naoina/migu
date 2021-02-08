package main

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/naoina/migu"
	"github.com/naoina/migu/dialect"
	"github.com/spf13/cobra"
)

var (
	dryRunMarker = "dry-run "
)

func init() {
	sync := &sync{}
	syncCmd := &cobra.Command{
		Use:   "sync [OPTIONS] DATABASE [FILE|DIRECTORY]",
		Short: "synchronize the database schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sync.Execute(args, option)
		},
	}
	syncCmd.Flags().BoolVar(&sync.DryRun, "dry-run", false, "")
	syncCmd.Flags().BoolVarP(&sync.Quiet, "quiet", "q", false, "")
	syncCmd.SetUsageTemplate(usageTemplate + "\nWith no FILE, or when FILE is -, read standard input.\n")
	rootCmd.AddCommand(syncCmd)
}

type sync struct {
	DryRun bool
	Quiet  bool
}

func (s *sync) Execute(args []string, opt *Option) error {
	var dbname string
	var file string
	switch len(args) {
	case 0:
		return fmt.Errorf("too few arguments")
	case 1:
		dbname = args[0]
	case 2:
		dbname, file = args[0], args[1]
	default:
		return fmt.Errorf("too many arguments")
	}
	var opts []dialect.Option
	if columnTypes := opt.global.ColumnTypes; len(columnTypes) != 0 {
		opts = append(opts, dialect.WithColumnType(columnTypes))
	}
	var di dialect.Dialect
	switch typ := opt.global.DatabaseType; typ {
	case databaseTypeMySQL, databaseTypeMariaDB:
		db, err := openDatabase(dbname)
		if err != nil {
			return err
		}
		defer db.Close()
		di = dialect.NewMySQL(db, opts...)
	case databaseTypeSpanner:
		di = dialect.NewSpanner(path.Join("projects", opt.spanner.Project, "instances", opt.spanner.Instance, "databases", dbname), opts...)
	default:
		return fmt.Errorf("BUG: unknown database type: %s", typ)
	}
	if !s.DryRun {
		dryRunMarker = ""
	}
	return s.run(di, file)
}

func (s *sync) run(d dialect.Dialect, file string) error {
	var src interface{}
	switch file {
	case "", "-":
		file = ""
		src = os.Stdin
	}
	sqls, err := migu.Diff(d, file, src)
	if err != nil {
		return err
	}
	var tx dialect.Transactioner
	if !s.DryRun {
		if tx, err = d.Begin(); err != nil {
			return err
		}
	}
	for _, sql := range sqls {
		s.printf("--------%sapplying--------\n", dryRunMarker)
		s.printf("%s\n", sql)
		start := time.Now()
		if !s.DryRun {
			if err := tx.Exec(sql); err != nil {
				tx.Rollback()
				return err
			}
		}
		d := time.Since(start)
		s.printf("--------%sdone %.3fs--------\n", dryRunMarker, d.Seconds()/time.Second.Seconds())
	}
	if s.DryRun {
		return nil
	} else {
		return tx.Commit()
	}
}

func (s *sync) printf(format string, a ...interface{}) (int, error) {
	if s.Quiet {
		return 0, nil
	}
	return fmt.Printf(format, a...)
}
