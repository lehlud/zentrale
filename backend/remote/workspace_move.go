package remote

import (
	"bufio"
	"context"
	_ "embed"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

//go:embed scripts/workspace_move.py
var workspaceMoveScript string

//go:embed scripts/workspace_move_stream.py
var workspaceMoveStreamScript string

var moveStreamSocket = regexp.MustCompile(`^/tmp/zentrale-transfer-[a-z0-9_]{8}/stream$`)

type MoveDirectory struct {
	Kind      string            `json:"kind"`
	BindingID string            `json:"bindingId"`
	MachineID string            `json:"machineId"`
	Path      string            `json:"path"`
	Paths     map[string]string `json:"paths,omitempty"`
}

func workspaceMoveCommand(operation, projectID, path, token, id string) string {
	return "python3 -I -c " + Quote(projectScratchScript+"\n"+workspaceMoveScript) + " " + Quote(operation) + " " + Quote("project-"+projectID) + " " + Quote(path) + " " + Quote(token) + " " + Quote(id)
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
	process, reader, err := startMoveStream(ctx, source, "send", sender)
	if err != nil {
		return fmt.Errorf("open project transfer on source: %w", err)
	}
	defer process.Close()
	defer reader.Close()

	receiver, writer, err := startMoveStream(ctx, destination, "receive", recipient)
	if err != nil {
		return fmt.Errorf("open project transfer on destination: %w", err)
	}
	defer receiver.Close()
	defer writer.Close()
	if _, err := io.Copy(writer, reader.reader); err != nil {
		return fmt.Errorf("transfer project files: %w", err)
	}
	if err := writer.CloseWrite(); err != nil {
		return err
	}
	// Check archive processes as well as the SSH relays. EOF only confirms
	// that a stream closed, and a failed tar can still send a valid prefix.
	for _, endpoint := range []struct {
		name    string
		process *Process
	}{
		{"source relay", reader}, {"destination relay", writer},
		{"archive producer", process}, {"archive receiver", receiver},
	} {
		if err := waitMoveStream(ctx, endpoint.process); err != nil {
			return fmt.Errorf("%s: %w", endpoint.name, err)
		}
	}

	return nil
}

// The daemon owns the archive process and captures its diagnostics. Its local
// socket carries binary data over an extra channel on the already trusted SSH
// connection, so archives do not enter the daemon's bounded replay log.
func startMoveStream(ctx context.Context, connection *Connection, mode, command string) (*Process, *Process, error) {
	helper := "python3 -I -u -c " + Quote(workspaceMoveStreamScript)
	endpoint, err := StartProcess(ctx, connection, helper+" serve "+Quote(mode)+" "+Quote(command))
	if err != nil {
		return nil, nil, err
	}
	connect := func() (*Process, error) {
		line, err := bufio.NewReaderSize(endpoint.reader, 256).ReadSlice('\n')
		if err != nil {
			if failure := waitMoveStream(ctx, endpoint); failure != nil {
				return nil, failure
			}
			return nil, fmt.Errorf("read transfer socket address: %w", err)
		}
		path := strings.TrimSuffix(string(line), "\n")
		if !moveStreamSocket.MatchString(path) {
			return nil, fmt.Errorf("invalid transfer socket address")
		}
		if err := connection.requireWorkTarget(); err != nil {
			return nil, err
		}
		client := connection.Client
		if connection.daemon != nil {
			wire, err := connection.daemon.connect(ctx)
			if err != nil {
				return nil, err
			}
			if err := connection.daemon.connector.VerifyTrust(wire.connection); err != nil {
				return nil, err
			}
			client = wire.connection.Client
		}
		return startSSHProcess(ctx, client, helper+" relay "+Quote(mode)+" "+Quote(path))
	}
	relay, err := connect()
	if err != nil {
		endpoint.Close()
		return nil, nil, err
	}
	return endpoint, relay, nil
}

func waitMoveStream(ctx context.Context, process *Process) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-process.done:
		if process.exitError != nil {
			return commandFailure(process.exitError, process.stderr.String())
		}
		return nil
	}
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

func DiscardLegacyMoveSnapshots(ctx context.Context, connection *Connection, projectID, workspace, token, id string) error {
	_, err := MoveDirectoryState(ctx, connection, "discard-legacy-snapshots", projectID, workspace, token, id)
	return err
}

// ArchiveDirectories uses actual initialization receipts, including hosts no
// longer in the project's current machine selection.
func (manager *Manager) ArchiveDirectories(projectID string) ([]MoveDirectory, error) {
	var directories []MoveDirectory
	for _, kind := range []string{"project_workspace", "project_storage"} {
		rows, err := manager.Connector.Store.ListFields(kind, &projectID, 1001, 0, []string{"id"})
		if err != nil {
			return nil, err
		}
		if len(rows) > 1000 {
			return nil, fmt.Errorf("too many project workspace receipts")
		}

		for _, row := range rows {
			id, _ := row["id"].(string)
			var binding workspaceBinding
			if err := manager.Connector.Store.Load(kind, id, &binding); err != nil {
				return nil, err
			}
			if len(binding.Directories) == 0 {
				return nil, fmt.Errorf("finish preparing the project workspace before archiving")
			}
			directories = append(directories, MoveDirectory{Kind: kind, BindingID: id, Paths: binding.Directories})
		}
	}
	return directories, nil
}

//go:embed scripts/workspace_archive.py
var workspaceArchiveScript string

func ArchiveGitCommand() string {
	return "python3 -I -c " + Quote(workspaceArchiveScript)
}

func RemoveProjectMoveScratch(ctx context.Context, connection *Connection, projectID string) error {
	if !ValidID(projectID) {
		return fmt.Errorf("invalid project identity for scratch cleanup")
	}
	_, err := MoveDirectoryState(ctx, connection, "remove-scratch", projectID, connection.ProjectTemporaryDirectory(projectID), "", "scratch")
	return err
}

//go:embed scripts/vm_move_cleanup.py
var vmMoveCleanupScript string

func RetireMovedVM(ctx context.Context, connection *Connection, execution Execution) error {
	command := "python3 -I -c " + Quote(vmMoveCleanupScript) + " " + Quote(connection.InstallationID()) + " " + Quote(execution.ProjectID) + " " + Quote(execution.AgentID)
	_, err := Run(ctx, connection, command)
	return err
}
