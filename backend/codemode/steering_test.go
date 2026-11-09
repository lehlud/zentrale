package codemode

import (
	"context"
	"errors"
	"testing"
)

func TestInterruptionStopsRemainingCallsEvenWhenScriptCatchesErrors(t *testing.T) {
	calls := 0
	_, err := Run(context.Background(), `try { tools.read({}); } catch {} tools.read({});`, []string{
		"read",
	}, func(string, map[string]any) (any, error) {
		calls++
		return nil, ErrInterrupted
	})
	if !errors.Is(err, ErrInterrupted) || calls != 1 {
		t.Fatal("steering did not stop the batch", calls, err)
	}
}
