package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestToolOutputReadsOwnedInnerActivitiesWithExactBoundedUTF8Slices(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("reading a saved activity must not invoke a provider")
		return providers.Result{}, nil
	}))
	content := JSON(map[string]any{"result": map[string]any{"content": strings.Repeat("ä😀 evidence ", 1000)}})
	message := domain.Message{ID: "outer-result", AgentID: record.ID, Role: "tool", ToolName: "code_mode", Content: `{"result":{"output":"selected summary","calls":2}}`}
	activities := []domain.ToolActivity{
		{Name: "read_file", Arguments: `{"path":"original.txt"}`, Content: content},
		{Name: "list_files", Arguments: `{"path":"."}`, Content: `{"result":["original.txt"]}`},
	}
	if err := engine.persistMessage(message, nil, activities); err != nil {
		t.Fatal(err)
	}

	// Recover even when the original file has changed or its SSH machine is
	// unavailable: this path uses only the saved message and activity records.
	offset := strings.Index(content, "😀") + 1
	value, err := engine.toolOutput(record, map[string]any{"messageId": message.ID, "activity": 1, "offset": offset, "limit": 1000})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	text := result["content"].(string)
	start, end := result["offset"].(int), result["nextOffset"].(int)
	if !utf8.ValidString(text) || len(text) > 1000 || start <= offset || text != content[start:end] ||
		result["toolName"] != "read_file" || result["activity"] != 1 || result["recordingTruncated"] != false || result["hasMore"] != true {
		t.Fatal("inner result lost its identity, byte bounds or exact stored data", result)
	}

	value, err = engine.toolOutput(record, map[string]any{"messageId": message.ID, "activity": 2})
	if err != nil || value.(map[string]any)["content"] != activities[1].Content {
		t.Fatal("activity selection read another inner result", value, err)
	}

	value, err = engine.toolOutput(record, map[string]any{"messageId": message.ID})
	if err != nil || value.(map[string]any)["content"] != message.Content {
		t.Fatal("ordinary outer tool_output changed", value, err)
	}
}

func TestToolOutputLabelsActivitiesThatWereTruncatedAtRecording(t *testing.T) {
	engine, record := testEngine(t, nil)
	message := domain.Message{ID: "preview-result", AgentID: record.ID, Role: "tool", ToolName: "code_mode", Content: "selected summary"}
	longResult := JSON(map[string]any{"result": strings.Repeat("source evidence ", 4000)})
	preview := modelHistory([]domain.Message{{Role: "tool", Content: longResult}}, 128000)[0].Content
	if err := engine.persistMessage(message, nil, []domain.ToolActivity{{Name: "read_file", Content: preview}}); err != nil {
		t.Fatal(err)
	}

	value, err := engine.toolOutput(record, map[string]any{"messageId": message.ID, "activity": 1})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["recordingTruncated"] != true || result["originalBytes"] != len(longResult) ||
		!strings.Contains(result["recovery"].(string), "stored preview") || result["totalBytes"] != len(preview) {
		t.Fatal("a recorded preview was presented as the complete original result", result)
	}
}

func TestInnerToolOutputCannotCrossAgentsOrReadInvalidActivityIndices(t *testing.T) {
	engine, record := testEngine(t, nil)
	message := domain.Message{ID: "owned", AgentID: record.ID, Role: "tool", Content: "outer"}
	if err := engine.persistMessage(message, nil, []domain.ToolActivity{{Name: "read_file", Content: "private recorded content"}}); err != nil {
		t.Fatal(err)
	}
	for _, index := range []any{0, -1, 2, 33, 1.5, "1"} {
		if _, err := engine.toolOutput(record, map[string]any{"messageId": message.ID, "activity": index}); err == nil {
			t.Fatal("invalid activity index accepted", index)
		}
	}

	other := record
	other.ID = "other-agent"
	if _, err := engine.toolOutput(other, map[string]any{"messageId": message.ID, "activity": 1}); err == nil {
		t.Fatal("another agent could read private activities")
	}

	// A mismatched activity owner must fail closed even if the outer message is
	// still owned by the caller.
	if err := engine.Store.Save("tool_activity", message.ID, other.ID, map[string]any{
		"agentId": other.ID, "activities": []domain.ToolActivity{{Name: "read_file", Content: "foreign activity"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.toolOutput(record, map[string]any{"messageId": message.ID, "activity": 1}); err == nil {
		t.Fatal("a foreign activity was read through an owned outer message")
	}
}

func TestCodeModeResultReferencesSurvivePersistenceAndMatchFailureReceipts(t *testing.T) {
	engine, record := testEngine(t, nil)
	if err := engine.executeCalls(context.Background(), record, []providers.Call{{
		ID: "readback", Name: "code_mode", Arguments: map[string]any{
			"code": `await tools.note_list({}); await tools.note_list({}); throw new Error("fixture after two results");`,
		},
	}}); err != nil {
		t.Fatal(err)
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 {
		t.Fatal(history, err)
	}
	var outcome struct {
		Result struct {
			MessageID string            `json:"messageId"`
			Receipts  []codeModeReceipt `json:"receipts"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(history[0].Content), &outcome); err != nil {
		t.Fatal(err)
	}
	if outcome.Result.MessageID != history[0].ID || len(outcome.Result.Receipts) != 2 ||
		outcome.Result.Receipts[0].Activity != 1 || outcome.Result.Receipts[1].Activity != 2 {
		t.Fatal("model-visible references do not identify the persisted result", outcome)
	}
	for _, receipt := range outcome.Result.Receipts {
		value, err := engine.toolOutput(record, map[string]any{"messageId": outcome.Result.MessageID, "activity": receipt.Activity})
		if err != nil || value.(map[string]any)["toolName"] != receipt.Tool || value.(map[string]any)["recordingTruncated"] != false {
			t.Fatal("receipt could not recover its own stored activity", value, err)
		}
	}
}
