package datastore

import (
	"reflect"
	"testing"
)

type typedQueryItem struct {
	ID      int64   `db:"id"`
	Label   string  `db:"label"`
	Note    *string `db:"note"`
	Payload []byte  `db:"payload"`
	Active  bool    `db:"active"`
}

func TestTypedInsertResults(t *testing.T) {
	i := newQueryTestInstance(t)
	payload := []byte{0, 1, 255}
	row, err := i.insertOne[typedQueryItem]("items", map[string]any{
		"label": "typed", "payload": payload,
	})
	want := typedQueryItem{ID: 1, Label: "typed", Payload: payload, Active: true}
	if err != nil || !reflect.DeepEqual(row, want) {
		t.Fatalf("struct insert result: row=%#v err=%v", row, err)
	}

	note := "only the note was supplied"
	pointer, err := i.insertOne[*typedQueryItem]("items", struct {
		Note string `db:"note"`
	}{Note: note})
	want = typedQueryItem{ID: 2, Label: "default label", Note: &note, Active: true}
	if err != nil || pointer == nil || !reflect.DeepEqual(*pointer, want) {
		t.Fatalf("pointer insert result: row=%#v err=%v", pointer, err)
	}

	rows, err := i.insertMany[typedQueryItem]("items", []struct{ Label string }{
		{Label: "first batch row"},
		{Label: "second batch row"},
	})
	wantRows := []typedQueryItem{
		{ID: 3, Label: "first batch row", Active: true},
		{ID: 4, Label: "second batch row", Active: true},
	}
	if err != nil || !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("typed batch results: rows=%#v err=%v", rows, err)
	}

	pointers, err := i.insertMany[*typedQueryItem]("items", []map[string]any{{}})
	want = typedQueryItem{ID: 5, Label: "default label", Active: true}
	if err != nil || len(pointers) != 1 || pointers[0] == nil ||
		!reflect.DeepEqual(*pointers[0], want) {
		t.Fatalf("pointer batch results: rows=%#v err=%v", pointers, err)
	}

	empty, err := i.insertMany[typedQueryItem]("items", []map[string]any(nil))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty typed batch: rows=%#v err=%v", empty, err)
	}
}

func TestTypedUpdateResults(t *testing.T) {
	i := newQueryTestInstance(t)
	payload := []byte{1, 2, 3}
	_, err := i.insertMany[typedQueryItem]("items", []map[string]any{
		{"label": "first", "note": "old note", "payload": payload},
		{"label": "second"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := i.update[typedQueryItem]("items", map[string]any{
		"note": nil, "active": false,
	}, "label = ?", "first")
	want := []typedQueryItem{{ID: 1, Label: "first", Payload: payload, Active: false}}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatalf("struct update results: rows=%#v err=%v", rows, err)
	}

	note := "new note"
	pointers, err := i.update[*typedQueryItem]("items", &struct {
		Note string `db:"note"`
	}{Note: note}, "id = ?", 2)
	wantPointer := typedQueryItem{ID: 2, Label: "second", Note: &note, Active: true}
	if err != nil || len(pointers) != 1 || pointers[0] == nil ||
		!reflect.DeepEqual(*pointers[0], wantPointer) {
		t.Fatalf("pointer update results: rows=%#v err=%v", pointers, err)
	}

	stored, err := i.query[typedQueryItem]("SELECT * FROM items WHERE id = ?", 2)
	if err != nil || !reflect.DeepEqual(stored, wantPointer) {
		t.Fatalf("update was not persisted: row=%#v err=%v", stored, err)
	}

	empty, err := i.update[typedQueryItem]("items", map[string]any{"note": nil}, "FALSE")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("no matching typed update: rows=%#v err=%v", empty, err)
	}
}

func TestMutationDecodeFailuresRollBack(t *testing.T) {
	i := newQueryTestInstance(t)
	before, err := i.insertMany[typedQueryItem]("items", []map[string]any{
		{"label": "keep first"},
		{"label": "keep second", "note": "not a number"},
	})
	if err != nil {
		t.Fatal(err)
	}

	type numericLabel struct {
		ID    int64 `db:"id"`
		Label int   `db:"label"`
	}
	row, err := i.insertOne[numericLabel]("items", map[string]any{"label": "not a number"})
	if err == nil || row != (numericLabel{}) {
		t.Fatalf("failed struct decode returned a partial row: row=%#v err=%v", row, err)
	}

	pointer, err := i.insertOne[*numericLabel]("items", map[string]any{"label": "not a number"})
	if err == nil || pointer != nil {
		t.Fatalf("failed pointer decode returned a partial row: row=%#v err=%v", pointer, err)
	}

	type numericNote struct {
		ID   int64 `db:"id"`
		Note int   `db:"note"`
	}
	// The NULL note decodes successfully; the second row must roll back both.
	rows, err := i.insertMany[numericNote]("items", []map[string]any{
		{"label": "must roll back first"},
		{"label": "must roll back second", "note": "not a number"},
	})
	if err == nil || rows != nil {
		t.Fatalf("expected a batch decoding failure: rows=%#v err=%v", rows, err)
	}

	updated, err := i.update[numericNote]("items", map[string]any{
		"label": "must roll back", "active": false,
	}, "TRUE")
	if err == nil || updated != nil {
		t.Fatalf("expected an update decoding failure: rows=%#v err=%v", updated, err)
	}

	// Database failures must also release the transaction and its connection.
	updated, err = i.update[numericNote]("items", map[string]any{"id": int64(1)}, "TRUE")
	if err == nil || updated != nil {
		t.Fatalf("expected an update constraint failure: rows=%#v err=%v", updated, err)
	}

	after, err := i.query[[]typedQueryItem]("SELECT * FROM items ORDER BY id")
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatalf("failed mutations changed stored rows: rows=%#v err=%v", after, err)
	}
}
