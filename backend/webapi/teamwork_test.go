package webapi

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestTeamworkQueryAuthenticationAndPublicThreadScope(t *testing.T) {
	fixture := newAPIFixture(t)
	seedAccessibleProject(t, fixture.engine.Store, "project")
	query := `query($agentId:String!){teamwork(agentId:$agentId){id agentId status revision tasks{id title kind acceptance dependsOn writePaths ownerId status result evidence}}}`
	variables := map[string]any{"agentId": "main"}
	if fixture.query(t, query, variables)["errors"] == nil {
		t.Fatal("teamwork was accessible without authentication")
	}

	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	for _, member := range []domain.Agent{
		{ID: "main", ProjectID: "project", Role: "main"},
		{ID: "worker", ProjectID: "project", ParentID: "main", Role: "subagent"},
		{ID: "private", ProjectID: "project", ParentID: "main", Role: "approver"},
		{ID: "other", ProjectID: "project", Role: "main"},
	} {
		if err := fixture.engine.Store.Save("agent", member.ID, member.ProjectID, member); err != nil {
			t.Fatal(err)
		}
	}

	if value := requireData(t, fixture.query(t, query, variables))["teamwork"]; value != nil {
		t.Fatal("missing board should be null", value)
	}

	board := domain.Teamwork{
		ID: "board", AgentID: "main", ProjectID: "project", Status: "active", Revision: 3,
		Tasks: []domain.TeamTask{{ID: "task", Title: "Work", Kind: "work", Acceptance: "Test must pass", Status: "pending"}},
	}
	if err := fixture.engine.Store.Save("teamwork", "main", "main", board); err != nil {
		t.Fatal(err)
	}

	for _, member := range []string{"main", "worker"} {
		data := requireData(t, fixture.query(t, query, map[string]any{"agentId": member}))["teamwork"].(map[string]any)
		if data["id"] != "board" || data["revision"] != float64(3) || len(data["tasks"].([]any)) != 1 {
			t.Fatal(data)
		}
	}

	if fixture.query(t, query, map[string]any{"agentId": "private"})["errors"] == nil {
		t.Fatal("private approver read public board")
	}

	if value := requireData(t, fixture.query(t, query, map[string]any{"agentId": "other"}))["teamwork"]; value != nil {
		t.Fatal("unrelated root read another board", value)
	}
}
