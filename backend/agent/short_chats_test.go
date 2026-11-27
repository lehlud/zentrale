package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/providers"
)

func TestShortChatUsesCustomInstructionsAndReferences(t *testing.T) {
	const additional = "Prefer product terminology."
	const reference = "The ventilation setting is called Air Exchange."
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if !strings.HasPrefix(request.System, shortChatPrompt) || !strings.HasSuffix(request.System, additional) {
			t.Error("inference did not receive both built-in and additional instructions")
		}
		var search, read bool
		for _, tool := range request.Tools {
			search = search || tool.Name == "knowledge_search"
			read = read || tool.Name == "knowledge_read"
		}
		if !search || !read {
			t.Error("reference tools missing from configured short chat")
		}
		for _, message := range request.Messages {
			if message.Role == "tool" && message.ToolName == "knowledge_search" {
				if !strings.Contains(message.Content, reference) {
					t.Errorf("missing passage in search result: %s", message.Content)
				}
				return providers.Result{Text: "Air Exchange", UsageKnown: true}, nil
			}
		}
		return providers.Result{
			Wire: `{"type":"function_call","call_id":"reference","name":"knowledge_search","arguments":"{\"query\":\"ventilation\"}"}`,
			Calls: []providers.Call{{
				ID: "reference", Name: "knowledge_search", Arguments: map[string]any{"query": "ventilation"},
			}},
			UsageKnown: true,
		}, nil
	})
	engine, _ := testEngine(t, provider)
	chat := createTestShortChat(t, engine, addTestChatUser(t, engine, "reference-reader"))
	if err := engine.SaveShortChatSystemPrompt(additional); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.AddShortChatDocument(t.Context(), "Manual.txt", strings.NewReader(reference)); err != nil {
		t.Fatal(err)
	}
	if err := engine.Prompt(chat.ID, "What is the ventilation setting called?"); err != nil {
		t.Fatal(err)
	}
	if record := waitForAgent(t, engine, chat.ID); record.Status != "idle" {
		t.Fatalf("short chat did not finish: %+v", record)
	}
}

func TestShortChatRejectsGoalsAndSubagents(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine := &Engine{Store: store}
	chat := createTestShortChat(t, engine, addTestChatUser(t, engine, "short-chat-owner"))

	for _, name := range []string{"create_goal", "get_goal", "update_goal", "subagent", "teamwork"} {
		for _, tool := range engine.shortChatTools(chat.ID) {
			if tool.Name == name {
				t.Errorf("short chats advertise %s", name)
			}
		}
		if _, err := engine.shortChatTool(t.Context(), chat, providers.Call{Name: name}); err == nil {
			t.Errorf("short-chat dispatcher accepted %s", name)
		}
	}

	if _, err := engine.goalTool(chat, "create_goal", map[string]any{"objective": "Keep working"}); err == nil || !strings.Contains(err.Error(), "only available in projects") {
		t.Fatalf("goal creation did not reject a short chat: %v", err)
	}
	if _, err := engine.spawn(chat, map[string]any{"name": "Worker"}); err == nil || !strings.Contains(err.Error(), "only available in projects") {
		t.Fatalf("subagent creation did not reject a short chat: %v", err)
	}
}

func TestShortChatRejectsSideConversation(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine := &Engine{Store: store}
	chat := createTestShortChat(t, engine, addTestChatUser(t, engine, "short-chat-owner"))

	if session, err := engine.OpenSide(t.Context(), chat.ID); session != nil || err == nil || !strings.Contains(err.Error(), "only available in projects") {
		t.Fatalf("side conversation did not reject a short chat: %v", err)
	}
}
