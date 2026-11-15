package datastore

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestQueryResources(t *testing.T) {
	instance := newQueryTestInstance(t)
	for _, test := range []struct {
		name      string
		statement string
		arguments []any
		want      []map[string]any
		wantError bool
	}{
		{
			name:      "bound JSON in query order",
			statement: "SELECT ? UNION ALL SELECT ?",
			arguments: []any{`{"id":"second's"}`, `{"id":"first"}`},
			want:      []map[string]any{{"id": "second's"}, {"id": "first"}},
		},
		{
			name:      "empty collection is non-nil",
			statement: "SELECT '{}' WHERE FALSE",
			want:      []map[string]any{},
		},
		{
			name:      "invalid JSON discards earlier rows",
			statement: "SELECT ? UNION ALL SELECT ?",
			arguments: []any{`{"id":"valid"}`, `not JSON`},
			wantError: true,
		},
		{
			name:      "scan failure discards earlier rows",
			statement: "SELECT '{}' UNION ALL SELECT NULL",
			wantError: true,
		},
		{
			name:      "query failure",
			statement: "SELECT data FROM missing_table",
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := instance.queryResources(test.statement, test.arguments...)
			if (err != nil) != test.wantError || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("queryResources = %#v, %v; want %#v, error %v", got, err, test.want, test.wantError)
			}

			// The fixture has only one connection. A leaked result set would keep
			// the next query waiting until its context expires.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var count int
			if err := instance.db.QueryRowContext(ctx, "SELECT count(*) FROM items").Scan(&count); err != nil {
				t.Fatalf("resource query did not release its connection: %v", err)
			}
		})
	}
}

func TestQueryResourcesRejectsClosedDatastore(t *testing.T) {
	closed, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}

	for _, instance := range []*Instance{nil, closed} {
		rows, err := instance.queryResources("SELECT '{}'")
		if rows != nil || !errors.Is(err, ErrClosed) {
			t.Fatalf("closed datastore returned %#v, %v", rows, err)
		}
	}
}

func TestResourceIDsRemainOpaqueAcrossIDFormats(t *testing.T) {
	instance, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer instance.Close()
	for _, id := range []string{
		"H7RFILKASP7V4WJPCAHSMWSQWV",
		"4c83bab1-fe61-4f7d-99f9-aa5b2a3be492",
		domain.NewID(),
	} {
		want := map[string]any{"id": id, "title": "Preserved reference"}
		if err := instance.Save("note", id, "agent", want); err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := instance.Load("note", id, &got); err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("resource ID changed: got=%v want=%v error=%v", got, want, err)
		}
	}
}
