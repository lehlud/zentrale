package datastore

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestReadsDoNotQueueBehindWriterAndShareDatabase(t *testing.T) {
	for _, disk := range []bool{false, true} {
		t.Run(fmt.Sprint("disk=", disk), func(t *testing.T) {
			path := ""
			if disk {
				path = filepath.Join(t.TempDir(), "reads.duckdb")
			}
			store, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.Save("project", "one", "", map[string]any{"id": "one", "name": "Project"}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			// Retain the sole writer connection, just as an active agent's
			// transaction does. A read through the old pool times out here.
			writer, err := store.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if store.db.Stats().MaxOpenConnections != 1 {
				t.Fatal("writes must remain serialized")
			}
			var name string
			if err := store.readDB().QueryRowContext(ctx, `SELECT data->>'name' FROM resources WHERE kind='project' AND id='one'`).Scan(&name); err != nil || name != "Project" {
				t.Fatalf("read queued behind writer, or opened another database: %q %v", name, err)
			}
			read := make(chan error, 1)
			go func() {
				var row map[string]any
				if err := store.LoadFields("project", "one", &row, []string{"id", "name"}); err != nil {
					read <- err
					return
				}
				if _, err := store.ListFields("project", nil, 500, 0, []string{"id", "name"}); err != nil {
					read <- err
					return
				}
				if _, err := store.SessionValid("missing"); err != nil {
					read <- err
					return
				}
				_, err := store.TranscriptBeforeFields("agent", 100, "", []string{"id"})
				read <- err
			}()
			select {
			case err := <-read:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("application reads still queued behind writer")
			}
			tx, err := writer.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, `UPDATE resources SET data=json_object('id','one','name','Committed') WHERE kind='project' AND id='one'`); err != nil {
				t.Fatal(err)
			}
			var snapshot map[string]any
			if err := store.Load("project", "one", &snapshot); err != nil || snapshot["name"] != "Project" {
				t.Fatalf("reader observed uncommitted data: %v %v", snapshot, err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := store.Load("project", "one", &snapshot); err != nil || snapshot["name"] != "Committed" {
				t.Fatalf("reader missed committed data: %v %v", snapshot, err)
			}
			// Exhaust the read pool independently; the writer must still make
			// progress (there must not be one shared four-connection pool).
			for range 4 {
				connection, err := store.readDB().Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer connection.Close()
			}
			if _, err := writer.ExecContext(ctx, `INSERT INTO settings VALUES ('progress','yes')`); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSingleParseProjectionPreservesJSONValues(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := map[string]any{"id": "one", "name": "quoted \" name", "nested": map[string]any{"ok": true}, "items": []any{float64(3), nil, "x"}, "nil": nil}
	if err := store.Save("agent", "one", "p", value); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := store.LoadFields("agent", "one", &result, []string{"id", "name", "nested", "items", "nil", "missing", "id"}); err != nil {
		t.Fatal(err)
	}
	value["missing"] = nil
	if !reflect.DeepEqual(value, result) {
		t.Fatalf("projection changed JSON types/nulls: %#v", result)
	}
}

// Compare the actual SQL projections on large hidden summary blobs, not only
// response bytes. The legacy variant intentionally repeats parsing per field.
func BenchmarkResourceProjection(b *testing.B) {
	store, err := Open("")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	fields := []string{"id", "name", "role", "status", "parentId", "approverId", "projectId", "providerId", "modelId", "machineId", "error", "contextStart"}
	writes := []ResourceWrite{}
	for n := range 300 {
		id := fmt.Sprint(n)
		writes = append(writes, ResourceWrite{Kind: "agent", ID: id, Parent: "p", Value: map[string]any{"id": id, "name": "Agent", "role": "child", "summary": strings.Repeat("summary", 16384)}})
	}
	if err := store.SaveResources(writes); err != nil {
		b.Fatal(err)
	}
	for _, legacy := range []bool{true, false} {
		b.Run(fmt.Sprint("legacy=", legacy), func(b *testing.B) {
			projection, args := resourceProjection(fields)
			if legacy {
				parts := []string{}
				args = nil
				for _, field := range fields {
					parts = append(parts, "?,json_extract(data,?)")
					args = append(args, field, "$."+field)
				}
				projection = "json_object(" + strings.Join(parts, ",") + ")"
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rows, err := store.readDB().Query("SELECT "+projection+"::VARCHAR FROM resources WHERE kind='agent' AND parent_id='p' ORDER BY created_at,id LIMIT 500", args...)
				if err != nil {
					b.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var raw string
					if err := rows.Scan(&raw); err != nil {
						b.Fatal(err)
					}
					count++
				}
				err = rows.Err()
				rows.Close()
				if err != nil || count != 300 {
					b.Fatalf("rows=%d error=%v", count, err)
				}
			}
		})
	}
}

// DuckDB ART indexes differ from B-tree indexes in OLTP databases. Measure the
// actual plans for selective ID/parent lookups before adding permanent indexes.
func BenchmarkResourceLookupIndexes(b *testing.B) {
	store, err := Open("")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`INSERT INTO resources SELECT 'message', 'm'||n, 'agent-'||(n%1000), json_object('id','m'||n,'content','x'), now() FROM range(100000) t(n)`); err != nil {
		b.Fatal(err)
	}
	for _, indexed := range []bool{false, true} {
		if indexed {
			if _, err := store.db.Exec(`CREATE INDEX lookup_id ON resources(id); CREATE INDEX lookup_parent ON resources(parent_id)`); err != nil {
				b.Fatal(err)
			}
		}
		for _, item := range []struct{ name, query string }{
			{"id", `SELECT data::VARCHAR FROM resources WHERE kind='message' AND id='m4242'`},
			{"parent", `SELECT id FROM resources WHERE kind='message' AND parent_id='agent-242' ORDER BY created_at,id LIMIT 100`},
		} {
			b.Run(fmt.Sprintf("%s/indexed=%v", item.name, indexed), func(b *testing.B) {
				var key, plan string
				if err := store.readDB().QueryRow("EXPLAIN "+item.query).Scan(&key, &plan); err != nil {
					b.Fatal(err)
				}
				b.Logf("EXPLAIN %s: %s", item.name, plan)
				b.ResetTimer()
				for range b.N {
					rows, err := store.readDB().Query(item.query)
					if err != nil {
						b.Fatal(err)
					}
					count := 0
					for rows.Next() {
						var value string
						if err := rows.Scan(&value); err != nil {
							b.Fatal(err)
						}
						count++
					}
					err = rows.Err()
					rows.Close()
					if err != nil || count == 0 {
						b.Fatalf("count=%d err=%v", count, err)
					}
				}
			})
		}
	}
}
