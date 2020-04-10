package main

import (
	"fmt"
	"os"

	"github.com/naoina/migu"
	"github.com/naoina/migu/dialect"
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
	db, err := database(dbname, d.GeneralOption)
	if err != nil {
		return err
	}
	defer db.Close()
	di := dialect.NewMySQL(db)
	return d.run(di, filename)
}

func (d *dump) run(di dialect.Dialect, filename string) error {
	out := os.Stdout
	if filename != "" {
		file, err := os.Create(filename)
		if err != nil {
			return err
		}
		defer file.Close()
		out = file
	}
	return migu.Fprint(out, di)
}
