package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/lehlud/zentrale/remote"
)

// One bounded outbox worker; slow/unavailable hosts cannot block API requests.
// Failed cleanup remains durable and retries with a delay instead of spinning.
func (engine *Engine) cleanupDeletedVMs() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-engine.ctx.Done():
			return
		case <-ticker.C:
		}
		records, err := engine.Store.PendingVMCleanup()
		if err != nil {
			continue
		}
		for _, record := range records {
			if engine.ctx.Err() != nil {
				return
			}
			var task struct {
				OwnerID       string    `json:"ownerId"`
				ProjectID     string    `json:"targetProjectId"`
				MachineID     string    `json:"machineId"`
				Workspace     string    `json:"workspace"`
				RetryAt       time.Time `json:"retryAt"`
				Configuration string    `json:"targetConfiguration"`
			}
			if decode(JSON(record), &task) != nil || time.Now().Before(task.RetryAt) {
				continue
			}
			id := stringArg(record, "id")
			// An administrator can remove the remote configuration altogether.
			// Never reinterpret it as a different host or attempt another route.
			machine, err := engine.Remote.Connector.Machine(task.MachineID)
			if err == nil {
				kind, configID := "machine", task.MachineID
				if prefix, _, found := strings.Cut(task.MachineID, "/"); found {
					kind, configID = "pool", prefix
				}
				var current, original map[string]any
				if engine.Store.Load(kind, configID, &current) != nil || decode(task.Configuration, &original) != nil || JSON(current) != JSON(original) {
					err = errors.New("remote configuration changed since deletion; review pending VM cleanup")
				}
			}
			if err != nil {
				slog.Warn("VM cleanup target unavailable; retaining cleanup record", "machine", task.MachineID, "owner", task.OwnerID, "error", err)
				_ = engine.Store.Patch("vm_cleanup", id, map[string]any{"retryAt": time.Now().Add(time.Hour), "error": err.Error()})
				continue
			}
			ctx, cancel := context.WithTimeout(engine.ctx, 10*time.Second)
			operation := engine.vmOperation(task.OwnerID, task.MachineID)
			err = operation.Lock(ctx)
			if err == nil {
				connection, dialErr := engine.Remote.Connector.Dial(ctx, task.MachineID)
				err = dialErr
				if err == nil {
					_, err = remote.ManageMicroVM(ctx, connection, machine, "remove", remote.Execution{AgentID: task.OwnerID, ProjectID: task.ProjectID, Workspace: task.Workspace, TemporaryDirectory: engine.environmentTemporaryDirectory(task.OwnerID)})
					connection.Close()
				}
				operation.Unlock()
			}
			cancel()
			if err == nil {
				_ = engine.Store.Remove("vm_cleanup", id)
			} else if engine.ctx.Err() == nil {
				slog.Warn("VM cleanup deferred", "machine", task.MachineID, "owner", task.OwnerID, "error", err)
				_ = engine.Store.Patch("vm_cleanup", id, map[string]any{"retryAt": time.Now().Add(time.Minute), "error": err.Error()})
			}
		}
	}
}
