package codemode

import (
	"context"
	"testing"
	"time"
)

func TestGuestTimersCannotBlockCancellation(t *testing.T) {
	// Compile the module before measuring cancellation of a guest timer.
	_, _ = Run(context.Background(), "print(1)", nil, nil)
	for _, source := range []string{
		`const os = await import("qjs:os"); os.sleep(400);`,
		`const os = await import("qjs:os"); await new Promise(resolve => os.setTimeout(resolve,400));`,
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, _ = Run(ctx, source, nil, nil)
		cancel()
		if time.Since(start) > 250*time.Millisecond {
			t.Fatal("guest timer delayed cancellation", time.Since(start))
		}
	}
}
