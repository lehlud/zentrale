package remote

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lehlud/zentrale/domain"
)

//go:embed microvm/runner.py
var managedMicroVMRunner string

//go:embed microvm/guest.py
var managedMicroVMGuest string

//go:embed microvm/init.py
var managedMicroVMInit string

//go:embed microvm/streams.py
var managedMicroVMStreams string

//go:embed microvm/release.json
var managedMicroVMRelease json.RawMessage

//go:embed microvm/vfio.py
var managedVFIO string

var microVMInvocationID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type VMRecoveryRequired struct{ Reason string }

func (failure *VMRecoveryRequired) Error() string {
	return "managed environment requires recovery: " + failure.Reason
}

// VMRecovery runs under the environment's lifecycle lock, before retrying work.
type VMRecovery interface {
	RecoverVM(context.Context, *Connection, domain.Machine, Execution, string) error
}

// SandboxImplementationID invalidates observations made before a runner update.
// Configuration alone cannot identify selections made by the old external-only
// MicroVM implementation.
func SandboxImplementationID() string {
	digest := sha256.Sum256([]byte(microVMConfiguration()))
	return hex.EncodeToString(digest[:])
}

func microVMConfiguration(machines ...domain.Machine) string {
	machine := domain.Machine{}

	if len(machines) > 0 {
		machine = machines[0]
	}

	machine = WithMachineDefaults(machine)
	options, _ := json.Marshal(map[string]any{
		"vmType":     machine.VMType,
		"gpuDevices": machine.GPUDevices,
		"storage":    machine.Storage,
	})

	digest := sha256.Sum256([]byte(managedMicroVMRunner + managedMicroVMGuest + managedMicroVMInit + managedMicroVMStreams + managedVFIO + terminalRelay + string(managedMicroVMRelease) + string(options)))
	configuration, err := json.Marshal(map[string]any{
		"release":    managedMicroVMRelease,
		"guest":      managedMicroVMGuest,
		"init":       managedMicroVMInit,
		"streams":    managedMicroVMStreams,
		"terminal":   terminalRelay,
		"vfio":       managedVFIO,
		"vmType":     machine.VMType,
		"gpuDevices": machine.GPUDevices,
		"storage":    machine.Storage,
		"runnerHash": hex.EncodeToString(digest[:]),
	})
	if err != nil {
		panic(err) // Embedded release data is checked by the release tests.
	}

	return string(configuration)
}

func microVMCommand(action string, machines ...domain.Machine) string {
	return "python3 -I -c " + Quote(managedMicroVMRunner) + " " + Quote(action) + " " + Quote(microVMConfiguration(machines...))
}

// Stage the invocation over stdin. Commands and file contents can exceed the
// kernel's command-line limit; only a short trusted control path reaches QEMU.
func managedMicroVMCommand(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	execution Execution,
) (string, error) {
	output, err := microVMAction(ctx, connection, machine, "stage", execution)
	if err != nil {
		return "", fmt.Errorf("prepare managed MicroVM invocation: %w", err)
	}

	invocation := strings.TrimSpace(output)
	if !path.IsAbs(invocation) ||
		path.Base(path.Dir(invocation)) != "requests" ||
		!microVMInvocationID.MatchString(path.Base(invocation)) {
		return "", fmt.Errorf("invalid managed MicroVM control path: %q", invocation)
	}

	return "exec " + microVMCommand("run", machine) + " " + Quote(invocation), nil
}

func microVMAction(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	action string,
	execution Execution,
) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"execution": map[string]any{
			"agentId":   execution.AgentID,
			"projectId": execution.ProjectID,

			"workspace":          execution.Workspace,
			"temporaryDirectory": execution.TemporaryDirectory,

			"command": execution.Command,
			"network": execution.Network,

			"guestRoot": execution.GuestRoot,
			"tooling":   execution.Tooling,

			"protocolIO": execution.ProtocolIO,

			"tty":              execution.TTY,
			"terminalProtocol": execution.TerminalProtocol,

			"storage": machine.StorageMounts,
		},
	})
	if err != nil {
		return "", err
	}

	output, err := RunPipe(ctx, connection.Client, microVMCommand(action, machine), strings.NewReader(string(payload)))
	var exit *ssh.ExitError
	if errors.As(err, &exit) && exit.ExitStatus() == 75 {
		const marker = "ZENTRALE_VM_RECOVERY_REQUIRED "
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, marker) {
				continue
			}
			var failure VMRecoveryRequired
			if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &failure) == nil &&
				(failure.Reason == "unresponsive" || failure.Reason == "configuration_changed") {
				return "", &failure
			}
		}
	}
	return output, err
}

// ManageMicroVM operates only on the scope identified by both agent and project.
// Reset replaces the Fedora disk; the project and temporary exports stay intact.
func ManageMicroVM(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	action string,
	execution Execution,
) (map[string]any, error) {
	switch action {
	case "prepare", "reset", "info", "stop", "remove":
	default:
		return nil, fmt.Errorf("unknown MicroVM lifecycle action: %s", action)
	}

	if action != "remove" && action != "stop" {
		level, err := sandboxLevel(ctx, connection, machine)
		if err != nil {
			return nil, err
		}

		if level != SandboxMicroVM || machine.MicroVMCommand != "" {
			return nil, fmt.Errorf("this action requires a managed Fedora MicroVM (selected: %s)", level)
		}
	}

	output, err := microVMAction(ctx, connection, machine, action, execution)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("decode MicroVM lifecycle result: %w", err)
	}

	return result, nil
}
