package agent

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// retryConnectionPreparation gives a contended startup one fresh budget, not an
// unbounded retry loop. Caller cancellation/deadlines and engine shutdown always
// win. This is deliberately not a general tool/network retry mechanism: only
// typed deadline errors from harness-owned connection preparation qualify.
func retryConnectionPreparation(ctx context.Context, budget, backoff time.Duration, prepare func(context.Context) error) error {
	const attempts = 2
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		bounded, cancel := context.WithTimeout(ctx, budget)
		err := prepare(bounded)
		if err == nil {
			err = bounded.Err()
		}
		cancel()
		if err == nil {
			return ctx.Err()
		}
		if parentError := ctx.Err(); parentError != nil {
			return parentError
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if attempt == attempts {
			return fmt.Errorf("project preparation timed out after %d attempts: %w", attempts, err)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	panic("unreachable")
}
