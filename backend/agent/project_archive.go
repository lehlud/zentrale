package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// ArchiveProject freezes the project, verifies every workspace before any
// deletion, then commits a read-only archive. Receipts survive interrupted
// cleanup so the owner can retry without recreating environments.
func (engine *Engine) ArchiveProject(ctx context.Context, id, ownerID string) error {
	unlock, err := engine.projectGates.LockContext(ctx, id)
	if err != nil {
		return err
	}
	defer unlock()

	project, family, release, err := engine.reserveArchive(id, ownerID)
	if err != nil || release == nil {
		return err
	}
	defer release()
	defer engine.Emit("resources", "", nil)

	directories, err := engine.Remote.ArchiveDirectories(id)
	if err != nil {
		return err
	}
	token, err := engine.Store.ProjectWorkspaceToken(id)
	if err != nil {
		return err
	}

	if !project.Archived {
		for _, directory := range directories {
			if err := engine.archiveDirectory(ctx, id, token, directory, false); err != nil {
				return err
			}
		}
	}

	vms, err := engine.archiveVMReceipts(id)
	if err != nil {
		return err
	}
	if !project.Archived {
		for _, vm := range vms {
			machine, err := engine.Remote.Connector.Machine(vm.MachineID)
			if err != nil || machine.MicroVMCommand != "" {
				return errors.New("restore the configured VM hosts before archiving this project")
			}
		}
		project.Archived, project.ArchivePending = true, true
		if err := engine.Store.Save("project", id, "", project); err != nil {
			return err
		}
	}

	for agentID := range family {
		engine.invalidate(agentID)
		engine.closeMCPServers(agentID)
		engine.releaseConnection(agentID)
	}
	if err := engine.retireArchive(ctx, id, token, directories, vms); err != nil {
		return fmt.Errorf("project archived, but remote cleanup must be retried: %w", err)
	}

	project.ArchivePending = false
	return engine.Store.Save("project", id, "", project)
}

func (engine *Engine) reserveArchive(id, ownerID string) (domain.Project, map[string]bool, func(), error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	var project domain.Project
	if err := engine.Store.Load("project", id, &project); err != nil || project.Owner() != ownerID {
		return project, nil, nil, errors.New("project owner required")
	}
	if project.Archived && !project.ArchivePending {
		return project, nil, nil, nil
	}
	if !project.ManagedWorkspace {
		return project, nil, nil, errors.New("only managed workspaces can be archived safely")
	}
	if !project.Archived {
		if err := engine.projectAvailable(id); err != nil {
			return project, nil, nil, err
		}
	}

	family, err := engine.agentFamily(domain.Agent{ID: remote.EditorScopeID(id, ""), ProjectID: id})
	if err != nil {
		return project, nil, nil, err
	}
	terminal, err := engine.Store.HasTerminalAttachment(id, "")
	if err != nil {
		return project, nil, nil, err
	}
	if terminal || engine.agentsAreBusy(family) || engine.Editors.HasActive(family, "") {
		return project, nil, nil, errors.New("finish project work and close editors and terminals before archiving")
	}

	finish, err := engine.reserveProject(project)
	if err != nil {
		return project, nil, nil, err
	}
	engine.setProjectMoving(id, true)
	return project, family, func() {
		engine.setProjectMoving(id, false)
		finish()
	}, nil
}

func (engine *Engine) archiveVMReceipts(id string) (map[string]microVMRecord, error) {
	rows, err := engine.Store.ProjectMicroVMs(id)
	if err != nil {
		return nil, err
	}
	records := make(map[string]microVMRecord, len(rows))
	for key, raw := range rows {
		var vm microVMRecord
		if err := json.Unmarshal(raw, &vm); err != nil {
			return nil, err
		}
		records[key] = vm
	}
	return records, nil
}

func (engine *Engine) archiveDirectory(ctx context.Context, id, token string, directory remote.MoveDirectory, remove bool) error {
	for _, host := range slices.Sorted(maps.Keys(directory.Paths)) {
		conn, err := engine.dialMoveHost(ctx, host)
		if err != nil {
			return fmt.Errorf("connect to project storage on %s: %w", host, err)
		}
		workspace := directory.Paths[host]
		if remove {
			_, err = remote.MoveDirectoryState(ctx, conn, "archive-remove", id, workspace, token, "archive")
		} else {
			_, err = remote.MoveDirectoryState(ctx, conn, "check-owned", id, workspace, token, "archive")
			if err == nil {
				err = engine.checkArchiveGit(ctx, conn, host, id, workspace)
			}
		}
		conn.Close()
		if err != nil {
			return fmt.Errorf("verify or retire project data on %s: %w", host, err)
		}
	}
	return nil
}

func (engine *Engine) checkArchiveGit(ctx context.Context, conn *remote.Connection, host, id, workspace string) error {
	machine, err := engine.Remote.Connector.Machine(host)
	if err != nil {
		return err
	}
	machine = remote.WithMachineDefaults(machine)
	if machine.MicroVMCommand != "" || machine.Sandbox == remote.SandboxDisabled {
		return errors.New("archive checks require an isolated workspace with the built-in sandbox")
	}

	scope := remote.EditorScopeID(id, "")
	execution := remote.Execution{
		AgentID: scope, ProjectID: id, Workspace: workspace,
		TemporaryDirectory: engine.environmentTemporaryDirectory(scope),
		Command:            remote.ArchiveGitCommand(),
	}
	setup, cancel := context.WithTimeout(ctx, remote.ManagedVMSetupTimeout)
	command, _, err := engine.prepareSandbox(setup, conn, machine, execution, engine.vmOperation(scope, host), false)
	cancel()
	if err != nil {
		return err
	}
	check, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_, err = remote.Run(check, conn, command)
	return err
}

func (engine *Engine) retireArchive(ctx context.Context, id, token string, directories []remote.MoveDirectory, vms map[string]microVMRecord) error {
	hosts := map[string]bool{}
	for _, directory := range directories {
		for host := range directory.Paths {
			hosts[host] = true
		}
	}
	for _, vm := range vms {
		if err := engine.removeMovedVM(ctx, vm); err != nil {
			return err
		}
		hosts[vm.MachineID] = true
	}
	for _, host := range slices.Sorted(maps.Keys(hosts)) {
		conn, err := engine.dialMoveHost(ctx, host)
		if err != nil {
			return err
		}
		err = remote.RemoveProjectMoveScratch(ctx, conn, id)
		conn.Close()
		if err != nil {
			return err
		}
	}

	for _, directory := range directories {
		if err := engine.archiveDirectory(ctx, id, token, directory, true); err != nil {
			return err
		}
		if err := engine.Store.Remove(directory.Kind, directory.BindingID); err != nil {
			return err
		}
	}
	for key := range vms {
		if err := engine.Store.Remove("microvm", key); err != nil {
			return err
		}
	}
	return nil
}
