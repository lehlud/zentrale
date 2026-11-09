package datastore

import (
	"database/sql"
	"fmt"
	"os"

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
		return nil, err
	}

	if path != "" && path != ":memory:" {
		if err := db.Ping(); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := os.Chmod(path, 0600); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	db.SetMaxOpenConns(1)
	i := &Instance{
		path: path,
		db:   db,
	}

	if err = initializeSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to open datastore %s: %v", path, err)
	}

	return i, nil
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
