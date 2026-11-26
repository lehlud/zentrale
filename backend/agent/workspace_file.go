package agent

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

//go:embed scripts/workspace_file.py
var workspaceFileScript string

var (
	ErrWorkspaceFileNotFound = errors.New("file not found in project workspace")
	ErrWorkspaceFileTooLarge = errors.New("file exceeds the transfer limit")
)

// ReadWorkspaceFile streams the conversation's existing, host-backed working
// tree. Reading a link does not allocate a different pool member or start a VM.
func (engine *Engine) ReadWorkspaceFile(ctx context.Context, projectID, agentID, filename string, limit int64, send func(string, int64, io.Reader) error) error {
	if filename == "" || len(filename) > 4096 {
		return ErrWorkspaceFileNotFound
	}
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil || record.ProjectID != projectID || record.Role == "approver" {
		return ErrWorkspaceFileNotFound
	}
	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil {
		return err
	}
	if err := engine.projectEnvironmentAvailable(projectID); err != nil {
		return err
	}
	machineID := record.MachineID
	if machineID == "" && len(project.MachineIDs) == 1 {
		machineID = project.MachineIDs[0]
	}
	pool, _, pooled := strings.Cut(machineID, "/")
	if !slices.Contains(project.MachineIDs, machineID) && !(pooled && slices.Contains(project.PoolIDs, pool)) {
		return ErrWorkspaceFileNotFound
	}
	directory, err := engine.Remote.KnownProjectDirectory(project, machineID)
	if err != nil {
		return err
	}
	relative, err := remote.WorkspaceRelative(directory, filename)
	if err != nil || relative == "." {
		return ErrWorkspaceFileNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()
	connection, err := engine.Remote.Connector.Dial(ctx, machineID)
	if err != nil {
		return err
	}
	defer connection.Close()
	command := workspaceFileCommand(path.Clean(directory), relative, limit)
	return streamWorkspaceFile(ctx, connection, command, limit, func(size int64, stream io.Reader) error {
		return send(path.Base(relative), size, stream)
	})
}

func workspaceFileCommand(root, relative string, limit int64) string {
	return "exec python3 -I -u -c " + remote.Quote(workspaceFileScript) + " " +
		remote.Quote(root) + " " + remote.Quote(strings.TrimPrefix(relative, "./")) + " " + strconv.FormatInt(limit, 10)
}

func streamWorkspaceFile(ctx context.Context, connection *remote.Connection, command string, limit int64, send func(int64, io.Reader) error) error {
	process, err := remote.StartProcess(ctx, connection, command)
	if err != nil {
		return err
	}
	defer process.Close()
	stream := bufio.NewReaderSize(process, 4096)
	header, err := stream.ReadSlice('\n')
	if err != nil {
		return err
	}
	switch line := strings.TrimSuffix(string(header), "\n"); line {
	case "missing":
		return ErrWorkspaceFileNotFound
	case "too_large":
		return ErrWorkspaceFileTooLarge
	default:
		size, err := strconv.ParseInt(line, 10, 64)
		if err != nil || size < 0 || size > limit {
			return errors.New("invalid workspace file response")
		}
		return send(size, io.LimitReader(stream, size))
	}
}
