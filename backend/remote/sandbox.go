package remote

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

// Execution describes one approved remote action. Network access is never carried
// from a previous action to the next one.
type Execution struct {
	AgentID            string
	ProjectID          string
	Workspace          string
	TemporaryDirectory string

	Command        string
	Network        bool
	OutsideSandbox bool
	GuestRoot      bool
	Tooling        bool // Trusted file harness; never supplied by a model.
	CheckWorkspace bool // Check the guest's workspace export before retrying a stalled checkout.

	ProtocolIO       bool // Persistent stdin configured by the harness for MCP or interactive commands.
	TTY              bool
	TerminalProtocol bool   // Trusted browser terminal: bounded JSON input/resize frames.
	EditorSessionID  string // A starting editor must not wait for its own shutdown.
}

const userScopeCommand = "systemd-run --user --scope --quiet --no-ask-password -p MemoryMax=4G -p TasksMax=256"

const isolatedBubblewrapCommand = "bwrap --die-with-parent --new-session --unshare-all"

func Sandbox(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	execution Execution,
) (string, string, error) {
	if _, err := Workspace(execution.Workspace, ""); err != nil {
		return "", "", err
	}

	command := hostExecutionCommand(execution)
	level, err := sandboxLevel(ctx, connection, machine)
	if err != nil {
		return "", "", err
	}
	if level == SandboxMicroVM && connection != nil && execution.ProjectID != "" {
		execution.AgentID = EditorScopeID(execution.ProjectID, "")
		execution.TemporaryDirectory = connection.ProjectTemporaryDirectory(execution.ProjectID)
	}

	if execution.OutsideSandbox {
		if level == SandboxMicroVM {
			return "", "", errors.New("host command execution is unavailable in VM mode; run the command inside the VM")
		}
		if execution.TTY {
			execution.Command = terminalCommand(execution.Command)
			command = hostExecutionCommand(execution)
		}

		if execution.GuestRoot {
			return "", "", errors.New("guestRoot cannot be combined with outsideSandbox")
		}

		return command, SandboxDisabled, nil
	}

	if execution.TTY && (level != SandboxMicroVM || machine.MicroVMCommand != "") {
		execution.Command = terminalCommandWithProtocol(execution.Command, execution.TerminalProtocol)
		command = hostExecutionCommand(execution)
	}

	if level == SandboxDisabled {
		if execution.GuestRoot {
			return "", "", errors.New("guestRoot requires a managed Fedora MicroVM")
		}

		return command, level, nil
	}

	switch level {
	case "microvm":
		if machine.MicroVMCommand == "" {
			wrapped, err := managedMicroVMCommand(ctx, connection, machine, execution)
			return wrapped, level, err
		}

		if execution.GuestRoot {
			return "", "", errors.New("guestRoot requires the managed Fedora MicroVM runner")
		}

		wrapped := Quote(machine.MicroVMCommand) +
			" --root " + Quote(execution.Workspace) +
			" --temporary-directory " + Quote(execution.TemporaryDirectory) +
			fmt.Sprintf(" --network=%t -- /bin/sh -c ", execution.Network) + Quote(command)

		return wrapped, level, nil
	case "bwrap":
		if execution.GuestRoot {
			return "", "", errors.New("guestRoot requires a managed Fedora MicroVM")
		}

		return bubblewrapCommand(execution), level, nil
	default:
		return "", "", errors.New("invalid sandbox level")
	}
}

func sandboxLevel(ctx context.Context, connection *Connection, machine domain.Machine) (string, error) {
	// Probes must not consume the durable resource ID of the process being prepared.
	ctx = WithInvocation(ctx, Invocation{})
	if GPUEnabled(machine) || len(machine.Storage) > 0 {
		if err := validateSandboxConfiguration(WithMachineDefaults(machine)); err != nil {
			return "", err
		}

		// Explicit PCI/GPU requirements must never fall back to a host process
		// or to another sandbox after KVM, VFIO or device preparation fails.
		if _, err := Run(ctx, connection, microVMCommand("ensure", machine)); err != nil {
			return "", fmt.Errorf("prepare managed VM: %w", err)
		}

		return SandboxMicroVM, nil
	}

	if machine.Sandbox == SandboxMicroVM && machine.MicroVMCommand == "" {
		if _, err := Run(ctx, connection, microVMCommand("ensure", machine)); err != nil {
			return "", fmt.Errorf("prepare managed MicroVM: %w", err)
		}

		return SandboxMicroVM, nil
	}

	if machine.Sandbox != SandboxAutomatic {
		return machine.Sandbox, nil
	}

	// Automatic mode requires VM isolation. Missing prerequisites are an error;
	// weaker isolation must be an explicit machine configuration decision.
	probe := "if ! test -c /dev/kvm || ! test -r /dev/kvm || ! test -w /dev/kvm; then echo 'VM isolation requires accessible /dev/kvm; automatic sandbox selection cannot downgrade isolation' >&2; exit 78; fi\n"
	if machine.MicroVMCommand != "" {
		probe += "if test -x " + Quote(machine.MicroVMCommand) + "; then echo microvm; else echo 'configured VM runner is unavailable' >&2; exit 78; fi\n"
	} else {
		probe += microVMCommand("select", machine)
	}

	probeContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	output, err := Run(probeContext, connection, probe)
	if err != nil {
		return "", fmt.Errorf("detect remote sandbox: %w", err)
	}

	level := strings.TrimSpace(output)

	switch level {
	case SandboxMicroVM:
		return level, nil
	default:
		return "", fmt.Errorf("unexpected sandbox detection result: %q", level)
	}
}

// InspectSandbox uses the same selection as execution, without running a user command.
func InspectSandbox(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
) domain.SandboxStatus {
	status, _ := InspectSandboxWithError(ctx, connection, machine)
	return status
}

// InspectSandboxWithError retains the typed transport/configuration failure for
// preparation retry decisions. SandboxStatus.Error is a display diagnostic.
func InspectSandboxWithError(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
) (domain.SandboxStatus, error) {
	level, err := sandboxLevel(ctx, connection, machine)
	status := domain.SandboxStatus{
		MachineID: machine.ID,
		Mode:      machine.Sandbox,
		Level:     level,
		CheckedAt: time.Now().UTC(),
	}

	if err != nil {
		status.Error = err.Error()
	}

	return status, err
}

// Host execution and non-VM sandboxes also receive the owner's scratch path.
// Helpers can use TMPDIR without embedding a host path into a guest command.
func hostExecutionCommand(execution Execution) string {
	command := "cd " + Quote(execution.Workspace) + " && "
	if execution.TemporaryDirectory != "" {
		command += "export TMPDIR=" + Quote(execution.TemporaryDirectory) + " && "
	}

	return command + execution.Command
}

func bubblewrapCommand(execution Execution) string {
	arguments := []string{
		userScopeCommand,
		isolatedBubblewrapCommand,
	}

	if execution.Network {
		arguments = append(arguments, "--share-net")
	}

	arguments = append(arguments,
		"--ro-bind / /",
		"--tmpfs /tmp",
		"--bind "+Quote(execution.Workspace)+" "+Quote(execution.Workspace),
		"--bind "+Quote(execution.TemporaryDirectory)+" "+Quote(execution.TemporaryDirectory),
		"--proc /proc --dev /dev",
		"--chdir "+Quote(execution.Workspace),
		"--setenv TMPDIR "+Quote(execution.TemporaryDirectory),
		"/bin/sh -c "+Quote(execution.Command),
	)

	return strings.Join(arguments, " ")
}
