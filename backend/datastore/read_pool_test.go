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

func TestConcurrentReadsAndWriterShareOneWALPool(t *testing.T) {
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
			// Retain one connection, just as an active transaction does.
			writer, err := store.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if store.readDB() != store.db || store.db.Stats().MaxOpenConnections != 16 {
				t.Fatal("the application must use a normal multi-connection pool")
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
			// Several readers can retain connections while a writer progresses.
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
				rows, err := store.readDB().Query("SELECT "+projection+" FROM resources WHERE kind='agent' AND parent_id='p' ORDER BY created_at,id LIMIT 500", args...)
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

// Measure selective ID/parent lookup plans and the effect of additional indexes.
func BenchmarkResourceLookupIndexes(b *testing.B) {
	store, err := Open("")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`INSERT INTO resources SELECT 'message', 'm'||n, 'agent-'||(n%1000), json_object('id','m'||n,'content','x'), CURRENT_TIMESTAMP FROM (WITH RECURSIVE numbers(n) AS (SELECT 0 UNION ALL SELECT n+1 FROM numbers WHERE n<99999) SELECT n FROM numbers) t`); err != nil {
		b.Fatal(err)
	}
	for _, indexed := range []bool{false, true} {
		if indexed {
			if _, err := store.db.Exec(`CREATE INDEX lookup_id ON resources(id); CREATE INDEX lookup_parent ON resources(parent_id)`); err != nil {
				b.Fatal(err)
			}
		}
		for _, item := range []struct{ name, query string }{
			{"id", `SELECT data FROM resources WHERE kind='message' AND id='m4242'`},
			{"parent", `SELECT id FROM resources WHERE kind='message' AND parent_id='agent-242' ORDER BY created_at,id LIMIT 100`},
		} {
			b.Run(fmt.Sprintf("%s/indexed=%v", item.name, indexed), func(b *testing.B) {
				var key int
				var parent, unused int
				var plan string
				if err := store.readDB().QueryRow("EXPLAIN QUERY PLAN "+item.query).Scan(&key, &parent, &unused, &plan); err != nil {
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
