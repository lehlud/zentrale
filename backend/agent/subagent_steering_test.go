package agent

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func steeringTestChild(t *testing.T, engine *Engine, parent domain.Agent) domain.Agent {
	t.Helper()
	child, err := engine.Create(parent.ProjectID, "Child", parent.ProviderID, "approver-provider", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	return child
}

func TestSteerAgentDiscoveryAndDecoding(t *testing.T) {
	if policy, ok := toolPolicies["steer_agent"]; !ok || policy.Review != reviewRequired {
		t.Fatal("steering must require independent approval")
	}
	if !slices.Contains(codeModeTools(), "steer_agent") {
		t.Fatal("steering unavailable in code mode")
	}
	for _, query := range []string{"steer", "subagent"} {
		if !strings.Contains(strings.Join(searchToolCatalog(query), "\n"), "steer_agent {agentId,id,text}") {
			t.Fatal("steering is not discoverable", query)
		}
	}
	found := false
	for _, tool := range mainTools() {
		if tool.Name == "steer_agent" {
			found = true
			required := tool.Schema["required"].([]string)
			for _, field := range []string{"agentId", "id", "text"} {
				if !slices.Contains(required, field) {
					t.Fatal("missing required schema field", field)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing native schema")
	}
	arguments := map[string]any{"agentId": "child", "id": "retry-1", "text": "Continue"}
	for _, call := range []providers.Call{
		{Name: "steer_agent", Arguments: arguments},
		{Name: "harness", Arguments: map[string]any{"action": "steer_agent", "arguments": JSON(arguments)}},
		codeModeCall("steer_agent", arguments),
	} {
		request, err := decodeInteraction(call)
		if err != nil || request.Action != "steer_agent" || stringArg(request.Arguments, "text") != "Continue" {
			t.Fatal(request, err)
		}
	}
}

func TestSteerAgentRejectsNonOwnedTargetsAndChildren(t *testing.T) {
	engine, parent := testEngine(t, nil)
	child := steeringTestChild(t, engine, parent)
	for _, test := range []struct {
		name   string
		caller domain.Agent
		target domain.Agent
	}{
		{"self", parent, parent},
		{"unrelated", parent, domain.Agent{ID: "unrelated", ProjectID: parent.ProjectID, Role: "subagent", ParentID: "other"}},
		{"cross-project", parent, domain.Agent{ID: "foreign", ProjectID: "other", Role: "subagent", ParentID: parent.ID}},
		{"approver", parent, domain.Agent{ID: parent.ApproverID, ProjectID: parent.ProjectID, Role: "approver", ParentID: parent.ID}},
		{"child-caller", child, child},
		{"approver-caller", domain.Agent{ID: "approver", Role: "approver"}, child},
		{"nested-main", domain.Agent{ID: parent.ID, Role: "main", ParentID: "ancestor", ProjectID: parent.ProjectID}, child},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := engine.Store.Save("agent", test.target.ID, test.target.ProjectID, test.target); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.steerChildAgent(test.caller, map[string]any{"agentId": test.target.ID, "id": "denied", "text": "Do work"}); err == nil {
				t.Fatal("unauthorized steering accepted")
			}
			inputs, err := engine.Store.AgentInputs(test.target.ID)
			if err != nil || len(inputs) != 0 {
				t.Fatal("unauthorized input persisted", inputs, err)
			}
		})
	}
	if _, err := engine.spawn(child, map[string]any{"task": "nested work"}); err == nil {
		t.Fatal("child spawned a subagent")
	}
}

func TestSteerAgentDurableIdempotencyAndValidation(t *testing.T) {
	engine, parent := testEngine(t, nil)
	child := steeringTestChild(t, engine, parent)
	sibling := steeringTestChild(t, engine, parent)
	// An existing worker keeps delivery asynchronous so the committed receipt
	// can be inspected without needing inference or remote setup.
	engine.mu.Lock()
	engine.running[child.ID] = func() {}
	engine.running[sibling.ID] = func() {}
	engine.mu.Unlock()
	t.Cleanup(func() {
		engine.mu.Lock()
		delete(engine.running, child.ID)
		delete(engine.running, sibling.ID)
		engine.mu.Unlock()
	})
	arguments := map[string]any{"agentId": child.ID, "id": "retry-1", "text": "Check the tests"}
	first, err := engine.steerChildAgent(parent, arguments)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := engine.steerChildAgent(parent, arguments)
	if err != nil || first.(domain.AgentInput).ID != retry.(domain.AgentInput).ID {
		t.Fatal(first, retry, err)
	}
	receipt := first.(domain.AgentInput)
	var persisted domain.AgentInput
	if err := engine.Store.Load("agent_input", receipt.ID, &persisted); err != nil || persisted.Mode != "steer" || persisted.AgentID != child.ID || !strings.Contains(persisted.Text, "not new user authorization") {
		t.Fatal(persisted, err)
	}
	for _, bad := range []map[string]any{
		{"agentId": child.ID, "id": "retry-1", "text": "Different instruction"},
		{"agentId": child.ID, "text": "Missing id"},
		{"agentId": child.ID, "id": "invalid id", "text": "Work"},
		{"agentId": child.ID, "id": "blank", "text": "  "},
		{"agentId": child.ID, "id": "large", "text": strings.Repeat("a", 100001)},
	} {
		if _, err := engine.steerChildAgent(parent, bad); err == nil {
			t.Fatal("invalid or conflicting input accepted")
		}
	}
	inputs, err := engine.Store.AgentInputs(child.ID)
	if err != nil || len(inputs) != 1 {
		t.Fatal(inputs, err)
	}
	// The same caller key must not collide across different children.
	other, err := engine.steerChildAgent(parent, map[string]any{"agentId": sibling.ID, "id": "retry-1", "text": "Check the tests"})
	if err != nil || other.(domain.AgentInput).ID == receipt.ID {
		t.Fatal("receipt key leaked across child boundaries", other, err)
	}
}

func TestSteerAgentDispatchRequiresApproval(t *testing.T) {
	engine, parent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":false,"reason":"outside user scope"}`, UsageKnown: true}, nil
	}))
	child := steeringTestChild(t, engine, parent)
	for _, call := range []providers.Call{
		{Name: "steer_agent", Arguments: map[string]any{"agentId": child.ID, "id": "native", "text": "Do not allow"}},
		{Name: "harness", Arguments: map[string]any{"action": "steer_agent", "arguments": JSON(map[string]any{"agentId": child.ID, "id": "dispatcher", "text": "Do not allow"})}},
	} {
		if _, err := engine.dispatch(context.Background(), parent, call); err == nil {
			t.Fatal("denied steering was executed", call.Name)
		}
	}
	inputs, err := engine.Store.AgentInputs(child.ID)
	if err != nil || len(inputs) != 0 {
		t.Fatal(inputs, err)
	}
}

func TestSteerAgentResumesErroredAndWakesRunningChild(t *testing.T) {
	requests := make(chan providers.Request, 4)
	proceed := make(chan struct{}, 4)
	engine, parent := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		requests <- request
		select {
		case <-proceed:
			return providers.Result{Text: "Done", UsageKnown: true}, nil
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		}
	}))
	child := steeringTestChild(t, engine, parent)
	child.Status, child.Error = "error", "previous model failure"
	if err := engine.saveAgent(child); err != nil {
		t.Fatal(err)
	}
	next := func(marker string) {
		t.Helper()
		select {
		case request := <-requests:
			if !strings.Contains(JSON(request.Messages), marker) {
				t.Fatal("steering missing from child context", marker)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("child did not resume/wake", marker)
		}
	}
	steer := func(id, text string) {
		t.Helper()
		if _, err := engine.runTool(context.Background(), parent, interaction{Action: "steer_agent", Arguments: map[string]any{"agentId": child.ID, "id": id, "text": text}}); err != nil {
			t.Fatal(err)
		}
	}
	steer("resume", "RESUME_ERRORED_CHILD")
	next("RESUME_ERRORED_CHILD")
	steer("running", "STEER_RUNNING_CHILD")
	proceed <- struct{}{}
	next("STEER_RUNNING_CHILD")
	proceed <- struct{}{}
	if final := waitForAgent(t, engine, child.ID); final.Status != "idle" || final.Error != "" {
		t.Fatal(final)
	}
	steer("running", "STEER_RUNNING_CHILD")
	if len(requests) != 0 {
		t.Fatal("idempotent replay restarted child inference")
	}
}
