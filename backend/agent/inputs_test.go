package agent

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestInputReceiptPrecedesPreparationAndSurvivesRequestCancellation(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	var blocked atomic.Bool
	var calls atomic.Int32
	base := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, _ := executionTestEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		calls.Add(1)
		if !strings.Contains(JSON(request.Messages), "STEERING_DURING_PREPARATION") {
			t.Error("inference started without steering received during setup")
		}
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	}), func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "for required in") && blocked.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return base(command, input, stdout, stderr)
	})
	ctx, cancel := context.WithCancel(context.Background())
	accepted := make(chan error, 1)
	go func() { accepted <- engine.PromptContext(ctx, record.ID, "Work after my browser closes") }()
	select {
	case err := <-accepted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("receipt waited for remote preparation")
	}
	cancel()
	<-entered
	inputs, err := engine.Store.AgentInputs(record.ID)
	if err != nil || len(inputs) != 1 || calls.Load() != 0 {
		t.Fatal(inputs, calls.Load(), err)
	}
	if state := engine.RunState(record.ID); state.Phase != "connecting" {
		t.Fatal(state)
	}
	if _, err := engine.SubmitInput(record.ID, "during-preparation", "STEERING_DURING_PREPARATION", "steer"); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" || calls.Load() != 1 {
		t.Fatal(final, calls.Load())
	}
}

func TestQueuedMessagesWaitForFinalAnswerAndReceiptsAreIdempotent(t *testing.T) {
	requests := make(chan providers.Request, 4)
	proceed := make(chan struct{}, 4)
	engine, record := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		requests <- request
		select {
		case <-proceed:
			return providers.Result{Text: "Done", UsageKnown: true}, nil
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		}
	}))
	submit := func(id, text, mode string) {
		t.Helper()
		if _, err := engine.SubmitInput(record.ID, id, text, mode); err != nil {
			t.Fatal(err)
		}
	}
	next := func() providers.Request {
		t.Helper()
		select {
		case request := <-requests:
			return request
		case <-time.After(3 * time.Second):
			t.Fatal("input was not processed")
			return providers.Request{}
		}
	}
	submit("original", "Original task", "steer")
	next()
	submit("queued-one", "QUEUED_ONE", "queue")
	submit("queued-two", "QUEUED_TWO", "queue")
	submit("steering", "STEERING_NOW", "steer")
	submit("steering", "STEERING_NOW", "steer")
	instructions, err := engine.listAll("instruction", &record.ProjectID)
	if err != nil || strings.Contains(JSON(instructions), "QUEUED") {
		t.Fatal(instructions, err)
	}
	proceed <- struct{}{}
	steered := next()
	if !strings.Contains(JSON(steered.Messages), "STEERING_NOW") || strings.Contains(JSON(steered.Messages), "QUEUED") {
		t.Fatal(steered.Messages)
	}
	if err := engine.CancelInput(record.ID, "queued-two"); err != nil {
		t.Fatal(err)
	}
	proceed <- struct{}{}
	queued := next()
	if !strings.Contains(JSON(queued.Messages), "QUEUED_ONE") || strings.Contains(JSON(queued.Messages), "QUEUED_TWO") {
		t.Fatal(queued.Messages)
	}
	proceed <- struct{}{}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}
	submit("original", "Original task", "steer")
	if _, err := engine.SubmitInput(record.ID, "original", "different task", "steer"); err == nil {
		t.Fatal("receipt reused for a different message")
	}
	if len(requests) != 0 {
		t.Fatal("receipt replay invoked the model twice")
	}
	history, _ := engine.loadHistory(record.ID)
	if len(history) != 6 {
		t.Fatal("messages duplicated or lost", history)
	}
	inputs, _ := engine.Store.AgentInputs(record.ID)
	if len(inputs) != 0 {
		t.Fatal("completed inputs remained queued", inputs)
	}
}

func TestSteeringDoesNotAbortStartedToolBatches(t *testing.T) {
	for _, codeMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "code_mode"}[codeMode], func(t *testing.T) {
			var engine *Engine
			var record domain.Agent
			var project domain.Project
			turns, approvals := 0, 0
			provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				if request.Config.ID == "approver-provider" {
					approvals++
					if approvals == 1 {
						if _, err := engine.SubmitInput(record.ID, "during-batch", "STEERING_AFTER_BATCH", "steer"); err != nil {
							t.Fatal(err)
						}
					}
					return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
				}
				turns++
				if turns == 1 {
					calls := []providers.Call{
						{ID: "one", Name: "exec_command", Arguments: map[string]any{"cmd": "printf first > first.txt"}},
						{ID: "two", Name: "exec_command", Arguments: map[string]any{"cmd": "printf second > second.txt"}},
					}
					if codeMode {
						calls = []providers.Call{{ID: "batch", Name: "code_mode", Arguments: map[string]any{"code": `await tools.exec_command({cmd:"printf first > first.txt"}); await tools.exec_command({cmd:"printf second > second.txt"});`}}}
					}
					return responseCalls(calls...), nil
				}
				if !strings.Contains(JSON(request.Messages), "STEERING_AFTER_BATCH") || strings.Contains(JSON(request.Messages), "tool batch interrupted") {
					t.Fatal(request.Messages)
				}
				return providers.Result{Text: "Done", UsageKnown: true}, nil
			})
			engine, record, project = executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
			if err := engine.Prompt(record.ID, "Write both files"); err != nil {
				t.Fatal(err)
			}
			if final := waitForAgent(t, engine, record.ID); final.Status != "idle" || approvals != 2 || turns != 2 {
				t.Fatal(final, approvals, turns)
			}
			for _, name := range []string{"first.txt", "second.txt"} {
				if _, err := os.Stat(filepath.Join(project.Directory, name)); err != nil {
					t.Fatal("batch did not complete", err)
				}
			}
		})
	}
}

func TestAcceptedInputsRecoverWithoutDuplicatingDeliveredMessages(t *testing.T) {
	engine, record := testEngine(t, nil)
	now := time.Now().UTC()
	input := domain.AgentInput{ID: "recovered-active", AgentID: record.ID, ProjectID: record.ProjectID, Text: "RECOVERED_TASK", Mode: "steer", Status: "active", CreatedAt: now, DeliveredAt: now}
	message := domain.Message{ID: input.ID, AgentID: record.ID, Role: "user", Content: input.Text, CreatedAt: now}
	if err := engine.Store.SaveResources([]datastore.ResourceWrite{
		{Kind: "agent_input", ID: input.ID, Parent: record.ID, Value: input},
		{Kind: "message", ID: input.ID, Parent: record.ID, Value: message},
		{Kind: "instruction", ID: input.ID, Parent: record.ProjectID, Value: message},
	}); err != nil {
		t.Fatal(err)
	}
	queued := domain.AgentInput{ID: "recovered-queued", AgentID: record.ID, ProjectID: record.ProjectID, Text: "RECOVERED_NEXT", Mode: "queue", Status: "queued", CreatedAt: now}
	if err := engine.Store.Save("agent_input", queued.ID, record.ID, queued); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	restarted, err := New(engine.Store, withTestGoalReviewer(providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		call := calls.Add(1)
		if !strings.Contains(JSON(request.Messages), "RECOVERED_TASK") || call == 1 && strings.Contains(JSON(request.Messages), "RECOVERED_NEXT") {
			t.Fatal(request.Messages)
		}
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if final := waitForAgent(t, restarted, record.ID); final.Status != "idle" || calls.Load() != 2 {
		t.Fatal(final, calls.Load())
	}
	history, _ := restarted.loadHistory(record.ID)
	if len(history) != 4 {
		t.Fatal("recovery duplicated a delivered message", history)
	}
}

func TestStopAndServiceShutdownHaveDifferentRecoverySemantics(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		t.Run(map[bool]string{false: "service_shutdown", true: "explicit_stop"}[stopped], func(t *testing.T) {
			started := make(chan struct{})
			engine, record := testEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
				close(started)
				<-ctx.Done()
				return providers.Result{}, ctx.Err()
			}))
			if err := engine.Prompt(record.ID, "Interrupted work"); err != nil {
				t.Fatal(err)
			}
			<-started
			if _, err := engine.SubmitInput(record.ID, "waiting-at-shutdown", "Next task", "queue"); err != nil {
				t.Fatal(err)
			}
			if stopped {
				if err := engine.Stop(record.ID); err != nil {
					t.Fatal(err)
				}
				if final := waitForAgent(t, engine, record.ID); final.Status != "stopped" {
					t.Fatal(final)
				}
			}
			engine.Close()
			if stopped {
				// Simulate a crash after Stop was acknowledged, before the old
				// worker could persist its paused input state.
				inputs, err := engine.Store.AgentInputs(record.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, input := range inputs {
					if input.Status != "paused" {
						continue
					}
					input.Status = "active"
					if err := engine.Store.Save("agent_input", input.ID, record.ID, input); err != nil {
						t.Fatal(err)
					}
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
			if stopped {
				if err := restarted.NotifyAgent(record.ID, "An old background job finished"); err != nil {
					t.Fatal(err)
				}
				var saved domain.Agent
				if err := engine.Store.Load("agent", record.ID, &saved); err != nil || saved.Status != "stopped" || calls.Load() != 0 {
					t.Fatal(saved, calls.Load(), err)
				}
				if err := restarted.ResumeInputs(record.ID); err != nil {
					t.Fatal(err)
				}
			}
			if final := waitForAgent(t, restarted, record.ID); final.Status != "idle" || calls.Load() != 2 {
				t.Fatal(final, calls.Load())
			}
		})
	}
}
