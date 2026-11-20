package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) projectEnvironmentAvailable(projectID string) error {
	engine.mu.Lock()
	resetting := engine.resettingProjects[projectID]
	moving := engine.movingProjects[projectID]
	engine.mu.Unlock()
	if moving {
		return errors.New("this project is moving; wait for the move to finish")
	}
	if resetting {
		return errors.New("the user is resetting this project's VMs; wait until the reset finishes")
	}
	return nil
}

// ResetProjectVM is exclusively an authenticated user action. A pool can own a
// project VM on several machines; reset the recorded VMs on their actual hosts.
// Never choose a new pool member just to perform a destructive operation.
func (engine *Engine) ResetProjectVM(ctx context.Context, projectID string) error {
	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil {
		return err
	}
	engine.configMu.Lock()
	if err := engine.projectAvailable(projectID); err != nil {
		engine.configMu.Unlock()
		return err
	}
	engine.mu.Lock()
	if engine.resettingProjects == nil {
		engine.resettingProjects = make(map[string]bool)
	}
	if engine.resettingProjects[projectID] {
		engine.mu.Unlock()
		engine.configMu.Unlock()
		return errors.New("project VM reset is already running")
	}
	engine.resettingProjects[projectID] = true
	engine.mu.Unlock()
	engine.configMu.Unlock()
	defer func() { engine.mu.Lock(); delete(engine.resettingProjects, projectID); engine.mu.Unlock() }()
	scope := remote.EditorScopeID(projectID, "")
	release := engine.useEnvironment(scope)
	defer release()
	if err := engine.prepareEnvironmentReset(scope); err != nil {
		return err
	}
	records, err := engine.listAll("microvm", &projectID)
	if err != nil {
		return err
	}
	var failures []error
	for _, record := range records {
		machineID := stringArg(record, "machineId")
		machine, err := engine.Remote.Connector.Machine(machineID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		operation := engine.vmOperation(scope, machineID)
		if err := operation.Lock(ctx); err != nil {
			return err
		}
		connection, err := engine.Remote.Connector.Dial(ctx, machineID)
		if err == nil {
			_, err = remote.ManageMicroVM(ctx, connection, machine, "reset", remote.Execution{
				AgentID: scope, ProjectID: projectID, Workspace: stringArg(record, "workspace"), TemporaryDirectory: engine.environmentTemporaryDirectory(scope),
			})
			connection.Close()
		}
		operation.stopped = true
		operation.Unlock()
		if err != nil {
			failures = append(failures, fmt.Errorf("reset project VM on %s: %w", machineID, err))
		}
	}
	users, err := engine.agentFamily(domain.Agent{ID: scope, ProjectID: projectID})
	if err != nil {
		return err
	}
	for id := range users {
		engine.invalidate(id)
		if entry := engine.connectionEntry(id); entry != nil {
			entry.mutex.Lock()
			entry.ready = nil
			entry.sourcesKey = ""
			entry.mutex.Unlock()
		}
	}
	engine.Emit("environment_reset", "", map[string]string{"projectId": projectID})
	return errors.Join(failures...)
}
