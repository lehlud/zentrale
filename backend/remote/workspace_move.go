package remote

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

//go:embed scripts/workspace_move.py
var workspaceMoveScript string

type MoveDirectory struct {
	Kind      string            `json:"kind"`
	BindingID string            `json:"bindingId"`
	MachineID string            `json:"machineId"`
	Path      string            `json:"path"`
	Paths     map[string]string `json:"paths,omitempty"`
}

func workspaceMoveCommand(operation, projectID, path, token, id string) string {
	return "python3 -I -c " + Quote(workspaceMoveScript) + " " + Quote(operation) + " " + Quote("project-"+projectID) + " " + Quote(path) + " " + Quote(token) + " " + Quote(id)
}

func MoveDirectoryState(ctx context.Context, conn *Connection, operation, projectID, path, token, id string) (string, error) {
	output, err := Run(ctx, conn, workspaceMoveCommand(operation, projectID, path, token, id))
	return strings.TrimSpace(output), err
}

// TransferWorkspace streams the archive over the two existing SSH transports.
// The final rename is allowed only after the sender and receiver both succeed.
func TransferWorkspace(ctx context.Context, source, destination *Connection, projectID, from, to, token, id string) error {
	if err := transferMoveStream(ctx, source, destination,
		workspaceMoveCommand("pack", projectID, from, token, id),
		workspaceMoveCommand("extract", projectID, to, token, id)); err != nil {
		return err
	}
	_, err := MoveDirectoryState(ctx, destination, "publish", projectID, to, token, id)
	return err
}

func transferMoveStream(ctx context.Context, source, destination *Connection, sender, recipient string) error {
	process, err := StartProcess(ctx, source, sender)
	if err != nil {
		return err
	}
	defer process.Close()

	receiver, err := StartProcess(ctx, destination, recipient)
	if err != nil {
		return err
	}
	defer receiver.Close()
	if _, err := io.Copy(receiver, process.reader); err != nil {
		return fmt.Errorf("transfer project files: %w", err)
	}
	if err := receiver.CloseWrite(); err != nil {
		return err
	}
	for _, endpoint := range []*Process{process, receiver} {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-endpoint.done:
			if endpoint.exitError != nil {
				return commandFailure(endpoint.exitError, endpoint.stderr.String())
			}
		}
	}

	return nil
}

// MoveDirectories enumerates recorded project-owned storage on the old target.
// Pool-shared bindings are represented once; independently allocated copies
// of a volume are kept separate so a move cannot silently overwrite one.
func (manager *Manager) MoveDirectories(project domain.Project) (map[string]MoveDirectory, error) {
	candidates, err := manager.projectCandidates(project)
	if err != nil {
		return nil, err
	}
	machines := make(map[string]domain.Machine, len(candidates))
	byStorage := map[string]string{}
	byWorkspace := map[string]bool{}
	for _, candidate := range candidates {
		machine, err := manager.Connector.Machine(candidate.id)
		if err != nil {
			return nil, err
		}
		if machine.MicroVMCommand != "" {
			return nil, fmt.Errorf("moving project environments requires the built-in VM runner")
		}
		machines[machine.ID] = machine
		byWorkspace[workspaceBindingID(project.ID, machine.ID)] = true
		for _, volume := range machine.Storage {
			byStorage[storageBindingID(project.ID, machine.ID, volume)] = volume.ID
		}
	}
	result := map[string]MoveDirectory{}
	for _, kind := range []string{"project_workspace", "project_storage"} {
		rows, err := manager.Connector.Store.ListFields(kind, &project.ID, 1001, 0, []string{"id"})
		if err != nil {
			return nil, err
		}
		if len(rows) > 1000 {
			return nil, fmt.Errorf("too many recorded project storage bindings")
		}
		for _, row := range rows {
			id, _ := row["id"].(string)
			var binding workspaceBinding
			if err := manager.Connector.Store.Load(kind, id, &binding); err != nil {
				return nil, err
			}
			if !binding.Initialized {
				continue
			}
			// Prefer deterministic hosts within any shared binding, retaining
			// alternative known paths for a temporarily unavailable member.
			location := MoveDirectory{Kind: kind, BindingID: id, Paths: binding.Directories}
			assigned := byWorkspace[id] || byStorage[id] != ""
			for machine := range binding.Directories {
				if _, exists := machines[machine]; exists {
					assigned = true
					break
				}
			}
			if !assigned {
				continue
			}
			key := ""
			if kind == "project_storage" {
				key = byStorage[id]
				if key == "" {
					return nil, fmt.Errorf("restore the old storage configuration before moving this project")
				}
			}
			if _, duplicate := result[key]; duplicate {
				return nil, fmt.Errorf("storage volume %q has multiple independent copies; consolidate them before moving", key)
			}
			result[key] = location
		}
	}
	return result, nil
}

//go:embed scripts/vm_move_snapshot.py
var vmMoveSnapshotScript string

func SnapshotMovedVM(ctx context.Context, source, destination *Connection, execution Execution, workspace, token, id string) error {
	snapshot := func(action string) string {
		return "python3 -I -c " + Quote(vmMoveSnapshotScript) + " " + Quote(action) + " " + Quote(source.InstallationID()) + " " + Quote(execution.ProjectID) + " " + Quote(execution.AgentID)
	}
	output, err := Run(ctx, source, snapshot("check"))
	if err != nil {
		return err
	}
	switch strings.TrimSpace(output) {
	case "missing":
		return nil
	case "present":
	default:
		return fmt.Errorf("invalid source VM disk receipt")
	}
	state, err := MoveDirectoryState(ctx, destination, "snapshot-check", execution.ProjectID, workspace, token, id)
	if err != nil || state == "published" {
		return err
	}
	if err := transferMoveStream(ctx, source, destination, snapshot("export"), workspaceMoveCommand("snapshot-receive", execution.ProjectID, workspace, token, id)); err != nil {
		return err
	}
	_, err = MoveDirectoryState(ctx, destination, "snapshot-publish", execution.ProjectID, workspace, token, id)
	return err
}

func RemoveProjectMoveScratch(ctx context.Context, connection *Connection, projectID string) error {
	if !ValidID(projectID) {
		return fmt.Errorf("invalid project identity for scratch cleanup")
	}
	_, err := MoveDirectoryState(ctx, connection, "remove-scratch", projectID, connection.ProjectTemporaryDirectory(projectID), "", "scratch")
	return err
}

func RetireMovedVM(ctx context.Context, connection *Connection, execution Execution) error {
	command := "python3 -I -c " + Quote(vmMoveSnapshotScript) + " retire " + Quote(connection.InstallationID()) + " " + Quote(execution.ProjectID) + " " + Quote(execution.AgentID)
	_, err := Run(ctx, connection, command)
	return err
}
