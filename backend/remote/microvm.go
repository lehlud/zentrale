package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/ascii85"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/ulikunitz/xz"
)

//go:embed microvm/runner.py
var managedMicroVMRunner string

//go:embed microvm/project_scratch.py
var projectScratchScript string

//go:embed microvm/memory.py
var managedMicroVMMemory string

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

func microVMOptions(machines ...domain.Machine) map[string]any {
	machine := domain.Machine{}

	if len(machines) > 0 {
		machine = machines[0]
	}

	machine = WithMachineDefaults(machine)
	configuration := map[string]any{
		"gpu":        GPUEnabled(machine),
		"gpuDevices": machine.GPUDevices,
		"storage":    machine.Storage,
	}
	options, _ := json.Marshal(configuration)

	hashInput := strings.Join([]string{
		managedMicroVMRunner,
		projectScratchScript,
		managedMicroVMMemory,
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
	configuration["runnerHash"] = hex.EncodeToString(digest[:])
	return configuration
}

func microVMConfiguration(machines ...domain.Machine) string {
	configuration := microVMOptions(machines...)
	configuration["release"] = managedMicroVMRelease
	configuration["guest"] = managedMicroVMGuest
	configuration["init"] = managedMicroVMInit
	configuration["streams"] = managedMicroVMStreams
	configuration["terminal"] = terminalRelay
	configuration["vfio"] = managedVFIO
	configuration["nvidia"] = managedNVIDIA
	encoded, err := json.Marshal(configuration)
	if err != nil {
		panic(err) // Embedded release data is checked by the release tests.
	}

	return string(encoded)
}

// Compress the embedded program once. Per-machine options travel separately so
// interactive commands do not repeat the compression or exceed the SSH limit.
var compressedMicroVMBundle = sync.OnceValue(func() string {
	// The trusted text files contain no NUL. Keeping their original bytes
	// avoids JSON escaping the embedded Python sources before compression.
	bundle := strings.Join([]string{
		managedMicroVMRunner, projectScratchScript, managedMicroVMMemory,
		managedMicroVMGuest, managedMicroVMInit, managedMicroVMStreams,
		terminalRelay, managedVFIO, managedNVIDIA, string(managedMicroVMRelease),
	}, "\x00")
	var compressed bytes.Buffer
	writer, err := xz.WriterConfig{DictCap: 1 << 20}.NewWriter(&compressed)
	if err != nil {
		panic(err)
	}
	_, _ = io.WriteString(writer, bundle)
	_ = writer.Close()
	return pythonBase85(compressed.Bytes())
})

func microVMCommand(action string, machines ...domain.Machine) string {
	return "python3 -I -c " + Quote(microVMBootstrap(machines...)) + " " + Quote(action)
}

func microVMBootstrap(machines ...domain.Machine) string {
	options, err := json.Marshal(microVMOptions(machines...))
	if err != nil {
		panic(err)
	}
	return `import base64,json,sys,types
try:
    from lzma import decompress
except ImportError:
    import subprocess
    def decompress(data):
        return subprocess.check_output(['/usr/bin/xz','-dc'],input=data)
` +
		"runner,scratch,memory,guest,init,streams,terminal,vfio,nvidia,release=decompress(base64.b85decode('" + compressedMicroVMBundle() + "')).decode().split(chr(0))\n" +
		"configuration=json.loads(base64.b85decode('" + pythonBase85(options) + "'))\n" +
		`configuration.update(release=json.loads(release),guest=guest,init=init,streams=streams,terminal=terminal,vfio=vfio,nvidia=nvidia)
for name, source in [('project_scratch', scratch), ('memory', memory)]:
    module = types.ModuleType(name)
    sys.modules[name] = module
    exec(compile(source, '<zentrale-' + name + '>', 'exec'), module.__dict__)
sys.argv.insert(2,json.dumps(configuration))
exec(compile(runner,'<zentrale-microvm>','exec'))`
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
	invocation, err := stageMicroVM(ctx, connection, machine, execution)
	if err != nil {
		return "", err
	}
	return "exec " + microVMCommand("run", machine) + " " + Quote(invocation), nil
}

func stageMicroVM(ctx context.Context, connection *Connection, machine domain.Machine, execution Execution) (string, error) {
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

	return invocation, nil
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

			"command":         execution.Command,
			"network":         execution.Network,
			"shortChatScript": execution.shortChatScript,

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
	if connection.localShortChatRoot != "" {
		switch action {
		case "stage", "prepare", "reset", "info", "stop", "remove":
			output, err = runLocalMicroVM(ctx, connection, machine, action, bytes.NewReader(payload), "")
		default:
			return "", errors.New("invalid local MicroVM control action")
		}
	} else if startupProgress(ctx) == nil {
		output, err = RunPipe(ctx, connection, microVMCommand(action, machine), strings.NewReader(string(payload)))
	} else {
		output, err = RunStreamingPipe(ctx, connection, microVMCommand(action, machine), strings.NewReader(string(payload)), vmStartupProgressOutput(ctx))
	}
	if startupProgress(ctx) != nil {
		// This marker belongs only to the trusted stage/prepare protocol. Keep
		// arbitrary output and all error diagnostics; never globally filter logs.
		output = stripStartupBootMarker(output)
		var failure *CommandError
		if errors.As(err, &failure) {
			err = commandFailure(failure.Cause, output)
		}
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
	"vm_allocating_memory",
	"vm_preparing_guest",
	"vm_installing_packages",
	"vm_preparing_tools",
}

func vmStartupProgressOutput(ctx context.Context) func(string) {
	if startupProgress(ctx) == nil {
		return nil
	}
	var reported [len(managedVMStartupPhases)]sync.Once
	return func(snapshot string) {
		for index, phase := range managedVMStartupPhases {
			marker := startupPhasePrefix + phase + "\n"
			if strings.HasPrefix(snapshot, marker) || strings.Contains(snapshot, "\n"+marker) {
				reported[index].Do(func() { ReportStartupPhase(ctx, phase) })
			}
		}
	}
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
	lines := strings.Split(output, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var failure VMStartupFailure
		if json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &failure) != nil {
			continue
		}
		switch failure.Reason {
		case "kernel_boot_timeout", "memory_admission_timeout", "package_install_timeout", "package_install_failed", "startup_timeout":
			failure.Diagnostic = strings.TrimSpace(strings.Join(slices.Delete(lines, index, index+1), "\n"))
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
