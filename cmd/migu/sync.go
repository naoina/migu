package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	return fmt.Sprintf(`Usage: %s sync [OPTIONS] DATABASE [FILE]

Options:
      --dry-run          Print the results with no changes
  -q, --quiet            Suppress non-error messages
%s
With no FILE, or when FILE is -, read standard input.
`, progName, s.GeneralOption.Usage())
}

func (s *sync) Execute(args []string) error {
	var dbname string
	var path string
	switch len(args) {
	case 0:
		return &usageError{
			err: fmt.Errorf("too few arguments"),
		}
	case 1:
		dbname = args[0]
	case 2:
		dbname, path = args[0], args[1]
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
	fInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	filenames := []string{}
	if fInfo.IsDir() {
		if filenames, err = walk(path); err != nil {
			return err
		}
	} else {
		filenames = append(filenames, path)
	}
	return s.run(db, filenames)
}

func walk(root string) ([]string, error) {
	filenames := []string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		filenames = append(filenames, path)
		return nil
	}); err != nil {
		return nil, err
	}
	return filenames, nil
}

func (s *sync) run(db *sql.DB, filenames []string) error {
	var src io.Reader
	switch len(filenames) {
	case 1:
		switch filenames[0] {
		case "", "-":
			filenames = []string{""}
			src = os.Stdin
		}
	}
	sqls, err := migu.Diff(db, filenames, src)
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
		s.printf("  %s\n", strings.Replace(sql, "\n", "\n  ", -1))
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
