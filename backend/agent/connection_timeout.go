package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/lehlud/zentrale/remote"
)

// retryConnectionPreparation gives a contended startup one fresh budget, not an
// unbounded retry loop. Caller cancellation/deadlines and engine shutdown always
// win. This is deliberately not a general tool/network retry mechanism: only
// typed transport failures and managed guest initialization failures qualify.
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
		if !retryablePreparationFailure(err) {
			return err
		}
		if attempt == attempts {
			return fmt.Errorf("project preparation failed after %d attempts: %w", attempts, err)
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

// A background agent must not become permanently idle after its two immediate
// preparation attempts fail. Back off while its run remains active; stopping the
// agent, shutting down the server or a genuine configuration/authentication
// error ends the wait. The callback only prepares an environment, never replays
// an agent command whose effects may already have happened.
func retryConnectionUntilReady(ctx context.Context, delay, maximumDelay time.Duration, prepare func(context.Context) error, waiting func(error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := prepare(ctx)
		if err == nil {
			return ctx.Err()
		}
		if parentError := ctx.Err(); parentError != nil {
			return parentError
		}
		if !retryablePreparationFailure(err) {
			return err
		}
		waiting(err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if delay < maximumDelay {
			delay = min(delay*2, maximumDelay)
		}
	}
}

func retryablePreparationFailure(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var startup *remote.VMStartupFailure
	var interrupted *remote.VMInvocationInterrupted
	var pending *remote.VMHealthPending
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &startup) || errors.As(err, &interrupted) || errors.As(err, &pending) {
		return true
	}
	// SSH authentication, unknown host keys, command exit statuses and ordinary
	// validation errors are not transient simply because their text says so.
	var transport net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) || (errors.As(err, &transport) && transport.Timeout())
}
