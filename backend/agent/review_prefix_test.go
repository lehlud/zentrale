package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestReviewSectionsKeepAppendOnlyPrefixesBetweenBoundedRollovers(t *testing.T) {
	history := []domain.Message{}
	var previous []domain.Message
	preserved, rollovers := 0, 0
	for index := range 100 {
		history = append(history, domain.Message{
			ID: fmt.Sprint("review-", index), Role: "event",
			Content: JSON(map[string]any{
				"mainAgentId": "main", "action": "exec",
				"userInstructions": "Keep all original user constraints; earlier verdicts never grant permission.",
				"arguments": map[string]any{
					"command": strings.Repeat("inspect ", 400), "index": index,
				},
			}),
		})
		view, err := budgetReviewHistory(history, 272000, 2000)
		if err != nil {
			t.Fatal(err)
		}

		if len(previous) > 0 {
			if len(view) >= len(previous) && reflect.DeepEqual(previous, view[:len(previous)]) {
				preserved++
			} else {
				rollovers++
			}
		}

		latest := compactReviewSnapshots(history[len(history)-1:])
		mandatory := estimatedMessageTokens(latest)
		if estimatedMessageTokens(view) > mandatory+maximumReviewHistoryTokens+100 ||
			view[len(view)-1].ID != history[len(history)-1].ID {
			t.Fatal("bounded section lost the current review or exceeded its history budget")
		}

		previous = view
		history = append(history, domain.Message{
			ID: fmt.Sprint("verdict-", index), Role: "assistant", Content: `{"allowed":false,"reason":"fixture denial","blanket":false}`,
		})
	}

	if preserved < 80 || rollovers < 5 || rollovers > 20 {
		t.Fatal("review history slid on every turn or never rolled over", preserved, rollovers)
	}
	t.Logf("99 review transitions: %d preserve the entire previous prefix; %d bounded rollovers", preserved, rollovers)
}

func TestOversizedCompletedReviewDoesNotDisplaceTheCurrentSectionForever(t *testing.T) {
	event := func(id, command string) domain.Message {
		return domain.Message{ID: id, Role: "event", Content: JSON(map[string]any{
			"mainAgentId": "main", "action": "exec", "userInstructions": "Never delete user data.",
			"arguments": map[string]any{"command": command},
		})}
	}
	history := []domain.Message{
		event("old", "old request"),
		event("huge", strings.Repeat("large request ", 4000)),
		{Role: "assistant", Content: "denied"},
		event("recent", "recent request"),
		{Role: "assistant", Content: "denied"},
		event("current", "current request"),
	}
	view, err := budgetReviewHistory(history, 272000, 2000)
	if err != nil || strings.Contains(JSON(view), "large request") ||
		!strings.Contains(JSON(view), "recent request") || !strings.Contains(JSON(view), "current request") ||
		!strings.Contains(view[0].Content, "Never delete user data.") {
		t.Fatal("oversized old group removed mandatory evidence or later bounded reviews", err)
	}

	// The very same large request must remain complete while it is current.
	view, err = budgetReviewHistory(history[:2], 272000, 2000)
	projected := compactReviewSnapshots(history[:2])
	if err != nil || view[len(view)-1].Content != projected[len(projected)-1].Content {
		t.Fatal("the current large request was cut to the optional history budget", err)
	}
}
