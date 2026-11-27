package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var shortChatScopePattern = regexp.MustCompile(`^shortchat-[a-f0-9]{56}$`)
var shortChatDirectoryPattern = regexp.MustCompile(`^chat-[a-f0-9]{64}$`)
var shortChatInstallationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var shortChatVenvPattern = regexp.MustCompile(`^\.venv-[A-Za-z0-9_-]+$`)
var shortChatPackagePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(\[[A-Za-z0-9._-]+(,[A-Za-z0-9._-]+)*\])?(==[A-Za-z0-9][A-Za-z0-9.+!_-]*)?$`)

const shortChatExitMarker = "\nZENTRALE_SHORT_CHAT_EXIT "

// ShortChatCommandError contains output from the isolated shell only.
type ShortChatCommandError struct {
	ExitCode int
	Output   string
}

func (failure *ShortChatCommandError) Error() string {
	return fmt.Sprintf("command exited with status %d\n%s", failure.ExitCode, failure.Output)
}

// ShortChatMachine validates that the machine can use only the managed runner,
// then removes capabilities that must not be exposed to a short chat.
func ShortChatMachine(machine domain.Machine) (domain.Machine, error) {
	if machine.MicroVMCommand != "" ||
		(machine.Sandbox != "" && machine.Sandbox != SandboxAutomatic && machine.Sandbox != SandboxMicroVM) {
		return domain.Machine{}, errors.New("short-chat execution requires a managed MicroVM target")
	}

	machine.Sandbox = SandboxMicroVM
	machine.Properties = nil
	machine.GPUDevices = nil
	machine.Storage = nil
	machine.StorageMounts = nil
	machine.QuotaCommand = nil
	return machine, nil
}

func StopShortChatVM(ctx context.Context, connection *Connection, machine domain.Machine, scope string) error {
	return shortChatVMLifecycle(ctx, connection, machine, scope, "stop")
}

func shortChatVMLifecycle(ctx context.Context, connection *Connection, machine domain.Machine, scope, action string) error {
	workspace, err := ShortChatWorkspacePath(connection, scope)
	if err != nil {
		return err
	}
	_, err = ManageMicroVM(ctx, connection, machine, action, Execution{
		AgentID: EditorScopeID(scope, ""), ProjectID: scope,
		Workspace:          workspace,
		TemporaryDirectory: connection.ProjectTemporaryDirectory(scope),
	})
	return err
}

// ShortChatWorkspacePath identifies a host-backed workspace without creating
// a directory, allocating a target or starting the guest.
func ShortChatWorkspacePath(connection *Connection, scope string) (string, error) {
	if connection == nil || !shortChatScopePattern.MatchString(scope) ||
		!shortChatInstallationPattern.MatchString(connection.InstallationID()) {
		return "", errors.New("invalid short-chat VM scope")
	}
	if connection.localShortChatRoot != "" {
		return filepath.Join(connection.localShortChatRoot, "workspaces", scope), nil
	}

	return "/tmp/zentrale-" + connection.InstallationID() + "-workspace-" + scope, nil
}

// RunShortChatVM uses a synthetic VM identity, not a datastore project. Each
// chat starts in its own directory within its owner's dedicated workspace.
func RunShortChatVM(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	userScope, chatDirectory, command string,
	attachments io.Reader,
) (string, error) {
	return runShortChatVM(ctx, connection, machine, userScope, chatDirectory, command, attachments, "", false)
}

func ValidateShortChatPackages(packages []string) error {
	for _, name := range packages {
		if !shortChatPackagePattern.MatchString(name) {
			return errors.New("use PyPI package names with optional extras or exact versions")
		}
	}
	return nil
}

func InstallShortChatVenv(ctx context.Context, connection *Connection, machine domain.Machine, userScope, chatDirectory, venv string, packages []string) (string, error) {
	if !shortChatVenvPattern.MatchString(venv) {
		return "", errors.New("invalid short-chat Python environment name")
	}
	if err := ValidateShortChatPackages(packages); err != nil {
		return "", err
	}
	script := "set -eu\nexport UV_CACHE_DIR=/tmp/uv-cache\n" +
		"uv --no-config venv --no-python-downloads --python /usr/bin/python3 " + Quote(venv) + "\n"
	if len(packages) > 0 {
		script += "uv --no-config pip install --default-index https://pypi.org/simple --only-binary :all: --link-mode copy --python " + Quote(venv+"/bin/python") + " --"
		for _, name := range packages {
			script += " " + Quote(name)
		}
	}
	return runShortChatVM(ctx, connection, machine, userScope, chatDirectory, script, nil, venv, len(packages) > 0)
}

func runShortChatVM(
	ctx context.Context,
	connection *Connection,
	machine domain.Machine,
	userScope, chatDirectory, command string,
	attachments io.Reader,
	venv string,
	network bool,
) (string, error) {
	workspace, err := ShortChatWorkspacePath(connection, userScope)
	if err != nil || !shortChatDirectoryPattern.MatchString(chatDirectory) {
		return "", errors.New("invalid short-chat VM scope")
	}

	machine, err = ShortChatMachine(machine)
	if err != nil {
		return "", err
	}
	// Never inherit a project invocation's resource ID or input stream.
	ctx = WithInvocation(ctx, Invocation{})

	temporary := connection.ProjectTemporaryDirectory(userScope)
	ReportStartupPhase(ctx, "preparing_environment")
	if err := prepareShortChatWorkspace(ctx, connection, workspace, temporary); err != nil {
		return "", err
	}

	execution := Execution{
		AgentID:            EditorScopeID(userScope, ""),
		ProjectID:          userScope,
		Workspace:          workspace,
		TemporaryDirectory: temporary,
		Command:            shortChatGuestCommand(chatDirectory, attachments != nil, venv, network),
		shortChatScript:    command,
		Network:            network,
	}
	phase, timeout := "running_code", 30*time.Second
	if venv != "" {
		phase, timeout = "installing_python_packages", 3*time.Minute
	}
	ReportStartupPhase(ctx, "sandbox")
	if connection.localShortChatRoot != "" {
		invocation, err := stageMicroVM(ctx, connection, machine, execution)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("could not prepare local short-chat MicroVM", "error", err)
			}
			return "", err
		}
		ReportStartupPhase(ctx, phase)
		commandContext, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return shortChatCommandResult(runLocalMicroVM(commandContext, connection, machine, "run", attachments, invocation))
	}
	wrapped, level, err := Sandbox(ctx, connection, machine, execution)
	if err != nil {
		return "", err
	}
	if level != SandboxMicroVM || wrapped == "" {
		return "", errors.New("short-chat execution was not staged inside a managed MicroVM")
	}

	ReportStartupPhase(ctx, phase)
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return shortChatCommandResult(RunPipe(commandContext, connection, wrapped, attachments))
}

func shortChatCommandResult(output string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	index := strings.LastIndex(output, shortChatExitMarker)
	if index < 0 {
		return output, nil
	}
	code, err := strconv.Atoi(strings.TrimSpace(output[index+len(shortChatExitMarker):]))
	if err != nil || code < 0 || code > 255 {
		return output, nil
	}
	output = output[:index]
	if code == 0 {
		return output, nil
	}
	if len(output) > 4096 {
		output = strings.ToValidUTF8(output[len(output)-4096:], "�")
	}
	return "", &ShortChatCommandError{ExitCode: code, Output: strings.TrimSpace(output)}
}

func prepareShortChatWorkspace(ctx context.Context, connection *Connection, workspace, temporary string) error {
	// A pre-existing symlink must not become a mount of an unrelated folder.
	const createPrivateWorkspace = `import os, stat, sys
path = sys.argv[1]
try:
    os.mkdir(path, 0o700)
except FileExistsError:
    pass
info = os.lstat(path)
if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
    raise SystemExit("short-chat workspace is not a private directory")`
	if connection.localShortChatRoot != "" {
		if err := os.MkdirAll(filepath.Dir(workspace), 0700); err != nil {
			return err
		}
		script := createPrivateWorkspace + "\n" + projectScratchScript + "\nprepare_project_scratch(Path(sys.argv[2]))"
		return exec.CommandContext(ctx, "python3", "-I", "-c", script, workspace, temporary).Run()
	}
	prepare := "python3 -c " + Quote(createPrivateWorkspace) + " " + Quote(workspace) +
		" && " + PrepareProjectScratchCommand(temporary)
	_, err := Run(ctx, connection, prepare)
	return err
}

func shortChatGuestCommand(chatDirectory string, attachments bool, venv string, network bool) string {
	directory := Quote("/home/zentrale/project/" + chatDirectory)
	prepare := "umask 077\n" +
		"if [ -L " + directory + " ]; then exit 78; fi\n" +
		"mkdir -p -- " + directory + " && cd -- " + directory + " || exit 78\n" +
		"[ \"$(pwd -P)\" = " + directory + " ] || exit 78\n"
	sandbox := "/usr/bin/bwrap --unshare-all --die-with-parent --new-session --clearenv " +
		"--ro-bind /usr /usr --ro-bind /etc /etc --ro-bind-try /opt/zentrale /opt/zentrale " +
		"--symlink usr/bin /bin --symlink usr/sbin /sbin " +
		"--symlink usr/lib /lib --symlink usr/lib64 /lib64 --dev /dev --proc /proc " +
		"--chdir /home/zentrale/project --ro-bind-data \"$ZENTRALE_SHORT_CHAT_SCRIPT_FD\" /run/zentrale-command " +
		"--setenv HOME /home/zentrale --setenv PATH /usr/local/bin:/usr/bin:/bin --setenv TMPDIR /tmp "
	if venv != "" {
		// Give the installer a fresh directory only. It cannot see user data,
		// existing virtual environments, or configuration supplied by a chat.
		if network {
			sandbox += "--share-net "
		}
		return prepare + "staging=$(mktemp -d .venv-building.XXXXXXXX) || exit 78\n" +
			"trap 'rm -rf -- \"$staging\"' EXIT\n" +
			"if " + sandbox + "--tmpfs /tmp --bind \"$staging\" " + Quote("/home/zentrale/project/"+venv) + " -- /bin/sh /run/zentrale-command 2>&1; then\n" +
			"mv -T --no-clobber -- \"$staging\" " + Quote(venv) + " && test ! -d \"$staging\" || exit 78\n" +
			"code=0\nelse\ncode=$?\nfi\n" +
			"printf '" + shortChatExitMarker + "%s\\n' \"$code\"\n"
	}
	const scratch = ".zentrale-tmp"
	script := ""
	if attachments {
		script = "umask 077; tar -xf - -C /tmp/attachments --no-same-owner --no-same-permissions || exit 78\n"
	}
	// A successful launcher supplies the exit status after the inner shell
	// exits. Host and outer guest errors never enter the model's tool output.
	script += "/bin/sh /run/zentrale-command 2>&1\n" +
		"printf '" + shortChatExitMarker + "%s\\n' \"$?\""
	return prepare +
		"[ ! -L " + scratch + " ] && mkdir -p -- " + scratch + " || exit 78\n" +
		"[ ! -L " + scratch + "/attachments ] || exit 78\n" +
		"exec " + sandbox + "--bind . /home/zentrale/project " +
		"--bind " + scratch + " /tmp --tmpfs /tmp/attachments " +
		"--tmpfs /home/zentrale/project/" + scratch + " " +
		"-- /bin/sh -c " + Quote(script) + "\n"
}
