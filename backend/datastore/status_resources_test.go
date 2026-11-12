package datastore

import (
	"fmt"
	"strings"
	"testing"
)

func TestStatusProjectionIsScopedBoundedAndOmitsOutput(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for n := range 105 {
		id := fmt.Sprintf("job-%03d", n)
		if err := store.Save("job", id, "main", map[string]any{"id": id, "status": "running", "output": strings.Repeat("secret-output", 1000)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct{ kind, id, parent, status string }{{"job", "old", "main", "completed"}, {"job", "foreign", "other", "running"}, {"question", "question", "main", "running"}} {
		if err := store.Save(row.kind, row.id, row.parent, map[string]any{"id": row.id, "status": row.status}); err != nil {
			t.Fatal(err)
		}
	}
	fields := []string{"id", "status"}
	rows, err := store.ListStatusFields("job", "main", "running", 5000, 0, fields)
	if err != nil || len(rows) != 100 {
		t.Fatalf("default bounded result: %d %v", len(rows), err)
	}
	for _, row := range rows {
		if len(row) != 2 || row["status"] != "running" {
			t.Fatalf("projection leaked fields: %v", row)
		}
	}
	tail, err := store.ListStatusFields("job", "main", "running", 100, 100, fields)
	if err != nil || len(tail) != 5 {
		t.Fatalf("offset: %d %v", len(tail), err)
	}
	if _, err := store.ListStatusFields("job", "main", "running", 100, -1, fields); err == nil {
		t.Fatal("negative offset accepted")
	}
	rows, err = store.ListStatusFields("job", "main", "running' OR TRUE --", 100, 0, fields)
	if err != nil || len(rows) != 0 {
		t.Fatalf("status must be a bound parameter: %v %v", rows, err)
	}
}
