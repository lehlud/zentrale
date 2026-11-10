package datastore

import (
	"fmt"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestMessageCursorScrollbackIgnoresNewOutputAndOtherAgents(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index := range 205 {
		id := fmt.Sprintf("m-%03d", index)
		if err := store.Save("message", id, "agent", domain.Message{ID: id, AgentID: "agent", Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := store.MessagesBefore("agent", 100, "")
	if err != nil || len(page) != 100 || page[0]["id"] != "m-204" || page[99]["id"] != "m-105" {
		t.Fatal(page, err)
	}
	if err := store.Save("message", "new", "agent", domain.Message{ID: "new", AgentID: "agent"}); err != nil {
		t.Fatal(err)
	}
	page, err = store.MessagesBefore("agent", 100, "m-105")
	if err != nil || len(page) != 100 || page[0]["id"] != "m-104" || page[99]["id"] != "m-005" {
		t.Fatal(page, err)
	}
	page, err = store.MessagesBefore("agent", 100, "m-005")
	if err != nil || len(page) != 5 || page[4]["id"] != "m-000" {
		t.Fatal(page, err)
	}
	for _, cursor := range []string{"m-105", "missing"} {
		page, err = store.MessagesBefore("other-agent", 100, cursor)
		if err != nil || len(page) != 0 {
			t.Fatal("cursor crossed agent boundary", page, err)
		}
	}
}
