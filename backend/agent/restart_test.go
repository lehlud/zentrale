package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

// Exercise real workspace preparation over the existing loopback SSH peer.
// The legacy testEngine peer only echoes commands and cannot produce the
// structured repository-preparation result required before inference.
func restartTestEngine(t *testing.T, provider providers.Client) (*Engine, domain.Agent) {
	t.Helper()
	engine, record, project := executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = "none"
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	return engine, record
}

func waitForRestartProvider(t *testing.T, engine *Engine, agentID string, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(restartTestTimeout(t)):
		var record domain.Agent
		_ = engine.Store.Load("agent", agentID, &record)
		t.Fatalf("provider never started: %+v", record)
	}
}

func restartTestTimeout(t *testing.T) time.Duration {
	t.Helper()
	// CPU emulation is needed on hosts lacking DuckDB's required instructions.
	// Opt in to a larger integration budget there; ordinary runs stay bounded
	// to thirty seconds and all completion/replay assertions remain identical.
	if value := os.Getenv("ZENTRALE_RESTART_TEST_TIMEOUT"); value != "" {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			t.Fatalf("invalid ZENTRALE_RESTART_TEST_TIMEOUT %q", value)
		}
		return duration
	}
	return 30 * time.Second
}

// Real SSH preparation and CPU-emulated DuckDB can exceed the legacy fixture's
// five-second deadline. Keep a bounded integration deadline without changing
// any completion, replay, input-count or goal-intent assertions.
func waitForRestartAgent(t *testing.T, engine *Engine, agentID string) domain.Agent {
	t.Helper()
	timeout := time.NewTimer(restartTestTimeout(t))
	defer timeout.Stop()
	updates, unsubscribe := engine.Subscribe()
	defer unsubscribe()
	for {
		var record domain.Agent
		if err := engine.Store.Load("agent", agentID, &record); err != nil {
			t.Fatal(err)
		}
		if record.Status != "running" {
			return record
		}
		select {
		case <-updates:
		case <-timeout.C:
			t.Fatalf("agent did not finish: %+v", record)
		}
	}
}

func TestShutdownCannotRegisterAnotherAgentWorker(t *testing.T) {
	engine, record := restartTestEngine(t, nil)
	engine.Close()
	engine.configMu.Lock()
	err := engine.startOrWakeAgent(record)
	engine.configMu.Unlock()
	if !errors.Is(err, context.Canceled) {
		t.Fatal("closed engine accepted a worker", err)
	}
	engine.mu.Lock()
	running := len(engine.running)
	engine.mu.Unlock()
	if running != 0 {
		t.Fatal("worker registered after shutdown", running)
	}
}

func TestRestartHonorsStopCommittedBeforeWorkerExit(t *testing.T) {
	engine, record := restartTestEngine(t, nil)
	if err := engine.Stop(record.ID); err != nil {
		t.Fatal(err)
	}
	// A killed worker may still have a running agent row even though Stop
	// returned successfully. The durable stop receipt is authoritative.
	record.Status = "running"
	if err := engine.saveAgent(record); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(engine.Store, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("explicitly stopped work was resumed")
		return providers.Result{}, context.Canceled
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.Store.Load("agent", record.ID, &record); err != nil || record.Status != "stopped" || record.Error != "" {
		t.Fatal(record, err)
	}
	if state := restarted.RunState(record.ID); state.Phase != "stopped" {
		t.Fatal(state)
	}
}

func TestRestartNotificationDrivenTurn(t *testing.T) {
	for _, graceful := range []bool{false, true} {
		t.Run(map[bool]string{false: "abrupt", true: "graceful"}[graceful], func(t *testing.T) {
			started := make(chan struct{})
			engine, record := restartTestEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
				close(started)
				<-ctx.Done()
				return providers.Result{}, ctx.Err()
			}))
			if graceful {
				if err := engine.NotifyAgent(record.ID, "Continue interrupted work"); err != nil {
					t.Fatal(err)
				}
				waitForRestartProvider(t, engine, record.ID, started)
				engine.Close()
				if err := engine.Store.Load("agent", record.ID, &record); err != nil || record.Status != "interrupted" {
					t.Fatal(record, err)
				}
			} else {
				// Persist exactly the state left by a killed process, with no worker
				// in this test engine and no accepted input or persistent goal.
				record.Status = "running"
				if err := engine.saveAgent(record); err != nil {
					t.Fatal(err)
				}
				if err := engine.appendMessage(record.ID, "event", "Continue interrupted work", "", "", ""); err != nil {
					t.Fatal(err)
				}
			}

			var calls atomic.Int32
			restarted, err := New(engine.Store, withTestGoalReviewer(providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				calls.Add(1)
				return providers.Result{Text: "Done", UsageKnown: true}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(restarted.Close)
			if final := waitForRestartAgent(t, restarted, record.ID); final.Status != "idle" || calls.Load() != 1 {
				t.Fatal(final, calls.Load())
			}
		})
	}
}

func TestRestartPreservesGoalIntent(t *testing.T) {
	for _, status := range []string{"active", "paused", "blocked", "complete", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			engine, record := restartTestEngine(t, nil)
			record.Status = "waiting"
			if err := engine.saveAgent(record); err != nil {
				t.Fatal(err)
			}
			goal := domain.Goal{ID: "restart-goal", AgentID: record.ID, Status: status, Objective: "Preserve this task", SourceInputIDs: []string{"old-input"}}
			if err := engine.saveGoal(goal); err != nil {
				t.Fatal(err)
			}
			// Active-goal recovery must work even without an active input. For
			// terminal goals, simulate a crash before input finalization.
			if status != "active" {
				input := domain.AgentInput{ID: "old-input", AgentID: record.ID, ProjectID: record.ProjectID, Status: "active", Text: "Old task", DeliveredAt: time.Now().UTC()}
				if err := engine.Store.Save("agent_input", input.ID, record.ID, input); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int32
			restarted, err := New(engine.Store, withTestGoalReviewer(providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				calls.Add(1)
				return providers.Result{Text: "Done", UsageKnown: true}, nil
			})))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(restarted.Close)
			waitForRestartAgent(t, restarted, record.ID)
			if status == "active" {
				if calls.Load() != 1 {
					t.Fatal("active goal did not resume", calls.Load())
				}
			} else {
				current, err := restarted.CurrentGoal(record.ID)
				if err != nil || current.Status != status || calls.Load() != 0 {
					t.Fatal("terminal intent changed", current, calls.Load(), err)
				}
			}
		})
	}
}

func TestRestartDoesNotReplayPartialToolBatch(t *testing.T) {
	engine, record := restartTestEngine(t, nil)
	record.Status = "running"
	if err := engine.saveAgent(record); err != nil {
		t.Fatal(err)
	}
	wire := `{"tool_calls":[{"id":"known","type":"function","function":{"name":"create_goal","arguments":"{\"objective\":\"MUST_NOT_REPLAY\"}"}},{"id":"unknown","type":"function","function":{"name":"create_goal","arguments":"{\"objective\":\"MUST_NOT_REPLAY\"}"}}]}`
	if err := engine.appendMessage(record.ID, "assistant", "", wire, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := engine.appendMessage(record.ID, "tool", "KNOWN_RESULT", "", "known", "create_goal"); err != nil {
		t.Fatal(err)
	}
	job := domain.Job{ID: "old-job", AgentID: record.ID, Status: "running"}
	if err := engine.Store.Save("job", job.ID, record.ID, job); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	restarted, err := New(engine.Store, withTestGoalReviewer(providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		calls.Add(1)
		current, err := engine.CurrentGoal(record.ID)
		if err != nil || current != nil {
			t.Error("stored call replayed", current, err)
		}
		known, unknown := 0, 0
		for _, message := range request.Messages {
			if message.Role != "tool" {
				continue
			}
			switch message.CallID {
			case "known":
				known++
				if message.Content != "KNOWN_RESULT" {
					t.Error("persisted result replaced", message)
				}
			case "unknown":
				unknown++
				if !strings.Contains(message.Content, "Inspect current state before retrying") || !strings.Contains(message.Content, "not a successful result") {
					t.Error("missing side-effect safety warning", message)
				}
			}
		}
		if known != 1 || unknown != 1 {
			t.Error("invalid recovered tool batch", request.Messages)
		}
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if final := waitForRestartAgent(t, restarted, record.ID); final.Status != "idle" || calls.Load() != 1 {
		t.Fatal(final, calls.Load())
	}
	if err := engine.Store.Load("job", job.ID, &job); err != nil || job.Status != "interrupted" {
		t.Fatal("old job was replayed", job, err)
	}
}

func TestRestartCompletedGoalAllowsFreshQueuedWork(t *testing.T) {
	engine, record := restartTestEngine(t, nil)
	goal := domain.Goal{ID: "finished-goal", AgentID: record.ID, Status: "complete", Objective: "Finished task", SourceInputIDs: []string{"finished-input"}}
	if err := engine.saveGoal(goal); err != nil {
		t.Fatal(err)
	}
	// Put the queued input first to ensure cleanup precedes worker launch,
	// independent of input ordering.
	now := time.Now().UTC()
	for _, input := range []domain.AgentInput{
		{ID: "fresh-input", AgentID: record.ID, ProjectID: record.ProjectID, Status: "queued", Mode: "queue", Text: "FRESH_TASK", CreatedAt: now},
		{ID: "finished-input", AgentID: record.ID, ProjectID: record.ProjectID, Status: "active", Text: "Finished task", CreatedAt: now.Add(time.Second), DeliveredAt: now},
	} {
		if err := engine.Store.Save("agent_input", input.ID, record.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	restarted, err := New(engine.Store, withTestGoalReviewer(providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		calls.Add(1)
		if !strings.Contains(JSON(request.Messages), "FRESH_TASK") {
			t.Error("stale active input prevented fresh delivery", request.Messages)
		}
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if final := waitForRestartAgent(t, restarted, record.ID); final.Status != "idle" || calls.Load() != 1 {
		t.Fatal(final, calls.Load())
	}
	var finished domain.AgentInput
	if err := engine.Store.Load("agent_input", "finished-input", &finished); err != nil || finished.Status != "completed" {
		t.Fatal(finished, err)
	}
}
