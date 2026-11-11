package datastore

import (
	"fmt"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

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
