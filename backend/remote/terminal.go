package remote

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

//go:embed scripts/terminal.py
var terminalRelay string

func terminalCommand(command string) string {
	return terminalCommandWithProtocol(command, false)
}

// Terminal uses the sandbox's controlling PTY, never a host SSH PTY.
type Terminal struct {
	*Process
	writeMu sync.Mutex
}

func ValidTerminalSize(cols, rows int) bool {
	return cols >= 1 && cols <= 1000 && rows >= 1 && rows <= 1000
}

func StartTerminal(ctx context.Context, connection *Connection, machine domain.Machine, project domain.Project, target EditorTarget, cols, rows int) (*Terminal, error) {
	if !ValidTerminalSize(cols, rows) {
		return nil, errors.New("terminal dimensions must be between 1 and 1000")
	}
	execution := Execution{
		AgentID: EditorScopeID(project.ID, target.AgentID), ProjectID: project.ID,
		Workspace: project.Directory, TemporaryDirectory: target.TemporaryDirectory,
		Network: true, ProtocolIO: true, TTY: true, TerminalProtocol: true,
		Command: WithCommitIdentity(`exec "${SHELL:-/bin/sh}" -i`, target.CommitIdentity),
	}
	ReportStartupPhase(ctx, "preparing_terminal")
	var command string
	var err error
	if target.PreparedSandbox == SandboxMicroVM && machine.MicroVMCommand == "" {
		// Auto machines may already have a harness-validated native selection.
		// Preserve Sandbox's workspace/config validation, then avoid repeating
		// auto detection only. Stage still performs every native prerequisite,
		// GPU/storage/session check and fails closed; no blind VM-running cache.
		if _, err = Workspace(execution.Workspace, ""); err == nil {
			err = validateSandboxConfiguration(WithMachineDefaults(machine))
		}
		if err == nil {
			command, err = managedMicroVMCommand(ctx, connection, machine, execution)
		}
	} else {
		command, _, err = Sandbox(ctx, connection, machine, execution)
	}
	if err != nil {
		return nil, err
	}
	ReportStartupPhase(ctx, "starting_terminal")
	process, err := StartProcess(ctx, connection.Client, command)
	if err != nil {
		return nil, err
	}
	terminal := &Terminal{Process: process}
	// StartProcess does not wait for shell output. Send the geometry consumed
	// by the relay before it forks the child.
	if err := terminal.frame(map[string]int{"cols": cols, "rows": rows}); err != nil {
		terminal.Close()
		return nil, err
	}
	const ready = "\x00ZENTRALE_TERMINAL_READY\n"
	ack := make([]byte, len(ready))
	if _, err := io.ReadFull(process, ack); err != nil {
		terminal.Close()
		return nil, err
	}
	if string(ack) != ready {
		terminal.Close()
		return nil, errors.New("invalid terminal startup acknowledgement")
	}
	return terminal, nil
}

func (terminal *Terminal) frame(value any) error {
	terminal.writeMu.Lock()
	defer terminal.writeMu.Unlock()
	return json.NewEncoder(terminal.Process).Encode(value)
}

func (terminal *Terminal) Input(data []byte) error {
	if len(data) > 65536 {
		return errors.New("terminal input exceeds 64 KiB")
	}
	return terminal.frame(map[string]any{"type": "input", "data": base64.StdEncoding.EncodeToString(data)})
}

func (terminal *Terminal) Resize(cols, rows int) error {
	if !ValidTerminalSize(cols, rows) {
		return errors.New("terminal dimensions must be between 1 and 1000")
	}
	return terminal.frame(map[string]any{"type": "resize", "cols": cols, "rows": rows})
}

func (terminal *Terminal) Exit(ctx context.Context) *int {
	select {
	case <-ctx.Done():
		return nil
	case <-terminal.done:
		code := 0
		if terminal.exitError != nil {
			var status *ssh.ExitError
			if !errors.As(terminal.exitError, &status) {
				return nil
			}
			code = status.ExitStatus()
		}
		return &code
	}
}

func terminalCommandWithProtocol(command string, protocol bool) string {
	result := "python3 -I -u -c " + Quote(terminalRelay) + " " + Quote(base64.StdEncoding.EncodeToString([]byte(command)))
	if protocol {
		result += " --protocol"
	}
	return result
}
