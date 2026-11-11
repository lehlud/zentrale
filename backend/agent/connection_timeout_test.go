package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/remote"
)

func TestConnectionPreparationRetriesWrappedDeadlineWithFreshBudget(t *testing.T) {
	calls := 0
	var first context.Context
	err := retryConnectionPreparation(context.Background(), time.Second, 0, func(ctx context.Context) error {
		calls++
		if calls == 1 {
			first = ctx
			return fmt.Errorf("inspect project repositories: %w", context.DeadlineExceeded)
		}
		if first.Err() == nil || ctx.Err() != nil {
			t.Fatal("retry must cancel the old attempt and supply a fresh context")
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationRetriesGuestInstallOnTheSameEnvironment(t *testing.T) {
	for _, reason := range []string{"package_install_timeout", "package_install_failed", "startup_timeout"} {
		t.Run(reason, func(t *testing.T) {
			calls := 0
			err := retryConnectionPreparation(context.Background(), time.Second, 0, func(context.Context) error {
				calls++
				if calls == 1 {
					return fmt.Errorf("prepare repository sandbox: %w", &remote.VMStartupFailure{Reason: reason})
				}
				return nil
			})
			if err != nil || calls != 2 {
				t.Fatalf("calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestRepeatedGuestSetupFailureRetainsDiagnosticsAndStopsRetrying(t *testing.T) {
	calls := 0
	err := retryConnectionPreparation(context.Background(), time.Second, 0, func(context.Context) error {
		calls++
		return &remote.VMStartupFailure{Reason: "package_install_failed", Diagnostic: "RPM transaction failed"}
	})
	var failure *remote.VMStartupFailure
	if calls != 2 || !errors.As(err, &failure) || !strings.Contains(err.Error(), "RPM transaction failed") {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationDoesNotRetryPermanentFailures(t *testing.T) {
	for _, failure := range []error{
		os.ErrPermission,
		errors.New("ssh: unable to authenticate"),
		errors.New("untrusted SSH host key"),
		errors.New("inspect project repositories: context deadline exceeded"),
		errors.New("connection reset by peer"),
		context.Canceled,
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			calls := 0
			err := retryConnectionPreparation(context.Background(), time.Second, 0, func(context.Context) error {
				calls++
				return failure
			})
			if err != failure || calls != 1 {
				t.Fatalf("permanent failure retried or replaced: calls=%d, error=%v", calls, err)
			}
		})
	}
}

func TestConnectionPreparationTimeoutIsBounded(t *testing.T) {
	calls := 0
	err := retryConnectionPreparation(context.Background(), time.Millisecond, 0, func(ctx context.Context) error {
		calls++
		<-ctx.Done()
		return fmt.Errorf("inspect project repositories: %w", ctx.Err())
	})
	if calls != 2 || !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "after 2 attempts") || !strings.Contains(err.Error(), "inspect project repositories") {
		t.Fatalf("lost bounded timeout diagnostics: calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationHonorsCallerDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	calls := 0
	err := retryConnectionPreparation(ctx, time.Minute, 0, func(attempt context.Context) error {
		calls++
		<-attempt.Done()
		return attempt.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 1 {
		t.Fatalf("caller deadline was extended: calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationCancellationInterruptsBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	failed := make(chan struct{})
	finished := make(chan error, 1)
	calls := 0
	go func() {
		finished <- retryConnectionPreparation(ctx, time.Minute, time.Hour, func(context.Context) error {
			calls++
			close(failed)
			return context.DeadlineExceeded
		})
	}()
	<-failed
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("cancellation retried: calls=%d, error=%v", calls, err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt retry backoff")
	}
}

func TestConnectionPreparationAlreadyCanceledDoesNotStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryConnectionPreparation(ctx, time.Minute, 0, func(context.Context) error {
		t.Fatal("preparation started after cancellation")
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestConnectionPreparationSuccessfulAttemptDoesNotRetry(t *testing.T) {
	calls := 0
	err := retryConnectionPreparation(context.Background(), time.Minute, 0, func(context.Context) error {
		calls++
		return nil
	})
	if err != nil || calls != 1 {
		t.Fatalf("calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationCanceledSuccessIsNotConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := retryConnectionPreparation(ctx, time.Minute, 0, func(context.Context) error {
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup reported success: %v", err)
	}
}

func TestConnectionPreparationContendedSourceWaitCanRecover(t *testing.T) {
	// Model another child's source-preparation semaphore consuming the first
	// attempt's deadline. Its completed checkout must be inspectable on retry
	// using a fresh budget, not the expired first attempt's context.
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	calls := 0
	err := retryConnectionPreparation(context.Background(), time.Millisecond, 0, func(ctx context.Context) error {
		calls++
		if calls == 2 {
			<-lock // The sibling completed preparation between attempts.
		}
		select {
		case lock <- struct{}{}:
			defer func() { <-lock }()
			return ctx.Err()
		case <-ctx.Done():
			return fmt.Errorf("inspect project repositories: %w", ctx.Err())
		}
	})
	if err != nil || calls != 2 {
		t.Fatalf("contended startup did not recover: calls=%d, error=%v", calls, err)
	}
}

func TestConnectionPreparationExpiredAttemptCannotReportSuccess(t *testing.T) {
	calls := 0
	err := retryConnectionPreparation(context.Background(), time.Millisecond, 0, func(ctx context.Context) error {
		calls++
		<-ctx.Done()
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) || calls != 2 {
		t.Fatalf("expired startup reported success: calls=%d, error=%v", calls, err)
	}
}
