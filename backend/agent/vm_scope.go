package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type vmOperation struct {
	gate    chan struct{}
	stopped bool
}

func (operation *vmOperation) Lock(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case operation.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (operation *vmOperation) Unlock() { <-operation.gate }

// A project owns one environment, independent of conversations and workers.
func (engine *Engine) vmOwner(agentID string) (domain.Agent, error) {
	projectID := strings.TrimPrefix(agentID, "project-")
	if projectID == agentID {
		var record domain.Agent
		if err := engine.Store.Load("agent", agentID, &record); err != nil {
			return record, err
		}
		projectID = record.ProjectID
	}
	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil {
		return domain.Agent{}, err
	}
	return domain.Agent{ID: remote.EditorScopeID(projectID, ""), ProjectID: projectID}, nil
}

func (engine *Engine) temporaryDirectory(agentID string) (string, error) {
	owner, err := engine.vmOwner(agentID)
	if err != nil {
		return "", err
	}
	return engine.environmentTemporaryDirectory(owner.ID), nil
}

func (engine *Engine) environmentTemporaryDirectory(scope string) string {
	if strings.HasPrefix(scope, "project-") {
		return "/tmp/zentrale-" + engine.installationID + "-" + scope
	}
	return agentTemporaryDirectory(scope) // Legacy scopes remain addressable for cleanup.
}

func (engine *Engine) vmOperation(agentID, machineID string) *vmOperation {
	if !strings.HasPrefix(agentID, "project-") {
		if owner, err := engine.vmOwner(agentID); err == nil {
			agentID = owner.ID
		}
	}
	value, _ := engine.vmOperations.LoadOrStore(agentID+":"+machineID, &vmOperation{gate: make(chan struct{}, 1)})
	return value.(*vmOperation)
}

func (engine *Engine) vmExecution(execution remote.Execution) (remote.Execution, error) {
	owner, err := engine.vmOwner(execution.AgentID)
	if err != nil {
		return execution, err
	}
	execution.AgentID = owner.ID
	execution.TemporaryDirectory = engine.environmentTemporaryDirectory(owner.ID)
	return execution, nil
}

func (engine *Engine) sandbox(
	ctx context.Context,
	connection *remote.Connection,
	machine domain.Machine,
	execution remote.Execution,
) (string, string, error) {
	return engine.sandboxWithRecovery(ctx, connection, machine, execution, true)
}

// Uploads may prepare/start a VM, but can never authorize destructive recovery.
func (engine *Engine) sandboxWithRecovery(ctx context.Context, connection *remote.Connection, machine domain.Machine, execution remote.Execution, allowRecovery bool) (string, string, error) {
	if err := engine.projectEnvironmentAvailable(execution.ProjectID); err != nil {
		return "", "", err
	}
	execution, err := engine.vmExecution(execution)
	if err != nil {
		return "", "", err
	}
	operation := engine.vmOperation(execution.AgentID, machine.ID)
	if err := operation.Lock(ctx); err != nil {
		return "", "", err
	}
	defer operation.Unlock()
	command, level, err := remote.Sandbox(ctx, connection, machine, execution)
	var recovery *remote.VMRecoveryRequired
	if allowRecovery && errors.As(err, &recovery) {
		if err := engine.RecoverVM(ctx, connection, machine, execution, recovery.Reason); err != nil {
			return "", level, err
		}
		command, level, err = remote.Sandbox(ctx, connection, machine, execution)
	}
	if level == remote.SandboxMicroVM && !execution.OutsideSandbox {
		// Staging can start a guest before failing. Keep it discoverable by idle
		// cleanup, including read-only inspections on an additional machine.
		operation.stopped = false
		recordError := engine.rememberMicroVM(
			domain.Agent{ID: execution.AgentID, ProjectID: execution.ProjectID},
			machine,
			domain.Project{ID: execution.ProjectID, Directory: execution.Workspace},
			level,
		)
		err = errors.Join(err, recordError)
	}
	return command, level, err
}

func (engine *Engine) manageVM(
	ctx context.Context,
	connection *remote.Connection,
	machine domain.Machine,
	action string,
	execution remote.Execution,
) (map[string]any, error) {
	if action == "prepare" {
		if err := engine.projectEnvironmentAvailable(execution.ProjectID); err != nil {
			return nil, err
		}
	}
	execution, err := engine.vmExecution(execution)
	if err != nil {
		return nil, err
	}
	operation := engine.vmOperation(execution.AgentID, machine.ID)
	if err := operation.Lock(ctx); err != nil {
		return nil, err
	}
	defer operation.Unlock()
	if action == "reset" {
		if err := engine.prepareEnvironmentReset(execution.AgentID); err != nil {
			return nil, err
		}
	}
	if action == "prepare" || action == "reset" {
		operation.stopped = false
	}
	result, err := remote.ManageMicroVM(ctx, connection, machine, action, execution)
	var recovery *remote.VMRecoveryRequired
	if action == "prepare" && errors.As(err, &recovery) {
		if err := engine.RecoverVM(ctx, connection, machine, execution, recovery.Reason); err != nil {
			return nil, err
		}
		return remote.ManageMicroVM(ctx, connection, machine, action, execution)
	}
	return result, err
}

func (engine *Engine) agentFamily(owner domain.Agent) (map[string]bool, error) {
	records, err := engine.listAll("agent", &owner.ProjectID)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(owner.ID, "project-") {
		selected := map[string]bool{owner.ID: true}
		for _, record := range records {
			selected[stringArg(record, "id")] = true
		}
		return selected, nil
	}
	selected := map[string]bool{owner.ID: true}
	for changed := true; changed; {
		changed = false
		for _, record := range records {
			id := stringArg(record, "id")
			if selected[stringArg(record, "parentId")] && !selected[id] {
				selected[id], changed = true, true
			}
		}
	}
	return selected, nil
}
