package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestReviewerHistoryIsBoundedAndExactEvidenceRemainsRecoverable(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("history inspection must not invoke a model")
		return providers.Result{}, nil
	}))
	var original domain.Message
	for index := range 25 {
		message := domain.Message{
			ID: fmt.Sprintf("history-%02d", index), AgentID: record.ID, Role: "assistant",
			Content: strings.Repeat("evidence ", 1000) + fmt.Sprint("needle-", index),
			Wire: JSON([]any{
				map[string]any{"type": "reasoning", "encrypted_content": strings.Repeat("private-state", 1000)},
				map[string]any{"type": "function_call", "call_id": "read-call", "name": "read_file", "arguments": `{"path":"checkpoint.txt"}`},
			}),
		}
		if err := engine.Store.Save("message", message.ID, record.ID, message); err != nil {
			t.Fatal(err)
		}
		original = message
	}

	first, err := engine.Inspect(context.Background(), record, "history", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	page := first.(map[string]any)
	if len(page["messages"].([]map[string]any)) != 10 || page["hasMore"] != true ||
		len(JSON(page)) > 16000 || strings.Contains(JSON(page), "private-state") {
		t.Fatal("reviewer history was unbounded or exposed opaque replay")
	}

	seen := map[string]bool{}
	for {
		for _, message := range page["messages"].([]map[string]any) {
			id := stringArg(message, "id")
			if seen[id] {
				t.Fatal("history page repeated an earlier message")
			}
			seen[id] = true
		}
		if page["hasMore"] != true {
			break
		}

		next, err := engine.Inspect(context.Background(), record, "history", map[string]any{"before": page["nextCursor"]})
		if err != nil {
			t.Fatal(err)
		}
		page = next.(map[string]any)
	}
	if len(seen) != 25 {
		t.Fatal("bounded history lost recoverable messages", len(seen))
	}

	for _, field := range []string{"content", "calls"} {
		args := map[string]any{"messageId": original.ID, "field": field}
		if field == "content" {
			args["offset"] = len(original.Content) - 9
			args["bytes"] = 9
		}
		result, err := engine.Inspect(context.Background(), record, "history", args)
		if err != nil {
			t.Fatal(err)
		}
		text := result.(map[string]any)["content"].(string)
		if field == "content" && text != original.Content[len(original.Content)-9:] ||
			field == "calls" && (!strings.Contains(text, "checkpoint.txt") || strings.Contains(text, "private-state")) {
			t.Fatal("exact evidence or semantic calls could not be recovered", text)
		}
	}

	result, err := engine.Inspect(context.Background(), record, "history", map[string]any{"search": "private-state"})
	if err != nil || len(result.(map[string]any)["messages"].([]map[string]any)) != 0 {
		t.Fatal("opaque reasoning leaked through search", err)
	}
	if _, err := engine.Inspect(context.Background(), record, "history", map[string]any{"messageId": original.ID, "field": "wire"}); err == nil {
		t.Fatal("raw provider replay was exposed as inspectable evidence")
	}
}

func TestReviewerHistoryRetainsItsProjectBoundary(t *testing.T) {
	engine, record := testEngine(t, nil)
	for _, project := range []string{record.ProjectID, "foreign-project"} {
		target := domain.Agent{ID: project + "-agent", ProjectID: project, Role: "main"}
		if err := engine.Store.Save("agent", target.ID, project, target); err != nil {
			t.Fatal(err)
		}
		_, err := engine.Inspect(context.Background(), record, "history", map[string]any{"agentId": target.ID})
		if (err != nil) != (project != record.ProjectID) {
			t.Fatal("reviewer history changed its project access boundary", project, err)
		}
	}
	for _, args := range []map[string]any{{"limit": 51}, {"messageId": "missing", "field": "wire"}} {
		if _, err := engine.Inspect(context.Background(), record, "history", args); err == nil {
			t.Fatal("invalid inspection bounds or raw-wire request accepted", args)
		}
	}
}

func TestMalformedFileCallsDoNotSpendAnApprovalRequest(t *testing.T) {
	reviews := 0
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		reviews++
		return providers.Result{Text: `{"allowed":false,"reason":"not authorized","blanket":false}`, UsageKnown: true}, nil
	}))
	for _, test := range []struct {
		action string
		args   map[string]any
	}{
		{"read_file", map[string]any{"path": "file.txt", "limit": 0}},
		{"apply_patch", map[string]any{"input": "not a patch"}},
		{"apply_patch", map[string]any{"input": "*** Begin Patch\n*** Update File: file.txt\n@@\n-a\n+b\n*** Update File: ./file.txt\n@@\n-c\n+d\n*** End Patch"}},
		{"apply_patch", map[string]any{"input": "*** Begin Patch\n*** Add File: a\n+x\n*** End Patch\x00"}},
	} {
		for _, native := range []bool{true, false} {
			call := providers.Call{Name: test.action, Arguments: test.args}
			if !native {
				call = providers.Call{Name: "harness", Arguments: map[string]any{"action": test.action, "arguments": JSON(test.args)}}
			}
			if _, err := engine.dispatch(context.Background(), record, call); err == nil {
				t.Fatal("invalid file call was not rejected", test.action, native)
			}
		}
	}
	if reviews != 0 {
		t.Fatal("structurally invalid requests spent model calls", reviews)
	}

	_, err := engine.dispatch(context.Background(), record, providers.Call{Name: "apply_patch", Arguments: map[string]any{
		"input": "*** Begin Patch\n*** Add File: valid.txt\n+content\n*** End Patch",
	}})
	if err == nil || reviews != 1 || !strings.Contains(err.Error(), "not authorized") {
		t.Fatal("passing structural validation bypassed independent approval", err, reviews)
	}
}
