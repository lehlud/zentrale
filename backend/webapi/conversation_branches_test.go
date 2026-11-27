package webapi

import (
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestConversationBranchesGraphQLAccess(t *testing.T) {
	alice := newAPIFixture(t)
	requireData(t, alice.query(t, `mutation {setup(username:"alice",password:"correct test password",commitIdentity:{name:"Alice",email:"alice@example.org"})}`, nil))
	requireData(t, alice.query(t, `mutation {saveUser(input:{username:"bob",role:"user"},password:"bob test password"){id}}`, nil))

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	bob := *alice
	bob.client = &http.Client{Jar: jar}
	requireData(t, bob.query(t, `mutation {login(username:"bob",password:"bob test password")}`, nil))

	store := alice.engine.Store
	chat := requireData(t, bob.query(t, `mutation {createShortChat{name id approverId}}`, nil))["createShortChat"].(map[string]any)
	chatID := chat["id"].(string)
	seedAccessibleProject(t, store, "branch-project", "project-conversation")
	seedAccessibleProject(t, store, "other-project", "foreign-conversation")
	if err := store.Patch("agent", "project-conversation", map[string]any{"approverId": "fresh-reviewer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("agent", "fresh-reviewer", "branch-project", domain.Agent{
		ID: "fresh-reviewer", ProjectID: "branch-project", ParentID: "project-conversation", Role: "approver", Status: "idle",
	}); err != nil {
		t.Fatal(err)
	}
	for _, message := range []domain.Message{
		{ID: "short-chat-user", AgentID: chatID, Role: "user", Content: "Private short-chat question"},
		{ID: "short-chat-assistant", AgentID: chatID, Role: "assistant", Content: "An answer"},
		{ID: "project-user", AgentID: "project-conversation", Role: "user", Content: "A project question"},
		{ID: "foreign-user", AgentID: "foreign-conversation", Role: "user", Content: "Another project question"},
	} {
		message.CreatedAt = time.Now().UTC()
		if err := store.Save("message", message.ID, message.AgentID, message); err != nil {
			t.Fatal(err)
		}
	}

	const fork = `mutation($id:String!,$message:String!){branchConversation(agentId:$id,messageId:$message){id projectId parentId branchParentId branchRootId branchMessageId branchMessageLineageId}}`
	const family = `query($id:String!){conversationBranches(agentId:$id){id branchParentId}}`
	const history = `query($id:String!){messages(agentId:$id){lineageId}}`
	variables := func(id, message string) map[string]any { return map[string]any{"id": id, "message": message} }
	reject := func(fixture *apiFixture, query string, values map[string]any) {
		t.Helper()
		if got := fixture.query(t, query, values); got["errors"] == nil {
			t.Fatalf("expected rejection of %s, got %v", query, got)
		}
	}

	reject(alice, family, map[string]any{"id": chatID})
	reject(alice, fork, variables(chatID, "short-chat-user"))
	reject(&bob, fork, variables(chatID, "short-chat-assistant"))
	reject(&bob, fork, variables(chat["approverId"].(string), "short-chat-user"))
	reject(&bob, family, map[string]any{"id": "project-conversation"})
	reject(&bob, fork, variables("project-conversation", "project-user"))
	reject(alice, fork, variables("project-conversation", "foreign-user"))

	if err := store.Patch("agent", "project-conversation", map[string]any{"status": "running"}); err != nil {
		t.Fatal(err)
	}
	reject(alice, fork, variables("project-conversation", "project-user"))
	if err := store.Patch("agent", "project-conversation", map[string]any{"status": "idle"}); err != nil {
		t.Fatal(err)
	}

	projectFork := requireData(t, alice.query(t, fork, variables("project-conversation", "project-user")))["branchConversation"].(map[string]any)
	if projectFork["projectId"] != "branch-project" || projectFork["parentId"] != "" || projectFork["branchRootId"] != "project-conversation" {
		t.Fatalf("project fork changed project/delegation scope: %+v", projectFork)
	}
	chatFork := requireData(t, bob.query(t, fork, variables(chatID, "short-chat-user")))["branchConversation"].(map[string]any)
	if chatFork["branchParentId"] != chatID || chatFork["branchMessageId"] != "short-chat-user" || chatFork["branchMessageLineageId"] != "short-chat-user" {
		t.Fatalf("short-chat fork lineage was lost: %+v", chatFork)
	}
	forkVars := map[string]any{"id": chatFork["id"]}
	reject(alice, family, forkVars)
	branches := requireData(t, bob.query(t, family, forkVars))["conversationBranches"].([]any)
	if len(branches) != 2 || branches[0].(map[string]any)["id"] != chatID || branches[1].(map[string]any)["id"] != chatFork["id"] {
		t.Fatalf("unexpected ordered family: %+v", branches)
	}
	messages := requireData(t, bob.query(t, history, forkVars))["messages"].([]any)
	if len(messages) != 1 || messages[0].(map[string]any)["lineageId"] != "short-chat-user" {
		t.Fatalf("unexpected fork transcript lineage: %+v", messages)
	}
	original := requireData(t, bob.query(t, history, map[string]any{"id": chatID}))["messages"].([]any)
	if len(original) != 2 || original[0].(map[string]any)["lineageId"] != "short-chat-user" {
		t.Fatalf("original lineage depended on selecting id or transcript was changed: %+v", original)
	}
}
