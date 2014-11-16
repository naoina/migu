package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/naoina/migu"
)

type dump struct {
	GeneralOption
}

func (d *dump) Usage() string {
	return fmt.Sprintf(`Usage: %s dump [OPTIONS] DATABASE [FILE]

Options:
%s
With FILE, output to FILE.
`, progName, d.GeneralOption.Usage())
}

func (d *dump) Execute(args []string) error {
	var dbname string
	var filename string
	switch len(args) {
	case 0:
		return &usageError{
			err: fmt.Errorf("too few arguments"),
		}
	case 1:
		dbname = args[0]
	case 2:
		dbname, filename = args[0], args[1]
	default:
		return &usageError{
			err: fmt.Errorf("too many arguments"),
		}
	}
	db, err := database(d.Host, d.User, d.Password, dbname)
	if err != nil {
		return err
	}
	defer db.Close()
	return d.run(db, filename)
}

func (d *dump) run(db *sql.DB, filename string) error {
	out := os.Stdout
	if filename != "" {
		file, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	return migu.Fprint(out, db)
}
