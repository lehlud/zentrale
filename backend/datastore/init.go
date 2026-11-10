package datastore

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
)

type Instance struct {
	path string
	db   *sql.DB
}

func Open(path string) (*Instance, error) {
	if path != "" && path != ":memory:" {
		if _, err := os.Stat(path); err == nil {
			if err = os.Chmod(path, 0600); err != nil {
				return nil, err
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}

	db, err := sql.Open("duckdb", path)
	if err != nil {
		return nil, openError(path, err)
	}
	db.SetMaxOpenConns(1)

	if path != "" && path != ":memory:" {
		if err := db.Ping(); err != nil {
			_ = db.Close()

			return nil, openError(path, err)
		}

		if err := os.Chmod(path, 0600); err != nil {
			_ = db.Close()

			return nil, err
		}
	}

	i := &Instance{
		path: path,
		db:   db,
	}

	if err = initializeSchema(db); err != nil {
		_ = db.Close()

		return nil, openError(path, err)
	}

	return i, nil
}

func openError(path string, err error) error {
	if strings.Contains(err.Error(), "Could not set lock on file") && strings.Contains(err.Error(), "Conflicting lock") {
		detail := "another process holds the database lock; run only one Zentrale instance per data volume " +
			"and stop the old container before starting its replacement"
		return fmt.Errorf("open datastore %q: %s: %w", path, detail, err)
	}

	return fmt.Errorf("open datastore %q: %w", path, err)
}

func (i *Instance) Close() error {
	if i == nil || i.db == nil {
		return nil
	}

	err := i.db.Close()
	i.db = nil

	return err
}

func (i *Instance) Closed() bool {
	return i == nil || i.db == nil
}
