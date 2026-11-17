package datastore

import (
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	_ "github.com/mattn/go-sqlite3"
)

// One database and one ordinary connection pool. SQLite WAL coordinates short
// write transactions and concurrent readers without application-wide DB locks.
type Instance struct {
	path                      string
	db                        *sql.DB
	temporary                 string
	readQueries               atomic.Uint64
	closeOnce                 sync.Once
	closeErr                  error
	closed                    atomic.Bool
	credentialMu              sync.Mutex
	credentialKey             []byte
	identityOnce              sync.Once
	identityID, identityToken string
	identityError             error
}

func (i *Instance) readDB() *sql.DB        { i.readQueries.Add(1); return i.db }
func (i *Instance) ReadQueryCount() uint64 { return i.readQueries.Load() }

func Open(path string) (*Instance, error) {
	requested := path
	temporary := ""
	if path == "" || path == ":memory:" {
		// Disposable WAL files give tests production concurrency semantics,
		// avoiding the different locking rules of shared-cache in-memory SQLite.
		file, err := os.CreateTemp("", "zentrale-*.sqlite")
		if err != nil {
			return nil, err
		}
		path, temporary = file.Name(), file.Name()
		_ = file.Close()
	}
	cleanup := func() {
		if temporary != "" {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(temporary + suffix)
			}
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		cleanup()
		return nil, err
	}
	if info, err := os.Stat(abs); err == nil {
		if info.Size() > 0 {
			file, err := os.Open(abs)
			if err != nil {
				cleanup()
				return nil, err
			}
			header := make([]byte, 16)
			_, readErr := io.ReadFull(file, header)
			_ = file.Close()
			if readErr != nil || string(header) != "SQLite format 3\x00" {
				cleanup()
				return nil, fmt.Errorf("database %q is not SQLite; import the existing DuckDB into a new SQLite file before starting Zentrale (the original was not changed)", abs)
			}
		}
		if err := os.Chmod(abs, 0600); err != nil {
			cleanup()
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		cleanup()
		return nil, err
	} else if filepath.Base(abs) == "zentrale.sqlite" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(abs), "zentrale.duckdb")); err == nil {
			cleanup()
			return nil, fmt.Errorf("existing zentrale.duckdb found; import it into %s first instead of starting with an empty database", abs)
		}
	}
	// Create with restrictive permissions before SQLite creates its WAL/SHM
	// sidecars, whose initial permissions derive from the database file.
	file, createErr := os.OpenFile(abs, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if createErr == nil {
		_ = file.Close()
	} else if !os.IsExist(createErr) {
		cleanup()
		return nil, createErr
	}
	address := url.URL{Scheme: "file", Path: abs}
	options := url.Values{"_busy_timeout": {"5000"}, "_journal_mode": {"WAL"}, "_foreign_keys": {"on"}, "_txlock": {"immediate"}, "_synchronous": {"FULL"}, "_loc": {"UTC"}}
	db, err := sql.Open("sqlite3", address.String()+"?"+options.Encode())
	if err != nil {
		cleanup()
		return nil, err
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	if err := initializeSchema(db); err != nil {
		_ = db.Close()
		cleanup()
		return nil, err
	}
	if err := os.Chmod(abs, 0600); err != nil {
		_ = db.Close()
		cleanup()
		return nil, err
	}
	if temporary == "" {
		requested = abs
	}
	return &Instance{path: requested, db: db, temporary: temporary}, nil
}

func (i *Instance) Close() error {
	if i == nil {
		return nil
	}
	i.closeOnce.Do(func() {
		i.closed.Store(true)
		if i.db != nil {
			i.closeErr = i.db.Close()
		}
		if i.temporary != "" {
			for _, suffix := range []string{"", "-wal", "-shm"} {
				_ = os.Remove(i.temporary + suffix)
			}
		}
	})
	return i.closeErr
}
func (i *Instance) Closed() bool { return i == nil || i.closed.Load() || i.db == nil }
