package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestModelHistoryBoundsOnlyToolOutputAndPreservesFailures(t *testing.T) {
	for _, window := range []int{2048, 128000} {
		for _, output := range []string{
			strings.Repeat("abcdef", 10000),
			strings.Repeat("\x00\"\\\n界🦊", 10000),
		} {
			history := []domain.Message{
				{ID: "user", Role: "user", Content: strings.Repeat("preserve instruction ", 2000)},
				{ID: "call", Role: "assistant", Wire: `{"call_id":"call-1"}`},
				{
					ID:       "tool",
					Role:     "tool",
					CallID:   "call-1",
					ToolName: "harness",
					Content: JSON(map[string]any{
						"result": map[string]any{
							"job": map[string]any{
								"status": "failed",
								"error":  "command failed",
								"output": output,
							},
						},
					}),
				},
			}

			before := append([]domain.Message(nil), history...)
			bounded := modelHistory(history, window)
			if !reflect.DeepEqual(history, before) || !reflect.DeepEqual(bounded[:2], history[:2]) {
				t.Fatal("inference projection modified stored history or user instructions")
			}

			budget := min(maximumModelToolOutputBytes, window/2)
			if len(bounded[2].Content) > budget ||
				!utf8.ValidString(bounded[2].Content) ||
				!json.Valid([]byte(bounded[2].Content)) {
				t.Fatalf("invalid or oversized preview: bytes=%d budget=%d", len(bounded[2].Content), budget)
			}

			var preview struct {
				Error         string
				Truncated     bool
				OriginalBytes int
				Preview       string
			}
			if err := json.Unmarshal([]byte(bounded[2].Content), &preview); err != nil {
				t.Fatal(err)
			}

			if !preview.Truncated ||
				preview.OriginalBytes != len(history[2].Content) ||
				preview.Error != "command failed" ||
				!strings.HasPrefix(history[2].Content, strings.Split(preview.Preview, "\n[...")[0]) ||
				!strings.HasSuffix(preview.Preview, "}}}") {
				t.Fatalf("preview lost failure, boundaries or recovery metadata: %#v", preview)
			}

			if bounded[2].CallID != "call-1" || bounded[2].ID != "tool" {
				t.Fatal("tool call pairing changed")
			}
		}
	}

	if result := modelHistory([]domain.Message{
		{
			Role:    "tool",
			Content: "small",
		},
	}, 128000); result[0].Content != "small" {
		t.Fatal("small outputs should remain unchanged")
	}
}

func TestToolOutputRecoveryIsExactPaginatedAndPrivate(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("reading own history must not call a provider")
		return providers.Result{}, nil
	}))

	content := JSON(map[string]any{"result": strings.Repeat("\n界🦊quoted\"", 3000)})

	for _, message := range []domain.Message{
		{ID: "own", AgentID: record.ID, Role: "tool", Content: content},
		{ID: "foreign", AgentID: "other", Role: "tool", Content: "private"},
		{ID: "user", AgentID: record.ID, Role: "user", Content: "instruction"},
	} {
		if err := engine.Store.Save("message", message.ID, message.AgentID, message); err != nil {
			t.Fatal(err)
		}
	}

	var reconstructed strings.Builder

	for offset := 0; offset < len(content); {
		result, err := engine.toolOutput(record, map[string]any{
			"messageId": "own",
			"offset":    float64(offset),
			"limit":     float64(101),
		})
		if err != nil {
			t.Fatal(err)
		}

		page := result.(map[string]any)
		text := page["content"].(string)
		if !utf8.ValidString(text) || page["offset"] != offset || page["nextOffset"].(int) <= offset {
			t.Fatalf("invalid UTF-8 page or no progress: %v", page)
		}

		reconstructed.WriteString(text)
		offset = page["nextOffset"].(int)
	}

	if reconstructed.String() != content {
		t.Fatal("recovery dropped or duplicated output bytes")
	}

	for _, arguments := range []map[string]any{
		{"messageId": "foreign"},
		{"messageId": "user"},
		{"messageId": "missing"},

		{"messageId": "own", "offset": -1.0},
		{"messageId": "own", "offset": 1.5},

		{"messageId": "own", "limit": 0.0},
		{"messageId": "own", "limit": 16001.0},
	} {
		if _, err := engine.toolOutput(record, arguments); err == nil {
			t.Fatalf("invalid or unauthorized recovery accepted: %v", arguments)
		}
	}

	approver := domain.Agent{ID: "other"}
	call := providers.Call{
		Name: "inspect",
		Arguments: map[string]any{
			"action":    "tool_output",
			"arguments": `{"messageId":"foreign"}`,
		},
	}
	if _, err := engine.executeInspection(context.Background(), record, approver, call); err != nil {
		t.Fatalf("approver cannot recover its own inspection output: %v", err)
	}

	call.Arguments["arguments"] = `{"messageId":"own"}`

	if _, err := engine.executeInspection(context.Background(), record, approver, call); err == nil {
		t.Fatal("approver recovery read another agent's private transcript")
	}
}
