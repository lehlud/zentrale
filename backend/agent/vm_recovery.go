package agent

import (
	"context"
	"fmt"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// RecoverVM is a harness operation, never an agent tool. The caller holds the
// VM lifecycle lock so no new invocation can start before recovery finishes.
func (engine *Engine) RecoverVM(
	ctx context.Context,
	connection *remote.Connection,
	machine domain.Machine,
	execution remote.Execution,
	reason string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	owner, err := engine.vmOwner(execution.AgentID)
	if err != nil {
		return err
	}
	family, err := engine.agentFamily(owner)
	if err != nil {
		return err
	}
	machines := make(map[string]string, len(family))
	for id := range family {
		var member domain.Agent
		if engine.Store.Load("agent", id, &member) == nil {
			machines[id] = member.MachineID
		}
	}

	var interrupted []*runningJob
	var working []string
	engine.mu.Lock()
	for id := range family {
		if engine.running[id] != nil && (machines[id] == "" || machines[id] == machine.ID) {
			working = append(working, id)
		}
	}
	for _, running := range engine.jobs {
		running.mu.Lock()
		if family[running.job.AgentID] && running.job.MachineID == machine.ID && running.sandbox == remote.SandboxMicroVM {
			running.terminationReason = "the managed environment was automatically recovered; saved files and installed packages were retained"
			interrupted = append(interrupted, running)
		}
		running.mu.Unlock()
	}
	engine.mu.Unlock()

	for _, id := range working {
		engine.setRunState(id, "recovering_sandbox", nil)
	}
	engine.Emit("environment_recovery", owner.ID, map[string]any{"machineId": machine.ID, "reason": reason})
	for _, running := range interrupted {
		running.cancel()
	}

	if err := engine.Editors.StopEnvironment(family, machine.ID, execution.EditorSessionID); err != nil {
		return err
	}
	engine.closeMCPServersMatching(func(server *mcpServer) bool {
		return family[server.agentID] && server.info.MachineID == machine.ID
	})

	// Stop releases processes and devices, preserving the existing guest disk.
	// The caller retries prepare/stage once, which starts that disk automatically.
	if _, err := remote.ManageMicroVM(ctx, connection, machine, "stop", execution); err != nil {
		return fmt.Errorf("recover managed environment: %w", err)
	}
	return nil
}
