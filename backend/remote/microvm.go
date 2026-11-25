package remote

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/ascii85"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
)

//go:embed microvm/runner.py
var managedMicroVMRunner string

//go:embed microvm/project_scratch.py
var projectScratchScript string

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

//go:embed microvm/nvidia.py
var managedNVIDIA string

var microVMInvocationID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Leave time for artifact staging and the runner's bounded 18 minute cold boot.
const ManagedVMSetupTimeout = 25 * time.Minute

type VMStartupFailure struct {
	Reason     string
	Diagnostic string
}

func (failure *VMStartupFailure) Error() string {
	return "managed environment startup failed: " + failure.Reason + "\n" + failure.Diagnostic
}

type VMRecoveryRequired struct{ Reason string }

func (failure *VMRecoveryRequired) Error() string {
	return "managed environment requires recovery: " + failure.Reason
}

type VMHealthPending struct{}

func (*VMHealthPending) Error() string {
	return "the managed MicroVM is slow to respond; existing jobs are still attached. Retry this operation when the guest responds"
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
		"gpu":        GPUEnabled(machine),
		"gpuDevices": machine.GPUDevices,
		"storage":    machine.Storage,
	})

	hashInput := strings.Join([]string{
		managedMicroVMRunner,
		projectScratchScript,
		managedMicroVMGuest,
		managedMicroVMInit,
		managedMicroVMStreams,
		managedVFIO,
		managedNVIDIA,
		terminalRelay,
		string(managedMicroVMRelease),
		string(options),
	}, "")

	digest := sha256.Sum256([]byte(hashInput))

	configuration, err := json.Marshal(map[string]any{
		"release":    managedMicroVMRelease,
		"guest":      managedMicroVMGuest,
		"init":       managedMicroVMInit,
		"streams":    managedMicroVMStreams,
		"terminal":   terminalRelay,
		"vfio":       managedVFIO,
		"nvidia":     managedNVIDIA,
		"gpu":        GPUEnabled(machine),
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
	// SSH servers pass the entire command through one shell argument. Keep the
	// trusted program and configuration below Linux's per-argument size limit,
	// including when another sandbox-selection shell quotes this command again.
	bundle, err := json.Marshal(map[string]any{
		"runner":         managedMicroVMRunner,
		"projectScratch": projectScratchScript,
		"configuration":  json.RawMessage(microVMConfiguration(machines...)),
	})
	if err != nil {
		panic(err)
	}
	var compressed bytes.Buffer
	writer, err := zlib.NewWriterLevel(&compressed, zlib.BestCompression)
	if err != nil {
		panic(err) // The fixed compression level is valid.
	}
	_, _ = writer.Write(bundle)
	_ = writer.Close()
	encoded := pythonBase85(compressed.Bytes())
	bootstrap := "import base64,json,sys,types,zlib; " +
		"bundle=json.loads(zlib.decompress(base64.b85decode('" + encoded + "'))); " +
		"scratch=types.ModuleType('project_scratch'); sys.modules[scratch.__name__]=scratch; " +
		"exec(compile(bundle['projectScratch'],'<zentrale-project-scratch>','exec'),scratch.__dict__); " +
		"sys.argv.insert(2,json.dumps(bundle['configuration'])); " +
		"exec(compile(bundle['runner'],'<zentrale-microvm>','exec'))"
	return "python3 -I -c " + Quote(bootstrap) + " " + Quote(action)
}

func PrepareProjectScratchCommand(directory string) string {
	script := projectScratchScript + "\nimport sys\nprepare_project_scratch(Path(sys.argv[1]))"
	return "python3 -I -c " + Quote(script) + " " + Quote(directory)
}

// RFC 1924's alphabet carries more bytes per SSH shell argument than Base64,
// while avoiding quotes and backslashes. Python's stdlib decodes it directly.
func pythonBase85(data []byte) string {
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~"
	encoded := make([]byte, ascii85.MaxEncodedLen(len(data)))
	encoded = encoded[:ascii85.Encode(encoded, data)]
	var output strings.Builder
	for _, digit := range encoded {
		if digit == 'z' { // Adobe's shorthand for four zero bytes.
			output.WriteString("00000")
		} else {
			output.WriteByte(alphabet[digit-'!'])
		}
	}
	return output.String()
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
	// VM control commands have independent IDs and stdin streams. Keep any
	// durable invocation and its input receipts for the actual guest process.
	ctx = WithInvocation(ctx, Invocation{})
	if action == "stage" || action == "prepare" || action == "reset" {
		execution.AgentID = EditorScopeID(execution.ProjectID, "")
		execution.TemporaryDirectory = connection.ProjectTemporaryDirectory(execution.ProjectID)
	}

	payload, err := json.Marshal(map[string]any{
		"execution": map[string]any{
			"installationId": connection.installationID,
			"agentId":        execution.AgentID,
			"projectId":      execution.ProjectID,

			"workspace":          execution.Workspace,
			"temporaryDirectory": execution.TemporaryDirectory,

			"command": execution.Command,
			"network": execution.Network,

			"guestRoot":      execution.GuestRoot,
			"tooling":        execution.Tooling,
			"checkWorkspace": execution.CheckWorkspace,

			"protocolIO": execution.ProtocolIO,

			"tty":              execution.TTY,
			"terminalProtocol": execution.TerminalProtocol,

			"storage":         machine.StorageMounts,
			"startupProgress": startupProgress(ctx) != nil,
		},
	})
	if err != nil {
		return "", err
	}

	var output string
	if startupProgress(ctx) == nil {
		output, err = RunPipe(ctx, connection, microVMCommand(action, machine), strings.NewReader(string(payload)))
	} else {
		var reported [len(managedVMStartupPhases)]sync.Once
		output, err = RunStreamingPipe(ctx, connection, microVMCommand(action, machine), strings.NewReader(string(payload)), func(snapshot string) {
			for index, phase := range managedVMStartupPhases {
				marker := startupPhasePrefix + phase + "\n"
				if strings.HasPrefix(snapshot, marker) || strings.Contains(snapshot, "\n"+marker) {
					reported[index].Do(func() { ReportStartupPhase(ctx, phase) })
				}
			}
		})
		// This marker belongs only to the trusted stage/prepare protocol. Keep
		// arbitrary output and all error diagnostics; never globally filter logs.
		output = stripStartupBootMarker(output)
	}
	if failure := parseVMStartupFailure(output, err); failure != nil {
		return "", failure
	}
	var exit interface{ ExitStatus() int }
	if errors.As(err, &exit) && exit.ExitStatus() == 75 {
		const marker = "ZENTRALE_VM_RECOVERY_REQUIRED "
		for _, line := range strings.Split(output, "\n") {
			if !strings.HasPrefix(line, marker) {
				continue
			}
			var failure VMRecoveryRequired
			if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &failure) != nil {
				continue
			}
			switch failure.Reason {
			case "guest_busy":
				return "", &VMHealthPending{}
			case "unresponsive", "configuration_changed", "supervisor_stopped":
				return "", &failure
			}
		}
	}
	return output, err
}

const startupPhasePrefix = "ZENTRALE_STARTUP_PHASE "
const startupBootMarker = startupPhasePrefix + "vm_starting"

var managedVMStartupPhases = [...]string{
	"vm_starting",
	"vm_retrying_boot",
	"vm_preparing_guest",
	"vm_installing_packages",
	"vm_preparing_tools",
}

func stripStartupBootMarker(output string) string {
	lines := strings.Split(output, "\n")
	kept := lines[:0]
	for _, line := range lines {
		phase, ok := strings.CutPrefix(line, startupPhasePrefix)
		if ok && slices.Contains(managedVMStartupPhases[:], phase) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func parseVMStartupFailure(output string, err error) *VMStartupFailure {
	var exit interface{ ExitStatus() int }
	if !errors.As(err, &exit) || exit.ExitStatus() != 76 {
		return nil
	}
	const marker = "ZENTRALE_VM_STARTUP_FAILED "
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var failure VMStartupFailure
		if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &failure) != nil {
			continue
		}
		switch failure.Reason {
		case "kernel_boot_timeout", "package_install_timeout", "package_install_failed", "startup_timeout":
			failure.Diagnostic = output
			if len(failure.Diagnostic) > 12000 {
				failure.Diagnostic = failure.Diagnostic[len(failure.Diagnostic)-12000:]
			}
			return &failure
		}
	}
	return nil
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
