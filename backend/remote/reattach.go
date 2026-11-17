package remote

import (
	"context"
	"errors"
	"time"
)

// Reattach waits for the original route after a Zentrale host restart. It
// never chooses another host and never retries a trust/configuration failure.
func (connector *Connector) Reattach(ctx context.Context, machineID string) (*Connection, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	for delay := 250 * time.Millisecond; ; delay = min(delay*2, 5*time.Second) {
		if _, err := connector.Machine(machineID); err != nil {
			return nil, err
		}
		connection, err := connector.Dial(ctx, machineID)
		if err == nil {
			return connection, nil
		}
		var hostKey *HostKeyError
		var poolKey *PoolHostKeyError
		var daemonError *DaemonError
		if errors.As(err, &hostKey) || errors.As(err, &poolKey) || errors.As(err, &daemonError) {
			return nil, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
