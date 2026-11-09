package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func responseCalls(calls ...providers.Call) providers.Result {
	wire := []map[string]any{}
	for _, call := range calls {
		wire = append(
			wire,
			map[string]any{
				"type":      "function_call",
				"call_id":   call.ID,
				"name":      call.Name,
				"arguments": JSON(call.Arguments),
			},
		)
	}

	return providers.Result{Calls: calls, Wire: JSON(wire), UsageKnown: true}
}

func TestCompactionTranscriptOmitsOpaqueStateAndDuplicateText(t *testing.T) {
	content := "Observed a failure; keep the existing checkpoint."
	message := domain.Message{
		ID:      "database-id",
		AgentID: "agent",
		Role:    "assistant",
		Content: content,
		Wire: JSON([]any{
			map[string]any{"type": "reasoning", "encrypted_content": strings.Repeat("opaque", 10000)},
			map[string]any{
				"type": "message",
				"content": []any{
					map[string]any{
						"type": "output_text",
						"text": content,
					},
				},
			},
			map[string]any{
				"type":      "function_call",
				"call_id":   "call",
				"name":      "read_file",
				"arguments": `{"path":"checkpoint.txt"}`,
			},
		}),
	}

	transcript := compactionTranscript("Keep the goal.", []domain.Message{message})
	if strings.Contains(transcript, "opaque") ||
		strings.Contains(transcript, "database-id") ||
		strings.Count(transcript, content) != 1 ||
		!strings.Contains(transcript, "checkpoint.txt") {
		t.Fatal("summary input lost meaning or retained redundant wire", transcript)
	}

	before := len(JSON([]domain.Message{message}))
	if len(transcript)*10 >= before {
		t.Fatal("compaction input did not remove opaque payload")
	}

	t.Logf("summary input: persisted JSON %d bytes, semantic transcript %d bytes", before, len(transcript))
}

func TestProviderCallPairsSurviveInterruptionAndMidCallInput(t *testing.T) {
	for _, wire := range []string{
		`[{"type":"reasoning","encrypted_content":"keep-private-state"},{"type":"function_call","call_id":"one","name":"read_file","arguments":"{\"path\":\"a\"}"},{"type":"function_call","call_id":"two","name":"read_file","arguments":"{\"path\":\"b\"}"}]`,
		`{"role":"assistant","tool_calls":[{"id":"one","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}},{"id":"two","function":{"name":"read_file","arguments":"{\"path\":\"b\"}"}}]}`,
		`[{"type":"tool_use","id":"one","name":"read_file","input":{"path":"a"}},{"type":"tool_use","id":"two","name":"read_file","input":{"path":"b"}}]`,
		`{"role":"model","parts":[{"functionCall":{"name":"read_file","args":{"path":"a"}},"thoughtSignature":"keep-private-state"},{"functionCall":{"name":"read_file","args":{"path":"b"}}}]}`,
	} {
		calls := storedCalls(domain.Message{Wire: wire})
		if len(calls) != 2 {
			t.Fatalf("missing native calls: %s", wire)
		}

		history := []domain.Message{
			{
				Role: "assistant",
				Wire: wire,
			},
			{
				Role:    "user",
				Content: "New correction; preserve it.",
			},
			{
				Role:     "tool",
				CallID:   calls[0].ID,
				ToolName: calls[0].Name,
				Content:  "actual result",
			},
		}

		original := append([]domain.Message(nil), history...)
		normalized := restoreConversationBoundary(history)
		if len(normalized) != 4 ||
			normalized[0].Wire != wire ||
			normalized[1].Content != "actual result" ||
			normalized[2].CallID != calls[1].ID ||
			!strings.Contains(normalized[2].Content, "interrupted") ||
			normalized[3].Content != history[1].Content {
			t.Fatalf("invalid pairing or input lost: %+v", normalized)
		}

		if !reflect.DeepEqual(history, original) {
			t.Fatal("projection modified persisted history")
		}
	}

	orphan := restoreConversationBoundary([]domain.Message{
		{
			Role:    "user",
			Content: "Keep working",
		},
		{
			Role:    "tool",
			Content: "result from previous context",
		},
	})
	if orphan[1].Role == "tool" || !strings.Contains(orphan[1].Content, "result from previous context") {
		t.Fatal("orphan output lost or replayed as a tool response")
	}
}

func TestCompactionRetainsOriginalInstructionsAndRecentToolGroup(t *testing.T) {
	requests := 0
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		requests++

		if !strings.HasPrefix(request.System, compactionInstructions) {
			t.Fatal("unexpected normal inference")
		}

		if estimatedMessageTokens(request.Messages)+estimatedPromptTokens(request.System, request.Tools) > request.Model.ContextWindow {
			t.Fatal("summary request exceeds its token budget")
		}

		return providers.Result{
			Text:       "Older inspection complete. Continue the pending work; keep user constraints.",
			UsageKnown: true,
		}, nil
	}))

	call := responseCalls(providers.Call{
		ID:   "recent",
		Name: "read_file",
		Arguments: map[string]any{
			"path": "recent.py",
		},
	})

	history := []domain.Message{
		{Role: "user", Content: "Original task: finish the implementation and tests."},
		{Role: "assistant", Content: strings.Repeat("older inspection result; ", 2000)},
		{Role: "user", Content: "Correction: do not overwrite the existing configuration."},
		{Role: "assistant", Wire: call.Wire},
		{
			Role:     "tool",
			CallID:   "recent",
			ToolName: "read_file",
			Content:  `{"result":{"content":"actual recent file"}}`,
		},
	}

	for _, message := range history {
		if err := engine.appendMessage(record.ID, message.Role, message.Content, message.Wire, message.CallID, message.ToolName); err != nil {
			t.Fatal(err)
		}
	}

	configuration := inferenceConfig{Model: domain.LanguageModel{ID: record.ModelID, ContextWindow: 4096}}
	prepared, messages, err := engine.prepareContext(context.Background(), record, configuration, false)
	if err != nil {
		t.Fatal(err)
	}

	if prepared.ContextStart != 2 ||
		requests == 0 ||
		!strings.Contains(prepared.Summary, "Older inspection") {
		t.Fatal("unexpected retained boundary", prepared, requests)
	}

	if messages[0].Content != history[0].Content ||
		!strings.Contains(JSON(messages), history[2].Content) ||
		messages[len(messages)-2].Wire != call.Wire ||
		messages[len(messages)-1].CallID != "recent" {
		t.Fatalf("instructions or exact recent group lost: %+v", messages)
	}

	before := requests
	if _, _, err := engine.prepareContext(context.Background(), prepared, configuration, false); err != nil ||
		requests != before {
		t.Fatal("compacted context immediately compacted again", err)
	}

	stored, _ := engine.loadHistory(record.ID)
	if len(stored) != len(history) || stored[1].Content != history[1].Content {
		t.Fatal("compaction changed the audit history")
	}
}

func TestApproverSnapshotProjectionKeepsAnAppendOnlyPrefix(t *testing.T) {
	history := []domain.Message{}
	for index := range 20 {
		instructions := strings.Repeat("Preserve the user's project and constraints. ", 100)
		history = append(
			history,
			domain.Message{
				Role: "event",
				Content: JSON(map[string]any{
					"mainAgentId": "main",
					"action":      "exec",
					"arguments": map[string]any{
						"command": "ls",
						"index":   index,
					},
					"userInstructions": instructions,
					"project": map[string]any{
						"directory": "/srv/project",
					},
					"sources": []string{
						"read-only",
					},
				}),
			},
		)
	}

	original := append([]domain.Message(nil), history...)
	previous := compactReviewSnapshots(history[:19])
	projected := compactReviewSnapshots(history)
	if !reflect.DeepEqual(previous, projected[:len(previous)]) {
		t.Fatal("new review rewrote the cached prefix")
	}

	previousBudget, err := budgetReviewHistory(history[:19], 128000, 1500)
	if err != nil {
		t.Fatal(err)
	}

	currentBudget, err := budgetReviewHistory(history, 128000, 1500)
	if err != nil || !reflect.DeepEqual(previousBudget, currentBudget[:len(previousBudget)]) {
		t.Fatal("review budgeting invalidated the normalized prefix", err)
	}

	if !strings.Contains(projected[0].Content, "userInstructions") ||
		!strings.Contains(projected[1].Content, "contextSnapshotRef") ||
		strings.Contains(projected[1].Content, "userInstructions") {
		t.Fatal("duplicate context was not reduced")
	}

	if !reflect.DeepEqual(history, original) {
		t.Fatal("projection modified the audit history")
	}

	if len(JSON(projected))*3 >= len(JSON(history)) {
		t.Fatal("unchanged review snapshots still dominate the request")
	}

	t.Logf(
		"20 review requests: stored %d bytes, inference projection %d bytes",
		len(JSON(history)),
		len(JSON(projected)),
	)
	history = append(
		history,
		domain.Message{
			Role: "event",
			Content: JSON(map[string]any{
				"mainAgentId":      "main",
				"action":           "exec",
				"userInstructions": "New instruction: do not delete files.",
			}),
		},
	)

	changed := compactReviewSnapshots(history)
	if !strings.Contains(changed[0].Content, "New instruction: do not delete files.") ||
		!reflect.DeepEqual(projected[1:], changed[1:len(projected)]) {
		t.Fatal("changed instructions missing or historical reviews rewritten")
	}
}

func TestContextBudgetIncludesSystemAndToolDefinitions(t *testing.T) {
	configuration := inferenceConfig{Model: domain.LanguageModel{ContextWindow: 1000}}
	messages := []domain.Message{{Role: "user", Content: strings.Repeat("x", 2000)}}
	if shouldCompact(domain.Agent{}, configuration, messages, false, 0) ||
		!shouldCompact(domain.Agent{}, configuration, messages, false, 400) {
		t.Fatal("static prompt was omitted from context accounting")
	}
}
