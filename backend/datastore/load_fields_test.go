package datastore

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestLoadFieldsProjectsAndPreservesLookupSemantics(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save("agent", "one", "project", map[string]any{"id": "one", "name": "Name", "summary": strings.Repeat("large", 16384)}); err != nil {
		t.Fatal(err)
	}
	var compact map[string]any
	if err := store.LoadFields("agent", "one", &compact, []string{"id", "name"}); err != nil {
		t.Fatal(err)
	}
	if len(compact) != 2 || compact["id"] != "one" || compact["name"] != "Name" {
		t.Fatal(compact)
	}
	var full map[string]any
	if err := store.Load("agent", "one", &full); err != nil {
		t.Fatal(err)
	}
	if full["summary"] != strings.Repeat("large", 16384) {
		t.Fatal("Load compatibility lost")
	}
	var empty map[string]any
	if err := store.LoadFields("agent", "one", &empty, []string{}); err != nil || len(empty) != 0 {
		t.Fatal(empty, err)
	}
	for _, id := range []string{"missing", "one' OR true --"} {
		if err := store.LoadFields("agent", id, &empty, []string{"id"}); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("lookup escaped scope %q: %v", id, err)
		}
	}
	var absent *Instance
	if err := absent.LoadFields("agent", "one", &empty, []string{"id"}); !errors.Is(err, errClosed) {
		t.Fatal(err)
	}
}
