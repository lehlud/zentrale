package agent

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/providers"
)

func TestShortChatRejectsGoalsAndSubagents(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine := &Engine{Store: store}
	chat := createTestShortChat(t, engine, addTestChatUser(t, engine, "short-chat-owner"))

	for _, name := range []string{"create_goal", "get_goal", "update_goal", "subagent", "teamwork"} {
		for _, tool := range engine.shortChatTools() {
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
