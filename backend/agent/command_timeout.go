package agent

import (
	"errors"
	"time"
)

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
