//go:build duckdb_import

package datastore

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// ImportDuckDB copies a consistent, read-only snapshot into a new SQLite file.
// It never opens the source for writing or replaces an existing destination.
// DuckDB is linked only into the explicitly tagged import command.
func ImportDuckDB(ctx context.Context, source, destination string) (map[string]int64, error) {
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return nil, err
	}
	for _, path := range []string{destination, destination + ".credentials.key"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return nil, fmt.Errorf("destination must not exist: %s", path)
		}
	}
	old, err := sql.Open("duckdb", source+"?access_mode=read_only")
	if err != nil {
		return nil, err
	}
	defer old.Close()
	old.SetMaxOpenConns(1)
	snapshot, err := old.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("open read-only DuckDB snapshot (stop Zentrale first): %w", err)
	}
	defer snapshot.Rollback()
	var encrypted int
	if err := snapshot.QueryRowContext(ctx, "SELECT count(*) FROM credential_records").Scan(&encrypted); err != nil {
		return nil, err
	}
	var key []byte
	if encrypted > 0 {
		key, err = readCredentialKey(source + ".credentials.key")
		if err != nil {
			return nil, fmt.Errorf("original credential key required: %w", err)
		}
	}
	staging, err := os.CreateTemp(filepath.Dir(destination), ".zentrale-import-*.sqlite")
	if err != nil {
		return nil, err
	}
	stage := staging.Name()
	_ = staging.Close()
	defer func() {
		for _, suffix := range []string{"", "-wal", "-shm", ".credentials.key"} {
			_ = os.Remove(stage + suffix)
		}
	}()
	store, err := Open(stage)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	tables := []struct{ name, columns string }{
		{"private_ssh_keys", "id,x509_data,purpose,purpose_id"},
		{"known_ssh_keys", "x509_data"}, {"settings", "name,value"},
		{"credential_records", "name,ciphertext"}, {"sessions", "token_hash,expires_at"},
		{"attachment_blobs", "agent_id,project_id,id,metadata,bytes"},
		{"resources", "kind,id,parent_id,data,created_at"},
		{"events", "id,type,agent_id,data,created_at"},
	}
	counts := map[string]int64{}
	for _, table := range tables {
		columns := strings.Split(table.columns, ",")
		projection := append([]string(nil), columns...)
		for n, column := range columns {
			if column == "data" || column == "metadata" {
				projection[n] = "CAST(" + column + " AS VARCHAR)"
			}
		}
		rows, err := snapshot.QueryContext(ctx, "SELECT "+strings.Join(projection, ",")+" FROM "+table.name)
		if err != nil {
			return nil, err
		}
		statement, err := tx.PrepareContext(ctx, "INSERT INTO "+table.name+" ("+table.columns+") VALUES ("+strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")+")")
		if err != nil {
			rows.Close()
			return nil, err
		}
		copyErr := func() error {
			defer rows.Close()
			defer statement.Close()
			for rows.Next() {
				values := make([]any, len(columns))
				pointers := make([]any, len(columns))
				for n := range values {
					pointers[n] = &values[n]
				}
				if err := rows.Scan(pointers...); err != nil {
					return err
				}
				for n, value := range values {
					if instant, ok := value.(time.Time); ok {
						values[n] = instant.UTC()
					}
				}
				if _, err := statement.ExecContext(ctx, values...); err != nil {
					return err
				}
				counts[table.name]++
			}
			return rows.Err()
		}()
		if copyErr != nil {
			return nil, fmt.Errorf("copy %s: %w", table.name, copyErr)
		}
	}
	// Preserve sequence high-water marks even if the highest record was deleted.
	for _, sequence := range []struct{ table, name string }{{"private_ssh_keys", "private_ssh_key_ids"}, {"events", "event_ids"}} {
		var value sql.NullInt64
		if err := snapshot.QueryRowContext(ctx, "SELECT last_value FROM duckdb_sequences() WHERE sequence_name=?", sequence.name).Scan(&value); err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if value.Valid {
			if _, err := tx.ExecContext(ctx, "DELETE FROM sqlite_sequence WHERE name=?", sequence.table); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO sqlite_sequence(name,seq) VALUES (?,?)", sequence.table, value.Int64); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return nil, err
	}
	if err := store.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := syncImportFile(stage); err != nil {
		return nil, err
	}
	directory, err := os.Open(filepath.Dir(destination))
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// Publish complete, synced files without overwriting anything, key first.
	if len(key) > 0 {
		if err := os.WriteFile(stage+".credentials.key", key, 0600); err != nil {
			return nil, err
		}
		if err := syncImportFile(stage + ".credentials.key"); err != nil {
			return nil, err
		}
		if err := os.Link(stage+".credentials.key", destination+".credentials.key"); err != nil {
			return nil, err
		}
		// Make the key's directory entry durable before publishing the database.
		if err := directory.Sync(); err != nil {
			_ = os.Remove(destination + ".credentials.key")
			return nil, err
		}
	}
	if err := os.Link(stage, destination); err != nil {
		if len(key) > 0 {
			_ = os.Remove(destination + ".credentials.key")
		}
		return nil, err
	}
	if err := directory.Sync(); err != nil {
		return nil, err
	}
	return counts, nil
}

func syncImportFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
