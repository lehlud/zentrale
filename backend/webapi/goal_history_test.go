package webapi

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestGoalHistoryRequiresAuthenticationAndKeepsOldGoals(t *testing.T) {
	fixture := newAPIFixture(t)
	query := `query{goalHistory(agentId:"history-agent"){id status objective}}`
	if fixture.query(t, query, nil)["errors"] == nil {
		t.Fatal("goal history was accessible without authentication")
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	if err := fixture.engine.Store.Save("agent", "history-agent", "project", domain.Agent{ID: "history-agent", ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	for _, goal := range []domain.Goal{{ID: "old", AgentID: "history-agent", Objective: "Previous work", Status: "complete"}, {ID: "new", AgentID: "history-agent", Objective: "New work", Status: "active"}, {ID: "unrelated", AgentID: "other", Objective: "Other work", Status: "complete"}} {
		if err := fixture.engine.Store.Save("goal_history", goal.ID, goal.AgentID, goal); err != nil {
			t.Fatal(err)
		}
	}
	rows := requireData(t, fixture.query(t, query, nil))["goalHistory"].([]any)
	if len(rows) != 2 {
		t.Fatal(rows)
	}
	ids := map[string]bool{}
	for _, row := range rows {
		ids[row.(map[string]any)["id"].(string)] = true
	}
	if !ids["old"] || !ids["new"] || ids["unrelated"] {
		t.Fatal(rows)
	}
}
