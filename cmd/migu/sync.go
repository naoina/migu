package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
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
	db, err := database(s.Host, s.User, s.Password, dbname)
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
	var sqls []string
	var err error
	switch file {
	case "", "-":
		if sqls, err = migu.Diff(db, "", os.Stdin); err != nil {
			return err
		}
	default:
		info, err := os.Stat(file)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			sqls, err = migu.Diff(db, file, nil)
			break
		}
		if err := filepath.Walk(file, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			switch info.Name()[0] {
			case '.', '_':
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if info.IsDir() {
				return nil
			}
			results, err := migu.Diff(db, path, nil)
			if err != nil {
				return err
			}
			sqls = append(sqls, results...)
			return nil
		}); err != nil {
			return err
		}
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
