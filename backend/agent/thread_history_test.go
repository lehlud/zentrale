package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestPublicThreadIncludesDescendantsButNotPrivateOrUnrelatedAgents(t *testing.T) {
	engine, root := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("history retrieval must not invoke a model")
		return providers.Result{}, nil
	}))
	for _, record := range []domain.Agent{
		{ID: "child", ParentID: root.ID, ProjectID: root.ProjectID, Role: "subagent"},
		{ID: "grandchild", ParentID: "child", ProjectID: root.ProjectID, Role: "subagent"},
		{ID: "private", ParentID: root.ID, ProjectID: root.ProjectID, Role: "approver"},
		{ID: "unrelated", ProjectID: root.ProjectID, Role: "main"},
		{ID: "foreign", ParentID: root.ID, ProjectID: "other-project", Role: "subagent"},
		{ID: "cycle", ParentID: "cycle", ProjectID: root.ProjectID, Role: "subagent"},
	} {
		if err := engine.Store.Save("agent", record.ID, record.ProjectID, record); err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range []string{root.ID, "child", "grandchild"} {
		if _, err := engine.threadMember(root, id); err != nil {
			t.Fatalf("lost public member %s: %v", id, err)
		}
	}

	for _, id := range []string{"private", "unrelated", "foreign", "cycle"} {
		if _, err := engine.threadMember(root, id); err == nil {
			t.Fatalf("scope leak to %s", id)
		}
	}

	agents, err := engine.threadAgents(root)
	if err != nil || len(agents) != 3 {
		t.Fatal("incorrect public tree", agents, err)
	}

	message := domain.Message{ID: "message", AgentID: "grandchild", Role: "assistant", Content: "ä😀" + strings.Repeat("data", 2000)}
	if err := engine.Store.Save("message", message.ID, message.AgentID, message); err != nil {
		t.Fatal(err)
	}

	result, err := engine.threadHistory(root, map[string]any{"agentId": "grandchild", "messageId": "message", "bytes": 8})
	if err != nil {
		t.Fatal(err)
	}
	content := result["content"].(string)
	if !utf8.ValidString(content) || len(content) > 8 || !strings.HasPrefix(message.Content, content) {
		t.Fatal("slice lost byte bounds or UTF-8 integrity", content)
	}

	if _, err := engine.threadHistory(root, map[string]any{"messageId": "message"}); err == nil {
		t.Fatal("message read crossed selected member")
	}
}
