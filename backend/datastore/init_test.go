package datastore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCloseIsConcurrentAndIdempotent(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.CreateSession("active", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	var workers sync.WaitGroup

	for range 8 {
		workers.Go(func() {
			<-start

			for range 100 {
				_ = store.Closed()
				_, _ = store.SessionValid("active")
			}
		})
	}

	for range 4 {
		workers.Go(func() {
			<-start

			if err := store.Close(); err != nil {
				t.Error(err)
			}
		})
	}

	close(start)
	workers.Wait()

	if !store.Closed() {
		t.Fatal("closed store still reports open")
	}

	if valid, err := store.SessionValid("active"); valid || err == nil {
		t.Fatal("closed store accepted a session", valid, err)
	}

	if err := store.Close(); err != nil {
		t.Fatal("repeated close failed", err)
	}
}

func TestNilAndZeroStoresAreClosed(t *testing.T) {
	for _, store := range []*Instance{nil, {}} {
		if !store.Closed() {
			t.Fatal("uninitialized store reports open")
		}

		if err := store.Close(); err != nil {
			t.Fatal("closing uninitialized store failed", err)
		}
	}
}

func TestConcurrentProcessesShareSQLiteWithoutExclusiveFileLock(t *testing.T) {
	const variable = "ZENTRALE_TEST_SQLITE_DATABASE"
	if path := os.Getenv(variable); path != "" {
		store, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()
		if value, err := store.Setting("work"); err != nil || value != "preserved" {
			t.Fatal(value, err)
		}
		if err := store.SetSetting("child", "written"); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetSetting("work", "preserved"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestConcurrentProcessesShareSQLiteWithoutExclusiveFileLock$")
	process.Env = append(os.Environ(), variable+"="+path)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("SQLite child process: %v\n%s", err, output)
	}
	if value, err := store.Setting("child"); err != nil || value != "written" {
		t.Fatal(value, err)
	}
}
