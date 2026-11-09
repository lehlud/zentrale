package datastore

import (
	"reflect"
	"testing"
)

func TestResourceBatchBindsIDsAndProjectsFields(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for _, id := range []string{"one", "quote' OR TRUE --"} {
		if err := store.Save(
			"file_change",
			id,
			"agent",
			map[string]any{
				"id":    id,
				"path":  id + ".py",
				"patch": "private content",
			},
		); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := store.ResourcesByID("file_change", []string{
		"quote' OR TRUE --",
		"missing",
	}, []string{
		"path",
	})
	if err != nil || !reflect.DeepEqual(rows, []map[string]any{{"path": "quote' OR TRUE --.py"}}) {
		t.Fatalf("bound lookup or field projection failed: %v %v", rows, err)
	}

	if _, err := store.ResourcesByID("file_change", make([]string, 501), nil); err == nil {
		t.Fatal("unbounded batch accepted")
	}
}
