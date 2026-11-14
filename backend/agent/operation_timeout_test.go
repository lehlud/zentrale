package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/providers"
)

func TestFileOperationDeadlineCancelsConnectionWait(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("a stalled pre-approval read must not invoke the provider")
		return providers.Result{}, nil
	}))
	entry := engine.connectionEntry(record.ID)
	entry.mutex.Lock()
	defer entry.mutex.Unlock()
	for _, action := range []string{"read_file", "apply_patch"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, err := engine.file(ctx, record, action, map[string]any{"path": "file.txt"}, false, false)
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), action+" timed out during environment connection") {
					t.Fatalf("missing bounded, phase-specific failure: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("file deadline did not interrupt the connection-state wait")
			}
		})
	}
}

func TestApprovalDeadlineReleasesGateAndRejectsLateDecision(t *testing.T) {
	var calls atomic.Int32
	engine, record := testEngine(t, providerFunction(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > approvalTimeout {
			t.Error("review model received no overall review deadline")
		}
		if calls.Add(1) == 1 {
			<-ctx.Done()
			// Even a provider returning an apparently valid late result must
			// not authorize or execute the reviewed action.
		}
		return providers.Result{Text: `{"allowed":true,"blanket":false}`, UsageKnown: true}, nil
	}))
	request := interaction{Action: "write_file", Arguments: map[string]any{"path": "file.txt", "content": "text"}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := engine.approve(ctx, record, request); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "auto-approval timed out during review model") {
		t.Fatalf("late review did not fail closed: %v", err)
	}
	rows, err := engine.Store.List("approval", &record.ID, 100, 0)
	if err != nil || len(rows) != 0 {
		t.Fatalf("expired review recorded authorization: %v, %v", rows, err)
	}
	if _, err := engine.approve(context.Background(), record, request); err != nil {
		t.Fatalf("expired review retained the family gate: %v", err)
	}
	rows, err = engine.Store.List("approval", &record.ID, 100, 0)
	if err != nil || len(rows) != 1 || rows[0]["allowed"] != true {
		t.Fatalf("subsequent review did not recover: %v, %v", rows, err)
	}
}

func TestApprovalQueueDeadlineDoesNotRunReview(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("queued expired review called the model")
		return providers.Result{}, nil
	}))
	approver, err := engine.familyApprover(record)
	if err != nil {
		t.Fatal(err)
	}
	release, err := engine.acquireReview(context.Background(), approver.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = engine.approve(ctx, record, interaction{Action: "read_file"})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "review queue") {
		t.Fatalf("queue did not return a bounded failure: %v", err)
	}
}

func TestApprovalInspectionDeadlineIdentifiesStalledFileRead(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{UsageKnown: true, Calls: []providers.Call{{
			ID: "inspect-file", Name: "inspect", Arguments: map[string]any{
				"action": "read_file", "arguments": `{"path":"file.txt"}`,
			},
		}}}, nil
	}))
	entry := engine.connectionEntry(record.ID)
	entry.mutex.Lock()
	defer entry.mutex.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := engine.approve(ctx, record, interaction{Action: "apply_patch"})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "review inspection") {
		t.Fatalf("inspection hang was not attributed to the correct phase: %v", err)
	}
}
