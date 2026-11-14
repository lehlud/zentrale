package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

//go:embed scripts/files.py
var fileScript string

//go:embed scripts/patch.py
var patchScript string

// Include connection, VM preparation and helper I/O, not only the final read.
// A stalled filesystem must not pin the agent (or its reviewer) indefinitely.
const fileOperationTimeout = 60 * time.Second

func fileOperationScript(action string) string {
	if action != "apply_patch" {
		return fileScript
	}
	// Keep the helper library's CLI inactive while loading the patch CLI.
	return "import sys, types\n" +
		"file_helpers = types.ModuleType('zentrale_files')\n" +
		"exec(" + JSON(fileScript) + ", file_helpers.__dict__)\n" +
		"sys.modules['zentrale_files'] = file_helpers\n" + patchScript
}

func fileReadOptions(arguments map[string]any) (map[string]int, error) {
	offset, err := outputInteger(arguments, "offset", 1, 1, 10000000)
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "limit", 200, 1, 2000)
	if err != nil {
		return nil, err
	}

	return map[string]int{"offset": offset, "limit": limit}, nil
}

func (engine *Engine) project(agentRecord domain.Agent) (domain.Project, error) {
	var project domain.Project
	err := engine.Store.Load("project", agentRecord.ProjectID, &project)
	if err == nil && project.ManagedWorkspace {
		var current domain.Agent
		if engine.Store.Load("agent", agentRecord.ID, &current) == nil {
			agentRecord.MachineID = current.MachineID
		}

		project.Directory, err = engine.Remote.KnownProjectDirectory(project, agentRecord.MachineID)
	}

	return project, err
}

func (engine *Engine) file(
	ctx context.Context,
	agentRecord domain.Agent,
	action string,
	args map[string]any,
	network, outside bool,
) (result map[string]any, err error) {
	ctx, cancel := context.WithTimeout(ctx, fileOperationTimeout)
	defer cancel()
	phase := "environment connection"
	defer func() {
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			warning := ""
			if phase == "remote helper" && action != "read_file" && action != "list_files" {
				warning = "; changes may already have been applied: read affected files before retrying"
			}
			err = fmt.Errorf("%s timed out during %s%s: %w", action, phase, warning, ctx.Err())
		}
	}()
	releaseEnvironment := engine.useEnvironment(agentRecord.ID)
	defer releaseEnvironment()
	options, err := fileReadOptions(args)
	if err != nil {
		return nil, err
	}

	script := fileOperationScript(action)
	payload := remote.Encode(stringArg(args, "content"))

	if action == "apply_patch" {
		payload = stringArg(args, "input")
		if len(payload) > 1024*1024 || !noSecrets(payload) {
			return nil, errors.New("patch exceeds 1 MiB or contains invalid characters")
		}

	}

	target := stringArg(args, "machineId")
	if target != "" && action != "read_file" && action != "list_files" {
		return nil, errors.New("machineId is available for read-only file inspection; use targeted exec for mutations")
	}

	connection, machine, project, release, err := engine.connectForJob(ctx, agentRecord, &executionRequest{
		MachineID: target,
	})
	if err != nil {
		return nil, err
	}
	defer release()
	defer connection.Close()

	filename := stringArg(args, "path")
	if filename == "" {
		filename = "."
	}

	temporaryDirectory, err := engine.temporaryDirectory(agentRecord.ID)
	if err != nil {
		return nil, err
	}
	if _, err := remote.Workspace(project.Directory, filename); err != nil &&
		!strings.HasPrefix(filename, temporaryDirectory+"/") {
		return nil, err
	}

	phase = "workspace preparation"
	if err := prepareRemoteWorkspace(ctx, connection, project.Directory, temporaryDirectory); err != nil {
		return nil, err
	}

	// File contents travel over stdin, outside both host and guest argv limits.
	command := "python3 -I -c " + remote.Quote(script) +
		" " + remote.Quote(project.Directory) +
		" " + remote.Quote(temporaryDirectory) +
		" " + remote.Quote(filename) +
		" " + remote.Quote(action) + " '-' " + remote.Quote(JSON(options))

	phase = "sandbox preparation"
	command, level, err := engine.sandbox(ctx, connection, machine, remote.Execution{
		AgentID:   agentRecord.ID,
		ProjectID: project.ID,
		Tooling:   true,

		Workspace: project.Directory,

		TemporaryDirectory: temporaryDirectory,

		Command: command,

		Network: network,

		OutsideSandbox: outside,
	})
	if err != nil {
		return nil, err
	}

	if err := engine.rememberExecutionSandbox(machine, level, outside); err != nil {
		return nil, err
	}

	if err := engine.rememberMicroVM(agentRecord, machine, project, level); err != nil {
		return nil, err
	}

	input := strings.NewReader(payload)
	phase = "remote helper"
	out, err := remote.RunPipe(ctx, connection.Client, command, input)
	if err != nil {
		return nil, fmt.Errorf("remote file operation: %w", err)
	}

	result, err = decodeFileResult(out)
	if err != nil {
		return nil, err
	}

	result["sandbox"] = level
	result["machineId"] = machine.ID

	return result, nil
}

// Only the fixed file helpers emit this private envelope. Expected operation
// failures travel over a successful helper transport, then become tool errors
// here; arbitrary subprocess output/tracebacks remain untouched by RunPipe.
func decodeFileResult(output string) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("invalid remote file result")
	}
	if raw, exists := result["_toolError"]; exists {
		failure, ok := raw.(map[string]any)
		code, _ := failure["code"].(string)
		message, _ := failure["message"].(string)
		if !ok || len(result) != 1 || code == "" || message == "" {
			return nil, errors.New("invalid remote file error response")
		}
		return nil, fmt.Errorf("file operation: %s [%s]", message, code)
	}
	return result, nil
}

func (engine *Engine) invalidate(agentID string) {
	engine.mu.Lock()

	for _, permission := range engine.grants[agentID] {
		if permission.cancel != nil {
			permission.cancel()
		}
	}

	delete(engine.grants, agentID)
	engine.mu.Unlock()
}

func (engine *Engine) activeGrants(ctx context.Context, agentRecord domain.Agent) ([]grant, error) {
	engine.mu.Lock()
	old := []grant{}
	for _, permission := range engine.grants[agentRecord.ID] {
		old = append(old, permission)
	}

	engine.mu.Unlock()
	active := []grant{}
	for _, permission := range old {
		readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		data, err := engine.file(readCtx, agentRecord, "read_file", map[string]any{
			"path": permission.Path,
		}, false, false)

		cancel()

		if err == nil &&
			data["sha256"] == permission.Hash &&
			data["machineId"] == permission.MachineID &&
			data["sandbox"] != "none" {
			active = append(active, permission)
		} else {
			if permission.cancel != nil {
				permission.cancel()
			}

			engine.mu.Lock()
			delete(engine.grants[agentRecord.ID], permission.Path)
			engine.mu.Unlock()
		}
	}

	return active, nil
}

func (engine *Engine) approvedRead(
	ctx context.Context,
	agentRecord domain.Agent,
	args map[string]any,
	justification string,
	network bool,
) (any, error) {
	revision := engine.instructionRevision(agentRecord.ProjectID)
	// Only the harness sees a file before approval. The bytes returned to the main
	// agent are exactly those whose digest was reviewed, including external edits.
	data, err := engine.file(ctx, agentRecord, "read_file", args, false, false)
	if err != nil {
		return nil, err
	}

	filename := stringArg(data, "path")
	hash := stringArg(data, "sha256")
	machine := stringArg(data, "machineId")
	level := stringArg(data, "sandbox")
	engine.mu.Lock()
	permission, ok := engine.grants[agentRecord.ID][filename]
	engine.mu.Unlock()

	if engine.instructionRevision(agentRecord.ProjectID) != revision {
		return nil, errors.New("user instructions changed; request the file again")
	}

	if !network && level != "none" && ok && permission.Hash == hash && permission.MachineID == machine {
		delete(data, "sha256")
		return data, nil
	}

	if ok && permission.cancel != nil {
		permission.cancel()
	}

	engine.mu.Lock()
	delete(engine.grants[agentRecord.ID], filename)
	engine.mu.Unlock()
	reviewed := map[string]any{}
	for k, v := range args {
		reviewed[k] = v
	}

	reviewed["sha256"] = hash
	reviewed["sandbox"] = level
	reviewed["machineId"] = machine
	blanket, err := engine.approve(
		ctx,
		agentRecord,
		interaction{
			Action:        "read_file",
			Arguments:     reviewed,
			Justification: justification,
			Network:       network,
		},
	)
	if err != nil {
		return nil, err
	}

	if engine.instructionRevision(agentRecord.ProjectID) != revision {
		return nil, errors.New("user instructions changed; request the file again")
	}

	if blanket && level != "none" {
		engine.rememberReadGrant(agentRecord, filename, hash, machine)
	}

	delete(data, "sha256")

	return data, nil
}

func noSecrets(s string) bool {
	return !strings.ContainsRune(s, '\x00')
}

func agentTemporaryDirectory(agentID string) string {
	return "/tmp/zentrale-" + agentID
}
