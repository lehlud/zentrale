package datastore

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestTranscriptFieldsProjectsBeforeDecodeAndPreservesPagination(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	wire := strings.Repeat("private-wire", 8192)
	for index := range 105 {
		id := fmt.Sprintf("m-%03d", index)
		if err := store.Save("message", id, "agent", map[string]any{"id": id, "role": "assistant", "content": id, "wire": wire}); err != nil {
			t.Fatal(err)
		}
	}
	fields := []string{"id", "role", "content"}
	page, err := store.TranscriptBeforeFields("agent", 100, "", fields)
	if err != nil || len(page) != 100 {
		t.Fatalf("page size: %d, %v", len(page), err)
	}
	for _, row := range page {
		if _, loaded := row["wire"]; loaded || len(row) != len(fields) {
			t.Fatal("unrequested wire reached Go decoding")
		}
	}
	older, err := store.TranscriptBeforeFields("agent", 100, page[99]["id"].(string), fields)
	if err != nil || len(older) != 5 {
		t.Fatalf("older page size: %d, %v", len(older), err)
	}
	full, err := store.TranscriptBefore("agent", 1, "")
	if err != nil || len(full) != 1 || full[0]["wire"] != wire {
		t.Fatal("full-detail compatibility lost", err)
	}
}

func TestTranscriptPaginatesCompletedGoalsWithMessages(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for index := range 205 {
		id := fmt.Sprintf("m-%03d", index)
		stamp := base.Add(time.Duration(index) * time.Minute)
		if err := store.Save("message", id, "agent", domain.Message{ID: id, AgentID: "agent", Role: "assistant", Content: id, CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`UPDATE resources SET created_at=? WHERE kind='message' AND id=?`, stamp, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, goal := range []domain.Goal{
		{ID: "old", AgentID: "agent", Status: "complete", UpdatedAt: base.Add(5*time.Minute + 30*time.Second)},
		{ID: "middle", AgentID: "agent", Status: "cancelled", UpdatedAt: base.Add(105*time.Minute + 30*time.Second)},
		{ID: "active", AgentID: "agent", Status: "active", UpdatedAt: base.Add(300 * time.Minute)},
		{ID: "foreign", AgentID: "other", Status: "complete", UpdatedAt: base.Add(300 * time.Minute)},
	} {
		if err := store.Save("goal_history", goal.ID, goal.AgentID, goal); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.TranscriptBefore("agent", 100, "")
	if err != nil || len(page) != 100 || page[99]["id"] != "goal:middle" || page[99]["role"] != "goal" {
		t.Fatal(page, err)
	}
	// Live output must not shift the older page, and a goal is a valid cursor.
	if err := store.Save("message", "new", "agent", domain.Message{ID: "new", AgentID: "agent"}); err != nil {
		t.Fatal(err)
	}
	page, err = store.TranscriptBefore("agent", 100, "goal:middle")
	if err != nil || len(page) != 100 || page[0]["id"] != "m-105" || page[99]["id"] != "m-006" {
		t.Fatal(page, err)
	}
	page, err = store.TranscriptBefore("agent", 100, "m-006")
	if err != nil || len(page) != 7 || page[0]["id"] != "goal:old" || page[6]["id"] != "m-000" {
		t.Fatal(page, err)
	}
	for _, cursor := range []string{"goal:foreign", "missing", "goal:active"} {
		page, err := store.TranscriptBefore("agent", 100, cursor)
		if err != nil || len(page) != 0 {
			t.Fatal("cursor leaked or invented history", cursor, page, err)
		}
	}
	// UI-only goal entries must never leak into model-context history.
	page, err = store.MessagesBefore("agent", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range page {
		if entry["role"] == "goal" {
			t.Fatal("UI goal entered model messages", entry)
		}
	}
}
