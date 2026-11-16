package codemode

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHostToolWaitsDoNotConsumeJavaScriptBudget(t *testing.T) {
	// Compile the shared WASM module before testing a short execution budget.
	if _, err := Run(context.Background(), "print(1)", []string{}, nil); err != nil {
		t.Fatal(err)
	}

	calls := 0
	result, err := run(context.Background(), `
print(await tools.read({}));
print(await tools.read({}));
`, []string{"read"}, func(ctx context.Context, _ string, _ map[string]any) (any, error) {
		calls++
		select {
		case <-time.After(300 * time.Millisecond):
			return "completed", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, 250*time.Millisecond, 3*time.Second)
	if err != nil || calls != 2 || result.Output != "completed\ncompleted\n" {
		t.Fatal("host waits exhausted the JavaScript budget or lost a completed result", result, calls, err)
	}
}

func TestBatchDeadlineAndCancellationReachHostToolsAndStopFurtherCalls(t *testing.T) {
	_, _ = Run(context.Background(), "print(1)", []string{}, nil)
	for _, cancelParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "batch deadline", true: "parent cancellation"}[cancelParent], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			start := time.Now()
			result, err := run(ctx, `try { tools.write({}); } catch {} tools.write({});`, []string{"write"}, func(ctx context.Context, _ string, _ map[string]any) (any, error) {
				calls++
				if cancelParent {
					cancel()
				}

				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Second):
					t.Error("host operation outlived its batch cancellation")
					return nil, nil
				}
			}, time.Second, 100*time.Millisecond)
			want := context.DeadlineExceeded
			if cancelParent {
				want = context.Canceled
			}

			if !errors.Is(err, want) || calls != 1 || result.Calls != 1 || time.Since(start) > time.Second {
				t.Fatal("cancellation lost or a later tool was executed", result, calls, err)
			}
		})
	}
}

func TestJavaScriptBudgetStillStopsAnInfiniteLoopAfterAHostWait(t *testing.T) {
	_, _ = Run(context.Background(), "print(1)", []string{}, nil)
	result, err := run(context.Background(), `tools.read({}); while(true) {}`, []string{"read"}, func(context.Context, string, map[string]any) (any, error) {
		time.Sleep(100 * time.Millisecond)
		return "done", nil
	}, 100*time.Millisecond, 3*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) || result.Calls != 1 {
		t.Fatal("JavaScript timeout was disabled after a tool returned", result, err)
	}
}
