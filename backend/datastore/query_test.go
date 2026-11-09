package datastore

import (
	"bytes"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"
)

func newQueryTestInstance(t *testing.T) *Instance {
	t.Helper()

	// Query tests own an independent schema rather than application tables.
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}

	// A leaked result set or transaction would block the next operation.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	_, err = db.Exec(`
		CREATE SEQUENCE item_ids START 1;
		CREATE TABLE items (
			id BIGINT PRIMARY KEY DEFAULT nextval('item_ids'),
			label VARCHAR DEFAULT 'default label',
			note VARCHAR,
			payload BLOB,
			active BOOLEAN DEFAULT true
		);
	`)
	if err != nil {
		t.Fatal(err)
	}

	return &Instance{db: db}
}

func TestInsertOne(t *testing.T) {
	i := newQueryTestInstance(t)
	type item struct {
		ID      int64  `db:"id,omitempty"`
		Label   string `db:"label"`
		Payload []byte `db:"payload"`
		Ignored string `db:"-"`
		hidden  string
	}

	payload := []byte{0, 1, 255}
	row, err := i.insertOne[map[string]any]("main.items", &item{
		Label:   "O'Reilly'); DELETE FROM items; --",
		Payload: payload,
		Ignored: "not a column",
		hidden:  "not exported",
	})
	if err != nil {
		t.Fatal(err)
	}

	if row == nil || row["id"] != int64(1) || row["active"] != true || row["note"] != nil {
		t.Fatalf("missing generated/default values: %#v", row)
	}

	if row["label"] != "O'Reilly'); DELETE FROM items; --" {
		t.Fatalf("label was not preserved: %#v", row)
	}

	if blob, ok := row["payload"].([]byte); !ok || !bytes.Equal(blob, payload) {
		t.Fatalf("blob was not preserved: %#v", row["payload"])
	}

	defaults, err := i.insertOne[map[string]any]("items", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	if defaults == nil || defaults["label"] != "default label" {
		t.Fatalf("DEFAULT VALUES was not returned: %#v", defaults)
	}

	// Untagged exported fields use their Go names, matched case-insensitively by SQL.
	untagged, err := i.insertOne[map[string]any]("items", struct{ Label string }{Label: "untagged"})
	if err != nil || untagged["label"] != "untagged" {
		t.Fatalf("untagged insert: row=%#v err=%v", untagged, err)
	}
}

func TestInsertMany(t *testing.T) {
	i := newQueryTestInstance(t)
	rows, err := i.insertMany[map[string]any]("items", []map[string]any{
		{"label": "first"},
		{"note": "different columns", "label": "second"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 || rows[0]["label"] != "first" || rows[1]["label"] != "second" {
		t.Fatalf("unexpected rows or order: %#v", rows)
	}

	if rows[0]["note"] != nil || rows[1]["note"] != "different columns" {
		t.Fatalf("column values were misaligned: %#v", rows)
	}

	empty, err := i.insertMany[map[string]any]("items", []map[string]any(nil))
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty insert: rows=%#v err=%v", empty, err)
	}
}

func TestInsertManyRollsBack(t *testing.T) {
	i := newQueryTestInstance(t)
	_, err := i.insertOne[map[string]any]("items", map[string]any{"id": int64(10), "label": "existing"})
	if err != nil {
		t.Fatal(err)
	}

	rows, err := i.insertMany[map[string]any]("items", []map[string]any{
		{"id": int64(11), "label": "must roll back"},
		{"id": int64(10), "label": "duplicate primary key"},
	})
	if err == nil || rows != nil {
		t.Fatalf("expected nil rows and a constraint error: rows=%#v err=%v", rows, err)
	}

	rows, err = i.insertMany[map[string]any]("items", []any{
		map[string]any{"label": "must also roll back"},
		42,
	})
	if err == nil || rows != nil {
		t.Fatalf("expected nil rows and a validation error: rows=%#v err=%v", rows, err)
	}

	remaining, err := i.query[[]map[string]any]("SELECT * FROM items")
	if err != nil || len(remaining) != 1 || remaining[0]["label"] != "existing" {
		t.Fatalf("batch was not rolled back: rows=%#v err=%v", remaining, err)
	}
}

func TestQuery(t *testing.T) {
	i := newQueryTestInstance(t)
	_, err := i.insertMany[map[string]any]("items", []map[string]any{
		{"label": "first", "payload": []byte{1, 2}},
		{"label": "second"},
	})
	if err != nil {
		t.Fatal(err)
	}

	type item struct {
		ID      int64 `db:"id"`
		Label   string
		Note    *string `db:"note"`
		Payload []byte  `db:"payload"`
		Active  bool    `db:"active"`
	}

	one, err := i.query[item]("SELECT * FROM items WHERE label = ?", "first")
	if err != nil {
		t.Fatal(err)
	}

	if one.ID != 1 || one.Label != "first" || one.Note != nil || !one.Active ||
		!bytes.Equal(one.Payload, []byte{1, 2}) {
		t.Fatalf("unexpected decoded struct: %#v", one)
	}

	pointer, err := i.query[*item]("SELECT * FROM items WHERE id = ?", 1)
	if err != nil || pointer == nil || !reflect.DeepEqual(*pointer, one) {
		t.Fatalf("pointer query: row=%#v err=%v", pointer, err)
	}

	many, err := i.query[[]item]("SELECT * FROM items ORDER BY id")
	if err != nil || len(many) != 2 || many[1].Label != "second" {
		t.Fatalf("slice query: rows=%#v err=%v", many, err)
	}

	row, err := i.query[map[string]any]("SELECT * FROM items WHERE id = ?", 2)
	if err != nil || row == nil || row["label"] != "second" {
		t.Fatalf("map query: row=%#v err=%v", row, err)
	}

	empty, err := i.query[[]item]("SELECT * FROM items WHERE false")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty query: rows=%#v err=%v", empty, err)
	}

	missing, err := i.query[map[string]any]("SELECT * FROM items WHERE false")
	if !errors.Is(err, sql.ErrNoRows) || missing != nil {
		t.Fatalf("expected sql.ErrNoRows: row=%#v err=%v", missing, err)
	}

	multiple, err := i.query[map[string]any]("SELECT * FROM items")
	if err == nil || multiple != nil {
		t.Fatalf("expected a cardinality error: row=%#v err=%v", multiple, err)
	}

	invalid, err := i.query[int]("SELECT label FROM items WHERE id = 1")
	if err == nil || invalid != 0 {
		t.Fatalf("expected a decoding error: row=%#v err=%v", invalid, err)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	i := newQueryTestInstance(t)
	_, err := i.insertMany[map[string]any]("items", []map[string]any{
		{"label": "first"},
		{"label": "second"},
		{"label": "third"},
	})
	if err != nil {
		t.Fatal(err)
	}

	note := "bound value'); DELETE FROM items; --"
	updated, err := i.update[map[string]any]("items", &struct {
		Note   string `db:"note"`
		Active bool   `db:"active"`
	}{Note: note}, "id IN (?, ?)", 1, 2)
	if err != nil {
		t.Fatal(err)
	}

	if len(updated) != 2 {
		t.Fatalf("expected two updated rows: %#v", updated)
	}

	for _, row := range updated {
		if row["note"] != note || row["active"] != false || row["label"] == "third" {
			t.Fatalf("unexpected updated row: %#v", row)
		}
	}

	deleted, err := i.delete("main.items", "note = ?", note)
	if err != nil || len(deleted) != 2 {
		t.Fatalf("delete: rows=%#v err=%v", deleted, err)
	}

	for _, row := range deleted {
		if row["note"] != note {
			t.Fatalf("unexpected deleted row: %#v", row)
		}
	}

	remaining, err := i.query[map[string]any]("SELECT * FROM items")
	if err != nil || remaining["label"] != "third" || remaining["active"] != true {
		t.Fatalf("unexpected remaining row: row=%#v err=%v", remaining, err)
	}

	noUpdates, err := i.update[map[string]any]("items", map[string]any{"note": nil}, "id = ?", -1)
	if err != nil || noUpdates == nil || len(noUpdates) != 0 {
		t.Fatalf("no matching update: rows=%#v err=%v", noUpdates, err)
	}

	noDeletes, err := i.delete("items", "id = ?", -1)
	if err != nil || noDeletes == nil || len(noDeletes) != 0 {
		t.Fatalf("no matching delete: rows=%#v err=%v", noDeletes, err)
	}

	_, err = i.update[map[string]any]("items", map[string]any{"note": nil}, "TRUE")
	if err != nil {
		t.Fatal(err)
	}

	all, err := i.delete("items", "TRUE")
	if err != nil || len(all) != 1 || all[0]["note"] != nil {
		t.Fatalf("explicit full-table mutation: rows=%#v err=%v", all, err)
	}
}

func TestQuotedIdentifiersAndDriverValues(t *testing.T) {
	i := newQueryTestInstance(t)
	_, err := i.db.Exec(`CREATE TABLE "odd"" table" ("select" VARCHAR, "co""lumn" TIMESTAMPTZ)`)
	if err != nil {
		t.Fatal(err)
	}

	stamp := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	row, err := i.insertOne[map[string]any](`odd" table`, map[string]any{
		"select":  sql.NullString{String: "a quoted value", Valid: true},
		`co"lumn`: stamp,
	})
	if err != nil || row["select"] != "a quoted value" {
		t.Fatalf("quoted identifiers: row=%#v err=%v", row, err)
	}

	if got, ok := row[`co"lumn`].(time.Time); !ok || !got.Equal(stamp) {
		t.Fatalf("timestamp was not preserved: %#v", row)
	}

	updated, err := i.update[map[string]any](`odd" table`, map[string]any{
		"select": nil,
	}, `"co""lumn" = ?`, stamp)
	if err != nil || len(updated) != 1 || updated[0]["select"] != nil {
		t.Fatalf("quoted update: rows=%#v err=%v", updated, err)
	}

	deleted, err := i.delete(`odd" table`, `"select" IS NULL`)
	if err != nil || len(deleted) != 1 {
		t.Fatalf("quoted delete: rows=%#v err=%v", deleted, err)
	}
}

func TestInvalidInputs(t *testing.T) {
	i := newQueryTestInstance(t)
	var nilPointer *struct{ Label string }
	var nilMap map[string]any
	invalidRows := []any{
		nil,
		nilPointer,
		nilMap,
		42,
		[]string{"not a row"},
		map[int]string{1: "not a string key"},
		map[string]any{"": 1},
		map[string]any{"bad\x00column": 1},
		struct {
			First  string `db:"label"`
			Second string `db:"label"`
		}{},
	}

	for index, input := range invalidRows {
		row, err := i.insertOne[map[string]any]("items", input)
		if err == nil || row != nil {
			t.Fatalf("invalid insert %d: row=%#v err=%v", index, row, err)
		}

		rows, err := i.update[map[string]any]("items", input, "TRUE")
		if err == nil || rows != nil {
			t.Fatalf("invalid update %d: rows=%#v err=%v", index, rows, err)
		}
	}

	for _, table := range []string{"", "main.", "bad\x00table", "missing_table"} {
		row, err := i.insertOne[map[string]any](table, map[string]any{"label": "invalid"})
		if err == nil || row != nil {
			t.Fatalf("invalid table %q: row=%#v err=%v", table, row, err)
		}
	}

	for _, predicate := range []string{"", " \n\t"} {
		updated, err := i.update[map[string]any]("items", map[string]any{"label": "invalid"}, predicate)
		if err == nil || updated != nil {
			t.Fatalf("empty update predicate: rows=%#v err=%v", updated, err)
		}

		deleted, err := i.delete("items", predicate)
		if err == nil || deleted != nil {
			t.Fatalf("empty delete predicate: rows=%#v err=%v", deleted, err)
		}
	}

	updated, err := i.update[map[string]any]("items", map[string]any{}, "TRUE")
	if err == nil || updated != nil {
		t.Fatalf("empty update fields: rows=%#v err=%v", updated, err)
	}

	for _, statement := range []string{"SELECT * FROM missing_table", "not valid SQL"} {
		row, err := i.query[map[string]any](statement)
		if err == nil || row != nil {
			t.Fatalf("invalid query: row=%#v err=%v", row, err)
		}
	}

	deleted, err := i.delete("missing_table", "TRUE")
	if err == nil || deleted != nil {
		t.Fatalf("invalid delete: rows=%#v err=%v", deleted, err)
	}

	remaining, err := i.query[[]map[string]any]("SELECT * FROM items")
	if err != nil || len(remaining) != 0 {
		t.Fatalf("invalid operations changed data: rows=%#v err=%v", remaining, err)
	}
}

func TestClosedInstance(t *testing.T) {
	closed := newQueryTestInstance(t)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	for _, i := range []*Instance{nil, {}, closed} {
		row, err := i.query[map[string]any]("SELECT 1")
		if !errors.Is(err, errClosed) || row != nil {
			t.Fatalf("closed query: row=%#v err=%v", row, err)
		}

		row, err = i.insertOne[map[string]any]("items", map[string]any{})
		if !errors.Is(err, errClosed) || row != nil {
			t.Fatalf("closed insert: row=%#v err=%v", row, err)
		}

		rows, err := i.insertMany[map[string]any]("items", []map[string]any(nil))
		if !errors.Is(err, errClosed) || rows != nil {
			t.Fatalf("closed batch insert: rows=%#v err=%v", rows, err)
		}

		rows, err = i.update[map[string]any]("items", map[string]any{"label": "invalid"}, "TRUE")
		if !errors.Is(err, errClosed) || rows != nil {
			t.Fatalf("closed update: rows=%#v err=%v", rows, err)
		}

		rows, err = i.delete("items", "TRUE")
		if !errors.Is(err, errClosed) || rows != nil {
			t.Fatalf("closed delete: rows=%#v err=%v", rows, err)
		}
	}
}
