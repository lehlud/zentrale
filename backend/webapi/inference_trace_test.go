package webapi

import (
	"github.com/lehlud/zentrale/domain"
	"testing"
)

func TestInferenceTraceQueryIsBoundedRecentAndAgentScoped(t *testing.T) {
	fixture := newAPIFixture(t)
	if fixture.query(t, `query{inferenceTraces(agentId:"own"){id}}`, nil)["errors"] == nil {
		t.Fatal("diagnostics bypass authentication")
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	for _, trace := range []domain.InferenceTrace{
		{ID: "first", AgentID: "own", Purpose: "main", Outcome: "completed", Attempts: []domain.InferenceAttempt{}},
		{ID: "second", AgentID: "own", Purpose: "summary", Outcome: "failed", Attempts: []domain.InferenceAttempt{{Number: 1, Transport: "http", Outcome: "stream_interrupted"}}},
		{ID: "foreign", AgentID: "other", Purpose: "approval", Attempts: []domain.InferenceAttempt{}},
	} {
		if err := fixture.engine.Store.Save("inference_trace", trace.ID, trace.AgentID, trace); err != nil {
			t.Fatal(err)
		}
	}
	data := requireData(t, fixture.query(t, `query{inferenceTraces(agentId:"own",limit:1){id purpose outcome tokensKnown attempts{number transport outcome tokensKnown}}}`, nil))
	rows := data["inferenceTraces"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "second" || rows[0].(map[string]any)["tokensKnown"] != false {
		t.Fatal(data)
	}
	data = requireData(t, fixture.query(t, `query{inferenceTraces(agentId:"own",limit:1000,offset:1){id}}`, nil))
	rows = data["inferenceTraces"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["id"] != "first" {
		t.Fatal(data)
	}
}
