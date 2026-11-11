package agent

import (
	"context"
	"crypto/rand"
	"errors"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) editorSelection(projectID, agentID string) (domain.Project, domain.Agent, error) {
	var project domain.Project
	var selected domain.Agent

	if err := engine.Store.Load("project", projectID, &project); err != nil {
		return project, selected, err
	}

	if agentID != "" {
		if err := engine.Store.Load("agent", agentID, &selected); err != nil {
			return project, selected, err
		}

		if selected.ProjectID != projectID || selected.Role == "approver" {
			return project, selected, errors.New("select a working agent from this project for VS Code")
		}

		selected, err := engine.vmOwner(selected.ID)
		return project, selected, err
	}

	records, err := engine.listAll("agent", &projectID)
	if err != nil {
		return project, selected, err
	}

	for _, record := range records {
		if stringArg(record, "role") != "approver" && stringArg(record, "parentId") == "" {
			if err := decode(JSON(record), &selected); err != nil {
				return project, selected, err
			}

			break
		}
	}

	return project, selected, nil
}

func (engine *Engine) CurrentEditor(projectID, agentID string) (domain.EditorSession, error) {
	project, selected, err := engine.editorSelection(projectID, agentID)
	if err != nil {
		return domain.EditorSession{}, err
	}

	return engine.Editors.CurrentFor(project, selected.ID), nil
}

// Starting an editor is an explicit user action. Provisioning and package
// installation stay in the same managed guest used by subsequent agent tools.
func (engine *Engine) StartEditor(ctx context.Context, projectID, agentID string) (domain.EditorSession, error) {
	return engine.withEditorEnvironment(ctx, projectID, agentID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, project domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
		return engine.Editors.StartFor(ctx, project, target)
	})
}

// withEditorEnvironment shares source/credential preparation and holds the
// existing project, agent and VM activity guards until use returns. Terminal
// callers deliberately keep use running until the PTY has been closed.
func (engine *Engine) withEditorEnvironment(ctx context.Context, projectID, agentID string, use func(context.Context, *remote.Connection, domain.Machine, domain.Project, remote.EditorTarget) (domain.EditorSession, error)) (domain.EditorSession, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	engine.configMu.Lock()
	engine.mu.Lock()

	if err := engine.ctx.Err(); err != nil {
		engine.mu.Unlock()
		engine.configMu.Unlock()

		return domain.EditorSession{}, err
	}

	// Provisioning happens before the editor supervisor exists. Include it in
	// shutdown so no SSH preparation or database write outlives the service.
	engine.workers.Add(1)
	engine.mu.Unlock()
	defer engine.workers.Done()

	project, selected, err := engine.editorSelection(projectID, agentID)
	if err != nil {
		engine.configMu.Unlock()
		return domain.EditorSession{}, err
	}

	engine.mu.Lock()
	engine.editorPreparing[projectID]++

	if selected.ID != "" {
		engine.preparing[selected.ID]++
	} else {
		engine.preparing[remote.EditorScopeID(projectID, "")]++
	}

	engine.mu.Unlock()
	engine.configMu.Unlock()
	defer func() {
		engine.mu.Lock()
		engine.editorPreparing[projectID]--

		if engine.editorPreparing[projectID] == 0 {
			delete(engine.editorPreparing, projectID)
		}

		scope := remote.EditorScopeID(projectID, selected.ID)
		engine.preparing[scope]--
		if engine.preparing[scope] == 0 {
			delete(engine.preparing, scope)
		}

		engine.mu.Unlock()
		engine.scheduleVMIdleCheck(remote.EditorScopeID(projectID, selected.ID))
	}()

	identity, err := engine.ProjectCommitIdentity(project)
	if err != nil {
		return domain.EditorSession{}, err
	}

	var connection *remote.Connection
	var machine domain.Machine
	scope := remote.EditorScopeID(project.ID, selected.ID)
	leaseOwner := selected.ID

	if selected.ID != "" {
		connection, machine, project, err = engine.connect(ctx, selected)
	} else {
		owner := "ide-prepare:" + scope + ":" + rand.Text()
		leaseOwner = owner
		defer engine.Remote.Release(owner)

		connection, machine, err = engine.Remote.Connect(ctx, project, owner, "", nil)
		if err == nil {
			project, err = engine.Remote.ResolveProject(ctx, connection, machine, project)
			if err != nil {
				connection.Close()
			}
		}
	}

	if err != nil {
		return domain.EditorSession{}, err
	}
	defer connection.Close()
	// Cached agent connections may expire independently while a terminal is
	// still open. Retain the selected pool/GPU lease for this whole operation.
	lease := "workspace:" + rand.Text()
	engine.Remote.Hold(leaseOwner, lease)
	defer engine.Remote.Release("job:" + lease)

	if len(machine.Storage) > 0 && len(machine.StorageMounts) == 0 {
		machine.StorageMounts, err = engine.Remote.PrepareStorage(ctx, connection, machine, project)
		if err != nil {
			return domain.EditorSession{}, err
		}
	}

	if len(machine.GPUDevices) > 0 && selected.ID == "" {
		return domain.EditorSession{}, errors.New("create a conversation before starting a GPU-backed editor")
	}

	if err := remote.CheckRuntime(ctx, connection); err != nil {
		return domain.EditorSession{}, err
	}

	temporary := agentTemporaryDirectory(scope)
	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporary); err != nil {
		return domain.EditorSession{}, err
	}

	status := remote.InspectSandbox(ctx, connection, machine)
	if status.Error == "" && status.Level == remote.SandboxMicroVM && machine.MicroVMCommand == "" {
		if err := engine.rememberMicroVM(domain.Agent{ID: scope, ProjectID: project.ID}, machine, project, status.Level); err != nil {
			return domain.EditorSession{}, err
		}
		_, err = engine.manageVM(ctx, connection, machine, "prepare", remote.Execution{
			AgentID:            scope,
			ProjectID:          project.ID,
			Workspace:          project.Directory,
			TemporaryDirectory: temporary,
		})
		if err != nil {
			status.Error = err.Error()
		}
	}

	if err := engine.rememberSandboxStatus(machine, status); err != nil {
		return domain.EditorSession{}, err
	}

	if status.Error != "" {
		return domain.EditorSession{}, errors.New(status.Error)
	}

	if err := engine.prepareProjectSources(ctx, connection, machine, project, scope); err != nil {
		return domain.EditorSession{}, err
	}

	return use(
		ctx,
		connection,
		machine,
		project,
		remote.EditorTarget{
			AgentID:            selected.ID,
			MachineID:          machine.ID,
			CommitIdentity:     identity,
			RuntimeLock:        engine.vmOperation(scope, machine.ID),
			Recovery:           engine,
			TemporaryDirectory: temporary,
		},
	)
}
