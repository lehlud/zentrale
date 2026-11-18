package agent

import (
	"context"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestShutdownDoesNotReopenSuccessfullyFinishedTurn(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{}, nil
	}))
	if err := engine.beginAgentRun(record.ID); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Patch("agent", record.ID, map[string]any{"error": "service shut down"}); err != nil {
		t.Fatal(err)
	}
	engine.cancel()
	engine.finishAgent(record.ID, nil)
	var stored domain.Agent
	if err := engine.Store.Load("agent", record.ID, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "idle" || stored.Error != "" {
		t.Fatalf("completed turn marked for restart: %+v", stored)
	}
}

func TestRollingRestartKeepsReplacementWorkerRunning(t *testing.T) {
	oldStarted := make(chan struct{})
	first, record := restartTestEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		close(oldStarted)
		<-ctx.Done()
		return providers.Result{}, ctx.Err()
	}))
	newStarted, finish := make(chan struct{}), make(chan struct{})
	replacement, err := New(first.Store, withTestGoalReviewer(providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		close(newStarted)
		select {
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		case <-finish:
			return providers.Result{Text: "Completed by the replacement", UsageKnown: true}, nil
		}
	})))
	if err != nil {
		t.Fatal(err)
	}
	replacement.Remote.Connector.NativeSessionsForTests = true
	t.Cleanup(replacement.Close)

	if err := first.Prompt(record.ID, "Continue this task across a rollout"); err != nil {
		t.Fatal(err)
	}
	waitForRestartProvider(t, first, record.ID, oldStarted)
	// The test SSH adapter must be configured before recovery launches any
	// workers. Run the startup recovery steps once the retiring worker is live.
	if err := replacement.recoverInterruptedWork(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.recoverInputs(); err != nil {
		t.Fatal(err)
	}
	waitForRestartProvider(t, replacement, record.ID, newStarted)
	first.Close()

	var stored domain.Agent
	if err := replacement.Store.Load("agent", record.ID, &stored); err != nil || stored.Status != "running" || stored.Error != "" {
		t.Fatal("old worker changed the replacement's status", stored, err)
	}
	if state := replacement.RunState(record.ID); state.Phase != "model" || state.Error != "" {
		t.Fatal("old worker changed the replacement's phase", state)
	}
	close(finish)
	if stored := waitForRestartAgent(t, replacement, record.ID); stored.Status != "idle" {
		t.Fatal("replacement did not finish", stored)
	}
}
