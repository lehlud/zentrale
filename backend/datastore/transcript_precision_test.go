package datastore

import (
	"testing"
	"time"
)

func TestTranscriptOrdersNanosecondsAndTimezonesAcrossCursorPages(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, item := range []struct{ id, instant string }{{"z-old", "2026-01-01T00:00:00.123456100Z"}, {"a-new", "2026-01-01T00:00:00.123456300Z"}} {
		instant, err := time.Parse(time.RFC3339Nano, item.instant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO resources(kind,id,parent_id,data,created_at) VALUES ('message',?,'agent',json_object('id',?),?)`, item.id, item.id, instant); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save("goal_history", "goal", "agent", map[string]any{"id": "goal", "status": "complete", "updatedAt": "2026-01-01T02:00:00.123456200+02:00"}); err != nil {
		t.Fatal(err)
	}
	first, err := store.TranscriptBefore("agent", 2, "")
	if err != nil || len(first) != 2 || first[0]["id"] != "a-new" || first[1]["id"] != "goal:goal" {
		t.Fatal("timestamp precision lost", first, err)
	}
	second, err := store.TranscriptBefore("agent", 2, "goal:goal")
	if err != nil || len(second) != 1 || second[0]["id"] != "z-old" {
		t.Fatal("cursor lost or reordered a message", second, err)
	}
}
