package datastore

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func BenchmarkMessageHistoryProjection(b *testing.B) {
	store, err := Open("")
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()

	for index := range 120 {
		message := domain.Message{
			ID: fmt.Sprintf("bench-%03d", index), AgentID: "agent", Role: "assistant",
			Content: strings.Repeat("public evidence ", 512), Wire: strings.Repeat("private provider state ", 1024),
		}
		if err := store.Save("message", message.ID, message.AgentID, message); err != nil {
			b.Fatal(err)
		}
	}

	parent := "agent"
	for _, mode := range []string{"full-resources", "bounded-excerpts"} {
		b.Run(mode, func(b *testing.B) {
			var result any
			read := func() {
				if mode == "full-resources" {
					result, err = store.List("message", &parent, 50, 0)
				} else {
					result, err = store.MessageExcerpts(parent, "", "", 50, 512)
				}

				if err != nil {
					b.Fatal(err)
				}
			}

			read()
			encoded, err := json.Marshal(result)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				read()
			}

			b.ReportMetric(float64(len(encoded)), "output-B")
		})
	}
}

func TestMessageExcerptsBoundProjectionAndTraverseTheWholeThread(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for index := range 123 {
		id := fmt.Sprintf("message-%03d", index)
		message := domain.Message{
			ID: id, AgentID: "agent", Role: "assistant",
			Content: strings.Repeat("ä", 3000) + " visible needle", Wire: "provider-private-sentinel",
		}
		if err := store.Save("message", id, "agent", message); err != nil {
			t.Fatal(err)
		}
	}

	cursor := ""
	seen := map[string]bool{}
	for {
		page, err := store.MessageExcerpts("agent", cursor, "", 20, 100)
		if err != nil {
			t.Fatal(err)
		}

		for _, row := range page {
			id := row["id"].(string)
			if seen[id] || row["wire"] != nil || row["content"] != strings.Repeat("ä", 100) {
				t.Fatal("duplicate, unbounded text or private wire in excerpt", row)
			}

			seen[id] = true
			cursor = id
		}

		if len(page) < 20 {
			break
		}
	}

	if len(seen) != 123 {
		t.Fatalf("thread truncated: read %d messages", len(seen))
	}

	found, err := store.MessageExcerpts("agent", "", "NEEDLE", 3, 40)
	if err != nil || len(found) != 3 {
		t.Fatal("visible-content search failed", err)
	}

	private, err := store.MessageExcerpts("agent", "", "provider-private-sentinel", 3, 40)
	if err != nil || len(private) != 0 {
		t.Fatal("search exposed a private provider-state oracle", private, err)
	}

	foreign, err := store.MessageExcerpts("other", cursor, "", 20, 100)
	if err != nil || len(foreign) != 0 {
		t.Fatal("cursor crossed thread scope", foreign, err)
	}

	if _, err := store.MessageExcerpts("agent", "", "", 100, 100); err == nil {
		t.Fatal("unbounded page accepted")
	}
}
