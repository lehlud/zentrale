package webapi

import (
	"fmt"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestMessageChangesAreNullableProjectedAndLoadedForFragments(t *testing.T) {
	fixture := newAPIFixture(t)

	for _, id := range []string{"old", "edited"} {
		message := domain.Message{
			ID:      id,
			AgentID: "agent",
			Role:    "tool",
			Content: `{"result":{"written":true}}`,
		}
		if err := fixture.engine.Store.Save("message", id, "agent", message); err != nil {
			t.Fatal(err)
		}
	}

	change := domain.FileChange{
		ID:      "edited",
		AgentID: "agent",
		Path:    "example.py",
		Kind:    "modified",
		Patch:   "-old\n+new\n",
		Added:   1,
		Removed: 1,
	}
	if err := fixture.engine.Store.Save("file_change", change.ID, "agent", change); err != nil {
		t.Fatal(err)
	}

	query := `query { messages(agentId:"agent") { ...Change } recentMessages(agentId:"agent") { ... on Message { change { path patch } } } } fragment Change on Message { change { path added removed } }`
	if response := fixture.query(t, query, nil); response["errors"] == nil {
		t.Fatal("unauthenticated transcript access succeeded")
	}

	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"correct test password",commitIdentity:{name:"Test Agent",email:"agent@example.test"})}`,
			nil,
		),
	)
	data := requireData(t, fixture.query(t, query, nil))

	for _, field := range []string{"messages", "recentMessages"} {
		rows := data[field].([]any)
		if len(rows) != 2 {
			t.Fatalf("missing message rows: %v", data)
		}

		found, missing := false, false

		for _, row := range rows {
			value := row.(map[string]any)["change"]
			if value == nil {
				missing = true
				continue
			}

			fields := value.(map[string]any)
			found = fields["path"] == "example.py"

			if fields["id"] != nil || fields["agentId"] != nil {
				t.Fatalf("projection included unrequested fields: %v", fields)
			}
		}

		if !found || !missing {
			t.Fatalf("missing real diff or nullable legacy result: %v", rows)
		}
	}
}

func TestOnePatchMessageReturnsEveryFileChangeInOneBatch(t *testing.T) {
	fixture := newAPIFixture(t)
	changes := []domain.FileChange{
		{
			Path:  "one.py",
			Kind:  "modified",
			Patch: "-one\n+two\n",
		},
		{
			Path:  "two.py",
			Kind:  "created",
			Patch: "+new\n",
		},
	}

	if err := fixture.engine.Store.Save("message", "old", "agent", domain.Message{
		ID:      "old",
		AgentID: "agent",
		Role:    "tool",
	}); err != nil {
		t.Fatal(err)
	}

	for index := range changes {
		changes[index].ID, changes[index].MessageID, changes[index].AgentID = fmt.Sprintf("patch-%d", index), "patch", "agent"

		if err := fixture.engine.Store.Save("file_change", changes[index].ID, "agent", changes[index]); err != nil {
			t.Fatal(err)
		}
	}

	if err := fixture.engine.Store.Save(
		"message",
		"patch",
		"agent",
		domain.Message{
			ID:      "patch",
			AgentID: "agent",
			Role:    "tool",
		},
	); err != nil {
		t.Fatal(err)
	}

	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`,
			nil,
		),
	)
	data := requireData(t, fixture.query(t, `query{messages(agentId:"agent"){id change{path} changes{path kind}}}`, nil))

	for _, value := range data["messages"].([]any) {
		message := value.(map[string]any)
		files := message["changes"].([]any)
		if message["id"] == "old" && len(files) != 0 {
			t.Fatal("legacy empty message gained a fabricated diff")
		}

		if message["id"] == "patch" &&
			(len(files) != 2 ||
				message["change"].(map[string]any)["path"] != "one.py" ||
				files[1].(map[string]any)["path"] != "two.py") {
			t.Fatal("patch files missing or legacy field changed", message)
		}
	}
}

func TestCodeModeActivitiesRemainSeparateProjectedAndAuthenticated(t *testing.T) {
	fixture := newAPIFixture(t)
	message := domain.Message{
		ID:      "combined",
		AgentID: "agent",
		Role:    "tool",
		Content: `{"result":{"output":"42","calls":1}}`,
	}
	if err := fixture.engine.Store.Save("message", message.ID, message.AgentID, message); err != nil {
		t.Fatal(err)
	}

	audit := map[string]any{
		"id":      message.ID,
		"agentId": "agent",
		"activities": []domain.ToolActivity{
			{
				Name:      "exec",
				Arguments: `{"command":"python3 answer.py"}`,
				Content:   `{"result":{"output":"42"}}`,
			},
		},
	}
	if err := fixture.engine.Store.Save("tool_activity", message.ID, message.AgentID, audit); err != nil {
		t.Fatal(err)
	}

	query := `query{messages(agentId:"agent"){content activities{name arguments content}} recentMessages(agentId:"agent"){activities{name}}}`
	if fixture.query(t, query, nil)["errors"] == nil {
		t.Fatal("unauthenticated activity access")
	}

	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`,
			nil,
		),
	)
	data := requireData(t, fixture.query(t, query, nil))

	for _, field := range []string{"messages", "recentMessages"} {
		rows := data[field].([]any)
		activities := rows[0].(map[string]any)["activities"].([]any)
		if len(activities) != 1 || activities[0].(map[string]any)["name"] != "exec" {
			t.Fatal(data)
		}
	}
}
