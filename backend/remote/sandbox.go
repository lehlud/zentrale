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

	ProtocolIO      bool // Persistent stdin configured by the harness for MCP or interactive commands.
	TTY             bool
	EditorSessionID string // A starting editor must not wait for its own shutdown.
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

	command := "cd " + Quote(execution.Workspace) + " && " + execution.Command

	if execution.OutsideSandbox {
		if execution.TTY {
			command = "cd " + Quote(execution.Workspace) + " && " + terminalCommand(execution.Command)
		}

		if execution.GuestRoot {
			return "", "", errors.New("guestRoot cannot be combined with outsideSandbox")
		}

		return command, SandboxDisabled, nil
	}

	level, err := sandboxLevel(ctx, connection, machine)
	if err != nil {
		return "", "", err
	}

	if execution.TTY && (level != SandboxMicroVM || machine.MicroVMCommand != "") {
		execution.Command = terminalCommand(execution.Command)
		command = "cd " + Quote(execution.Workspace) + " && " + execution.Command
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
	if machine.VMType == VMTypeQ35 || len(machine.GPUDevices) > 0 || len(machine.Storage) > 0 {
		if err := validateSandboxConfiguration(WithMachineDefaults(machine)); err != nil {
			return "", err
		}

		// Explicit PCI/GPU requirements must never fall back to a host process
		// or to another sandbox after KVM, VFIO or device preparation fails.
		if _, err := Run(ctx, connection.Client, microVMCommand("ensure", machine)); err != nil {
			return "", fmt.Errorf("prepare managed VM: %w", err)
		}

		return SandboxMicroVM, nil
	}

	if machine.Sandbox == SandboxMicroVM && machine.MicroVMCommand == "" {
		if _, err := Run(ctx, connection.Client, microVMCommand("ensure", machine)); err != nil {
			return "", fmt.Errorf("prepare managed MicroVM: %w", err)
		}

		return SandboxMicroVM, nil
	}

	if machine.Sandbox != SandboxAutomatic {
		return machine.Sandbox, nil
	}

	// Installed binaries do not establish that user scopes and namespaces work
	// in this SSH session. Test a harmless isolated command before selecting auto.
	checkBubblewrap := userScopeCommand + " " + isolatedBubblewrapCommand +
		" --ro-bind / / --proc /proc --dev /dev /bin/sh -c 'exit 0'"

	probe := "if test -c /dev/kvm && test -r /dev/kvm && test -w /dev/kvm; then\n"

	if machine.MicroVMCommand != "" {
		probe += "if test -x " + Quote(machine.MicroVMCommand) + "; then echo microvm; exit 0; fi\n"
	} else {
		// Exit 78 means prerequisites are absent. Download or boot failures are
		// actionable errors, not permission to silently select a weaker sandbox.
		probe += "if " + microVMCommand("select", machine) + "; then exit 0; else status=$?; if test \"$status\" -ne 78; then exit \"$status\"; fi; fi\n"
	}

	probe += "fi\nif command -v bwrap >/dev/null 2>&1 && command -v systemd-run >/dev/null 2>&1" +
		" && test -f /sys/fs/cgroup/cgroup.controllers && " + checkBubblewrap +
		" >/dev/null 2>&1; then echo bwrap; else echo none; fi"

	probeContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	output, err := Run(probeContext, connection.Client, probe)
	if err != nil {
		return "", fmt.Errorf("detect remote sandbox: %w", err)
	}

	level := strings.TrimSpace(output)

	switch level {
	case "microvm", "bwrap", SandboxDisabled:
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

	return status
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
		"/bin/sh -c "+Quote(execution.Command),
	)

	return strings.Join(arguments, " ")
}
