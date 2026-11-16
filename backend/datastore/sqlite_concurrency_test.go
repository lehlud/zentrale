package datastore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWALReadersRetainSnapshotsWhileWriterCommits(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetSetting("snapshot", "before"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ready := make(chan error, 4)
	release := make(chan struct{})
	defer close(release)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			rows, err := store.db.QueryContext(ctx, "SELECT value FROM settings WHERE name='snapshot'")
			if err != nil {
				ready <- err
				return
			}
			defer rows.Close()
			var value string
			if !rows.Next() {
				ready <- fmt.Errorf("reader has no row: %v", rows.Err())
				return
			}
			if err := rows.Scan(&value); err != nil {
				ready <- err
				return
			}
			if value != "before" {
				ready <- fmt.Errorf("unexpected snapshot: %s", value)
				return
			}
			ready <- nil
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}
	for range 4 {
		select {
		case err := <-ready:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("readers did not run concurrently")
		}
	}
	if store.db.Stats().InUse != 4 {
		t.Fatal("expected four simultaneous readers", store.db.Stats())
	}
	if err := store.SetSetting("snapshot", "after"); err != nil {
		t.Fatal("writer blocked behind open readers", err)
	}
	value, err := store.Setting("snapshot")
	if err != nil || value != "after" {
		t.Fatal(value, err)
	}
	// Cleanup must release rows before database/sql.Close waits for them.
	cancel()
	readers.Wait()
}

func TestWALConcurrentWritersDoNotLoseUpdates(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetSetting("counter", "0"); err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		writers.Go(func() {
			for range 20 {
				if _, err := store.db.Exec("UPDATE settings SET value=CAST(value AS INTEGER)+1 WHERE name='counter'"); err != nil {
					failures <- err
					return
				}
			}
		})
	}
	writers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	value, err := store.Setting("counter")
	if err != nil || value != "240" {
		t.Fatal("lost concurrent updates", value, err)
	}
}

func TestOpenRefusesDuckDBAndAccidentalEmptyReplacement(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "zentrale.duckdb")
	original := []byte("not an SQLite database; must remain untouched")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	if store, err := Open(source); err == nil {
		store.Close()
		t.Fatal("accepted DuckDB as SQLite")
	}
	if store, err := Open(filepath.Join(directory, "zentrale.sqlite")); err == nil {
		store.Close()
		t.Fatal("silently started empty alongside existing DuckDB")
	}
	actual, err := os.ReadFile(source)
	if err != nil || string(actual) != string(original) {
		t.Fatal("modified original", err)
	}
}
