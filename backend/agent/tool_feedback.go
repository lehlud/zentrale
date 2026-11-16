package agent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
)

type toolFeedbackKey struct{}
type toolFeedback struct {
	mutex       sync.Mutex
	fingerprint [32]byte
	count       int
}

func withToolFeedback(ctx context.Context) context.Context {
	return context.WithValue(ctx, toolFeedbackKey{}, &toolFeedback{})
}

// Only local argument-validation failures prove that execution did not start.
// Unknown remote failures never become permission to repeat a mutation.
func structuralToolFeedback(ctx context.Context, request interaction, failure error) error {
	state, _ := ctx.Value(toolFeedbackKey{}).(*toolFeedback)
	if state == nil {
		return failure
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if failure == nil {
		state.count = 0
		return nil
	}
	fingerprint := sha256.Sum256([]byte(JSON(request)))
	if state.count == 0 || state.fingerprint != fingerprint {
		state.fingerprint, state.count = fingerprint, 0
	}
	state.count++
	if state.count < 2 {
		return failure
	}
	return fmt.Errorf("%w; repeated invalid request (%d times). Validation stopped this call before execution or approval. Change the arguments using the error/tool help; do not repeat the unchanged request", failure, state.count)
}
