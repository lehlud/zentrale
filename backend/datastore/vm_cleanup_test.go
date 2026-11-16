package datastore

import (
	"path/filepath"
	"testing"
)

func TestDeletionOutboxIsAtomicAndSurvivesDatabaseReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		kind, id, parent string
		value            any
	}{
		{"machine", "host", "", map[string]any{"id": "host", "host": "example.invalid"}},
		{"agent", "main", "project", map[string]any{"id": "main", "projectId": "project"}},
		{"microvm", "main:host", "main", map[string]any{"agentId": "main", "projectId": "project", "machineId": "host", "workspace": "/original"}},
	} {
		if err := store.Save(item.kind, item.id, item.parent, item.value); err != nil {
			t.Fatal(err)
		}
	}
	// Failure after queueing and resource removal must roll both back.
	if _, err := store.db.Exec("DROP TABLE events"); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAgents([]string{"main"}); err == nil {
		t.Fatal("expected failed transaction")
	}
	pending, err := store.PendingVMCleanup()
	if err != nil || len(pending) != 0 {
		t.Fatal("outbox escaped rollback", pending, err)
	}
	if err := store.Load("agent", "main", &map[string]any{}); err != nil {
		t.Fatal("agent escaped rollback", err)
	}
	if err := initializeSchema(store.db); err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveAgents([]string{"main"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pending, err = store.PendingVMCleanup()
	if err != nil || len(pending) != 1 || pending[0]["ownerId"] != "main" || pending[0]["targetProjectId"] != "project" || pending[0]["targetConfiguration"] == nil {
		t.Fatal("durable cleanup lost", pending, err)
	}
	if pending[0]["agentId"] != nil || pending[0]["projectId"] != nil {
		t.Fatal("cleanup still belongs to deleted records", pending)
	}
}
