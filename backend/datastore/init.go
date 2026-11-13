package datastore

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/duckdb/duckdb-go/v2"
)

type Instance struct {
	path        string
	db          *sql.DB
	reads       *sql.DB
	readQueries atomic.Uint64
}

// Sharing the connector, rather than opening another DSN, also shares in-memory
// databases. Only the writer pool owns/closes the native database handle.
type borrowedConnector struct{ driver.Connector }

func (i *Instance) readDB() *sql.DB {
	i.readQueries.Add(1)
	if i.reads != nil {
		return i.reads
	}
	return i.db // Test fixtures with their own schema retain their existing pool.
}

// ReadQueryCount is a process-local diagnostic counter of attempted read
// statements (not a per-request metric when requests run concurrently).
func (i *Instance) ReadQueryCount() uint64 { return i.readQueries.Load() }

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

	connector, err := duckdb.NewConnector(path, nil)
	if err != nil {
		return nil, openError(path, err)
	}
	db := sql.OpenDB(connector)
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
	// Keep mutations serialized: DuckDB does not retry concurrent write
	// conflicts. Reads use independent MVCC snapshots instead of queueing behind
	// agent writes or another large transcript read on the sole writer connection.
	i.reads = sql.OpenDB(borrowedConnector{connector})
	i.reads.SetMaxOpenConns(4)
	i.reads.SetMaxIdleConns(4)

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

	var readErr error
	if i.reads != nil {
		readErr = i.reads.Close()
		i.reads = nil
	}
	err := errors.Join(readErr, i.db.Close())
	i.db = nil

	return err
}

func (i *Instance) Closed() bool {
	return i == nil || i.db == nil
}
