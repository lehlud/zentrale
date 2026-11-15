package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestToolConnectionRecoveryKeepsRecordedFailureWithoutReplayingEffects(t *testing.T) {
	var engine *Engine
	var machine domain.Machine
	var agentID string
	var interrupted atomic.Bool
	shell := shellPeerHandler("/bin/sh", os.Environ())
	approve := providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{Text: `{"allowed":true}`, UsageKnown: true}, nil
	})
	instance, record, project := executionTestEngine(t, approve, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		status := shell(command, input, stdout, stderr)
		if status == 0 && strings.Contains(command, "record-once") && interrupted.CompareAndSwap(false, true) {
			// The first command has already run. Make the next inner action lose
			// its connection without discarding that command's completed effects.
			offline := machine
			offline.Port = 1
			if err := engine.Store.Save("machine", offline.ID, "", offline); err != nil {
				t.Error(err)
				return 1
			}
			// Drop the owner receipt; the active command keeps its retained
			// transport until it finishes, but the next action must reconnect.
			engine.releaseConnection(agentID)
		}
		return status
	})
	engine = instance
	agentID = record.ID
	if err := engine.Store.Load("machine", project.MachineIDs[0], &machine); err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = "none"
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := engine.requireConnection(ctx, record.ID); err != nil {
		t.Fatal(err)
	}

	events, unsubscribe := engine.Subscribe()
	defer unsubscribe()
	restored := make(chan error, 1)
	go func() {
		for {
			select {
			case event, open := <-events:
				if !open {
					restored <- fmt.Errorf("event stream closed before the failed call was persisted")
					return
				}
				if event.Type != "message" || !strings.Contains(event.Data, "project connection failed") {
					continue
				}
				// Restore only after the failure has become durable model context.
				restored <- engine.Store.Save("machine", machine.ID, "", machine)
				return
			case <-ctx.Done():
				restored <- ctx.Err()
				return
			}
		}
	}()
	counter := filepath.Join(project.Directory, "effects.txt")
	code := fmt.Sprintf(`
const result = await tools.exec_command({cmd:%s, timeout_ms:5000});
if (result.error) throw new Error(result.error);
await tools.read_file({path:"effects.txt"});
`, JSON("printf record-once >> "+remote.Quote(counter)))
	calls := []providers.Call{
		{ID: "interrupted-call", Name: "code_mode", Arguments: map[string]any{"code": code}},
		{ID: "remaining-call", Name: "harness", Arguments: map[string]any{
			"action": "note_save", "arguments": JSON(map[string]any{"title": "Continued", "content": "The remaining call ran after recovery."}),
		}},
	}
	err := engine.executeCalls(ctx, record, calls)
	if repairError := <-restored; repairError != nil {
		history, _ := engine.loadHistory(record.ID)
		t.Fatalf("repair=%v execution=%v interrupted=%v script=%s history=%v", repairError, err, interrupted.Load(), code, history)
	}
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(counter)
	if err != nil || string(content) != "record-once" {
		t.Fatalf("already executed command was lost or replayed: %q, %v", content, err)
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 2 || !strings.Contains(history[0].Content, "project connection failed") || history[1].CallID != "remaining-call" {
		t.Fatalf("recovery lost its recorded failure or remaining call: %v, %v", history, err)
	}
}

func TestApprovalDenialKeepsOnlyReasonAndCompactReference(t *testing.T) {
	engine, record := testEngine(t, nil)
	_, err := engine.recordApproval(record, interaction{Action: "exec", Arguments: map[string]any{"command": "fixture"}}, approvalDecision{Reason: "The target is outside the task."})
	if err == nil {
		t.Fatal("denial was accepted")
	}
	approvals, readError := engine.listAll("approval", &record.ID)
	if readError != nil || len(approvals) != 1 {
		t.Fatal(approvals, readError)
	}
	id := stringArg(approvals[0], "id")
	want := "auto-approval denied: The target is outside the task. (approvalId: " + id + ")"
	if len(id) != 17 || err.Error() != want || approvals[0]["allowed"] != false {
		t.Fatalf("unexpected denial contract: %v, %v", err, approvals)
	}
}

func TestToolBatchContinuesOrdinaryErrorsButStopsForPermanentConnectionFailures(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		t.Run(fmt.Sprintf("permanent=%t", permanent), func(t *testing.T) {
			approve := providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				return providers.Result{Text: `{"allowed":true}`, UsageKnown: true}, nil
			})
			engine, record, project := executionTestEngine(t, approve, shellPeerHandler("/bin/sh", os.Environ()))
			if permanent {
				var machine domain.Machine
				if err := engine.Store.Load("machine", project.MachineIDs[0], &machine); err != nil {
					t.Fatal(err)
				}
				machine.KeyID = -1 // A missing SSH identity needs configuration, not a retry.
				if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
					t.Fatal(err)
				}
			}
			calls := []providers.Call{
				{ID: "failed-call", Name: "read_file", Arguments: map[string]any{"path": "missing.txt"}},
				{ID: "remaining-call", Name: "harness", Arguments: map[string]any{
					"action": "note_save", "arguments": JSON(map[string]any{"title": "Continued", "content": "Independent work continued."}),
				}},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := engine.executeCalls(ctx, record, calls)
			if (err != nil) != permanent || ctx.Err() != nil {
				t.Fatalf("permanent=%t error=%v context=%v", permanent, err, ctx.Err())
			}
			history, readError := engine.loadHistory(record.ID)
			want := 2
			if permanent {
				want = 1
			}
			if readError != nil || len(history) != want || !strings.Contains(history[0].Content, `"error"`) {
				t.Fatalf("wrong batch continuation: %v, %v", history, readError)
			}
		})
	}
}
