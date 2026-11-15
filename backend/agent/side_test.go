package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestSideConversationReadsWorkersWithoutPersistingConversationState(t *testing.T) {
	calls := 0
	engine, root := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		calls++
		if request.Config.ID != "main-provider" || len(request.Tools) != 2 || !strings.Contains(request.System, "read-only") {
			t.Fatal("side inference changed provider or advertised environmental tools")
		}

		if calls == 1 {
			return responseCalls(providers.Call{ID: "read", Name: "thread_history", Arguments: map[string]any{"agentId": "worker", "search": "measurement"}}), nil
		}

		if !strings.Contains(JSON(request.Messages), "measurement passed") {
			t.Fatal("worker evidence did not reach side context")
		}

		return providers.Result{Text: "The worker's measurement passed.", UsageKnown: true, InputTokens: 100, OutputTokens: 10}, nil
	}))
	worker := domain.Agent{ID: "worker", ParentID: root.ID, ProjectID: root.ProjectID, Role: "subagent"}
	if err := engine.Store.Save("agent", worker.ID, worker.ProjectID, worker); err != nil {
		t.Fatal(err)
	}
	if err := engine.appendMessage(worker.ID, "assistant", "measurement passed", "", "", ""); err != nil {
		t.Fatal(err)
	}

	before, err := engine.Store.List("agent", &root.ProjectID, 500, 0)
	if err != nil {
		t.Fatal(err)
	}

	session, err := engine.OpenSide(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	answer, err := session.Ask("What did the worker establish?", nil)
	if err != nil || !strings.Contains(answer, "passed") || calls != 2 {
		t.Fatal(answer, calls, err)
	}

	if _, err := session.read(providers.Call{Name: "exec", Arguments: map[string]any{"command": "touch forbidden"}}); err == nil {
		t.Fatal("side accepted an execution tool")
	}

	after, err := engine.Store.List("agent", &root.ProjectID, 500, 0)
	if err != nil || len(after) != len(before) {
		t.Fatal("side persisted an agent", err)
	}
	mainMessages, err := engine.loadHistory(root.ID)
	if err != nil || len(mainMessages) != 0 {
		t.Fatal("side wrote to main transcript", mainMessages, err)
	}
	var current domain.Agent
	if err := engine.Store.Load("agent", root.ID, &current); err != nil || current.ContextTokens != root.ContextTokens || current.Status != root.Status {
		t.Fatal("side changed main context or status", current, err)
	}
}

func TestSideCloseCancelsInferenceAndReleasesAdmission(t *testing.T) {
	started := make(chan struct{})
	engine, root := testEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		close(started)
		<-ctx.Done()
		return providers.Result{}, ctx.Err()
	}))
	session, err := engine.OpenSide(context.Background(), root.ID)
	if err != nil {
		t.Fatal(err)
	}

	finished := make(chan error, 1)
	go func() { _, err := session.Ask("What is happening?", nil); finished <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("side inference did not start")
	}

	session.Close()
	session.Close()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled inference reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("side inference survived close")
	}

	if len(engine.sideSlots) != 0 || len(session.history) != 0 {
		t.Fatal("closed side retained admission or conversation memory")
	}
	if _, err := session.Ask("Still alive?", nil); err == nil {
		t.Fatal("closed side accepted another question")
	}
}

func TestSideAdmissionIsBoundedAndPrivateAgentsAreRejected(t *testing.T) {
	engine, root := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("opening an empty modal must not call a model")
		return providers.Result{}, nil
	}))
	if _, err := engine.OpenSide(context.Background(), root.ApproverID); err == nil {
		t.Fatal("private approver opened a public side conversation")
	}

	sessions := []*SideSession{}
	defer func() {
		for _, session := range sessions {
			session.Close()
		}
	}()
	for range 8 {
		session, err := engine.OpenSide(context.Background(), root.ID)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	if _, err := engine.OpenSide(context.Background(), root.ID); err == nil {
		t.Fatal("unbounded side admission")
	}

	sessions[0].Close()
	replacement, err := engine.OpenSide(context.Background(), root.ID)
	if err != nil {
		t.Fatal("closed side did not free its slot", err)
	}
	replacement.Close()
}
