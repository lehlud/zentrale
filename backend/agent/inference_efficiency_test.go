package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestInferenceTracesSeparatePurposesAndExplainPreparedPrefixChanges(t *testing.T) {
	fail := false
	engine, agent := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.ReasoningLevel != "xhigh" {
			t.Fatal("reasoning changed")
		}
		if fail {
			return providers.Result{}, errors.New("private server diagnostic must not be persisted")
		}
		return providers.Result{Text: "done", InputTokens: 100, OutputTokens: 5, CachedTokens: 80, UsageKnown: true, CacheReadKnown: true}, nil
	}))
	request := providers.Request{Config: domain.Provider{ID: "provider", Credential: "private-credential"}, Model: domain.LanguageModel{ID: "model"}, ReasoningLevel: "xhigh", System: "instructions", Messages: []domain.Message{{Role: "user", Content: "original task"}, {Role: "context", Content: "Harness runtime state:\n{}"}}}
	for index := range 4 {
		if index == 1 {
			request.Messages = append(request.Messages, domain.Message{Role: "assistant", Content: "done"})
		}
		if index == 2 {
			request.System = "new instructions"
		}
		if index == 3 {
			fail = true
		}
		result, err := engine.completeModel(context.Background(), agent.ID, "main", request)
		if (err != nil) != (index == 3) {
			t.Fatal(err)
		}
		var trace domain.InferenceTrace
		if err := engine.Store.Load("inference_trace", result.TraceID, &trace); err != nil {
			t.Fatal(err)
		}
		expected := []string{"first_request", "append_only", "configuration_changed", "append_only"}[index]
		if trace.Purpose != "main" || trace.PrefixChange != expected || trace.EstimatedRuntimeTokens == 0 || strings.Contains(JSON(trace), "private-") || strings.Contains(JSON(trace), "server diagnostic") {
			t.Fatal(trace)
		}
		if index == 1 && trace.SharedMessages != 2 {
			t.Fatal("old prepared prefix was not recognized", trace)
		}
		if index == 3 && (trace.Outcome != "failed" || trace.TokensKnown) {
			t.Fatal("failed round was presented as known free usage", trace)
		}
	}
	if _, err := engine.completeModel(context.Background(), agent.ID, "summary", request); err == nil {
		t.Fatal("fixture should fail")
	}
	rows, err := engine.Store.RecentInferenceTraces(agent.ID, 2, 0)
	if err != nil || len(rows) != 2 || rows[0]["purpose"] != "summary" {
		t.Fatal(rows, err)
	}
}

func TestRepeatedOutputsAreWindowLocalExactAndRecoverable(t *testing.T) {
	content := JSON(map[string]any{"result": strings.Repeat("exact evidence; ", 150)})
	history := []domain.Message{{ID: "one", AgentID: "agent", Role: "tool", ToolName: "read_file", CallID: "call-1", Content: content}, {ID: "two", AgentID: "agent", Role: "tool", ToolName: "read_file", CallID: "call-2", Content: content}}
	before := append([]domain.Message(nil), history...)
	view := modelHistory(history, 128000)
	if view[0].Content != content || !strings.Contains(view[1].Content, `"repeatedOutput":true`) || !strings.Contains(view[1].Content, `"messageId":"two"`) || view[1].CallID != "call-2" || !reflect.DeepEqual(history, before) {
		t.Fatal("reference lost evidence, pairing or stored history", view)
	}
	if modelHistory(history[1:], 128000)[0].Content != content {
		t.Fatal("reference depends on an evicted window")
	}
	for _, change := range []string{"owner", "tool", "content", "error", "batch_error", "recovery"} {
		copy := append([]domain.Message(nil), history...)
		switch change {
		case "owner":
			copy[1].AgentID = "foreign"
		case "tool":
			copy[1].ToolName = "exec_command"
		case "content":
			copy[1].Content += " "
		case "error":
			for i := range copy {
				copy[i].Content = JSON(map[string]any{"error": "denied", "result": content})
			}
		case "batch_error":
			for i := range copy {
				copy[i].Content = JSON(map[string]any{"result": map[string]any{"items": []any{map[string]any{"error": "denied", "result": content}}}})
			}
		case "recovery":
			for i := range copy {
				copy[i].ToolName = "tool_output"
			}
		}
		if strings.Contains(modelHistory(copy, 128000)[1].Content, `"repeatedOutput":true`) {
			t.Fatal("unsafe deduplication", change)
		}
	}
}

func TestInspectionBatchPreservesPositionsErrorsAndRejectsMutationsBeforeStarting(t *testing.T) {
	engine, record := testEngine(t, nil)
	result, err := engine.inspectBatch(context.Background(), record, map[string]any{"items": []any{
		map[string]any{"action": "instructions", "arguments": map[string]any{}},
		map[string]any{"action": "job_status", "arguments": map[string]any{"id": "missing"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	items := result.(map[string]any)["items"].([]map[string]any)
	if len(items) != 2 || items[0]["index"] != 1 || items[0]["error"] != nil || items[1]["index"] != 2 || items[1]["error"] == nil {
		t.Fatal(items)
	}
	for _, action := range []string{"exec", "batch", "note_save", "context_clear", "grants"} {
		if _, err := engine.inspectBatch(context.Background(), record, map[string]any{"items": []any{map[string]any{"action": "instructions"}, map[string]any{"action": action}}}); err == nil {
			t.Fatal("unsupported batch action accepted", action)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.inspectBatch(ctx, record, map[string]any{"items": []any{map[string]any{"action": "instructions"}}}); err == nil {
		t.Fatal("canceled inspection reported success")
	}
}

func TestStructuralLoopFeedbackSurvivesCodeModeBatchesAndResetsForUserTurns(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("invalid requests invoked approver")
		return providers.Result{}, nil
	}))
	ctx := withToolFeedback(context.Background())
	call := providers.Call{Name: "apply_patch", Arguments: map[string]any{"input": "invalid"}}
	for index := range 2 {
		_, err := engine.dispatch(ctx, record, call)
		if err == nil || strings.Contains(err.Error(), "repeated invalid") != (index == 1) {
			t.Fatal(index, err)
		}
	}
	if _, err := engine.dispatch(withToolFeedback(context.Background()), record, call); err == nil || strings.Contains(err.Error(), "repeated invalid") {
		t.Fatal("feedback crossed a user turn", err)
	}
	ctx = withToolFeedback(context.Background())
	for index := range 2 {
		value, err := engine.runCodeMode(ctx, record, map[string]any{"code": `print(await tools.apply_patch({input:"invalid"}));`})
		if err != nil || strings.Contains(JSON(value), "repeated invalid") != (index == 1) {
			t.Fatal(index, value, err)
		}
	}
}
