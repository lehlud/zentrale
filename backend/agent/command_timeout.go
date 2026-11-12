package agent

import (
	"context"
	"errors"
	"time"
)

type commandLifetimeKey struct{}

func requiresCommandTimeout(action string) bool {
	switch action {
	case "exec", "python", "venv_create", "container", "packages_install", "source_git", "clone_source":
		return true
	default:
		return false
	}
}

func withCommandLifetime(ctx context.Context, request interaction) (context.Context, error) {
	if !requiresCommandTimeout(request.Action) {
		return ctx, nil
	}
	lifetime, err := commandTimeout(request.Arguments)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, commandLifetimeKey{}, lifetime), nil
}

// This is a command lifetime, not the time to wait before returning a job ID.
// Omission is rejected for new agent calls; internal/legacy execution requests
// retain the finite runtime fallback in startJob.
func commandTimeout(arguments map[string]any) (time.Duration, error) {
	if _, present := arguments["timeout_ms"]; !present {
		return 0, errors.New("timeout_ms is required; choose a finite lifetime appropriate for this command")
	}
	value, err := outputInteger(arguments, "timeout_ms", 0, 1, 604800000)
	if err != nil {
		return 0, err
	}
	return time.Duration(value) * time.Millisecond, nil
}
