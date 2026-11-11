package webapi

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestUserCannotPromptSubagentsOrAnswerTheirQuestions(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	child := domain.Agent{ID: "child", Role: "subagent", ParentID: "main", ProjectID: "project"}
	if err := fixture.engine.Store.Save("agent", child.ID, child.ProjectID, child); err != nil {
		t.Fatal(err)
	}
	question := domain.Question{ID: "child-question", AgentID: child.ID, Title: "Which target?", Status: "open"}
	if err := fixture.engine.Store.Save("question", question.ID, child.ID, question); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`mutation{prompt(agentId:"child",text:"Bypass parent")}`,
		`mutation{submitInput(agentId:"child",id:"attempt",text:"Bypass parent",mode:"steer"){id}}`,
		`mutation{submitInput(agentId:"child",id:"attempt",text:"Bypass parent",mode:"queue"){id}}`,
		`mutation{answerQuestion(id:"child-question",answer:"A",status:"answered")}`,
		`mutation{answerQuestion(id:"child-question",answer:"",status:"dismissed")}`,
		`mutation{cancelInput(agentId:"child",id:"attempt")}`,
		`mutation{cancelGoal(agentId:"child",goalId:"goal")}`,
	} {
		if response := fixture.query(t, query, nil); response["errors"] == nil {
			t.Fatal("user bypassed delegation", query, response)
		}
	}
	inputs, err := fixture.engine.Store.AgentInputs(child.ID)
	if err != nil || len(inputs) != 0 {
		t.Fatal("rejected input was persisted", inputs, err)
	}
	if err := fixture.engine.Store.Load("question", question.ID, &question); err != nil || question.Status != "open" {
		t.Fatal("rejected answer changed question", question, err)
	}
	// Pause is intentionally still a valid user action for a subagent.
	requireData(t, fixture.query(t, `mutation{stopAgent(agentId:"child")}`, nil))
}
