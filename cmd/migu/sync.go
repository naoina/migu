package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	"github.com/naoina/migu"
)

var (
	dryRunMarker = "dry-run "
)

type sync struct {
	GeneralOption

	DryRun bool `long:"dry-run"`
	Quiet  bool `short:"q" long:"quiet"`
}

func (s *sync) Usage() string {
	return fmt.Sprintf(`Usage: %s sync [OPTIONS] DATABASE [FILE|DIRECTORY]

Options:
      --dry-run          Print the results with no changes
  -q, --quiet            Suppress non-error messages
%s
With no FILE, or when FILE is -, read standard input.
`, progName, s.GeneralOption.Usage())
}

func (s *sync) Execute(args []string) error {
	var dbname string
	var file string
	switch len(args) {
	case 0:
		return &usageError{
			err: fmt.Errorf("too few arguments"),
		}
	case 1:
		dbname = args[0]
	case 2:
		dbname, file = args[0], args[1]
	default:
		return &usageError{
			err: fmt.Errorf("too many arguments"),
		}
	}
	db, err := database(dbname, s.GeneralOption)
	if err != nil {
		return err
	}
	defer db.Close()
	if !s.DryRun {
		dryRunMarker = ""
	}
	return s.run(db, file)
}

func (s *sync) run(db *sql.DB, file string) error {
	var src interface{}
	switch file {
	case "", "-":
		file = ""
		src = os.Stdin
	}
	sqls, err := migu.Diff(db, file, src)
	if err != nil {
		return err
	}
	var tx *sql.Tx
	if !s.DryRun {
		if tx, err = db.Begin(); err != nil {
			return err
		}
	}
	for _, sql := range sqls {
		s.printf("--------%sapplying--------\n", dryRunMarker)
		s.printf("%s\n", sql)
		start := time.Now()
		if !s.DryRun {
			if _, err := tx.Exec(sql); err != nil {
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
