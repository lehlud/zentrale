package remote

import (
	"context"
	"errors"
	"io"
	"regexp"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var shortChatScopePattern = regexp.MustCompile(`^shortchat-[a-f0-9]{56}$`)
var shortChatDirectoryPattern = regexp.MustCompile(`^chat-[a-f0-9]{64}$`)
var shortChatInstallationPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

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
	workspace, err := ShortChatWorkspacePath(connection, scope)
	if err != nil {
		return err
	}
	_, err = ManageMicroVM(ctx, connection, machine, "stop", Execution{
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
	if err := prepareShortChatWorkspace(ctx, connection, workspace, temporary); err != nil {
		return "", err
	}

	if attachments != nil {
		command = "umask 077; mkdir /tmp/attachments && " +
			"tar -xf - -C /tmp/attachments --no-same-owner --no-same-permissions || exit 78\n" + command
	}

	wrapped, level, err := Sandbox(ctx, connection, machine, Execution{
		AgentID:            EditorScopeID(userScope, ""),
		ProjectID:          userScope,
		Workspace:          workspace,
		TemporaryDirectory: temporary,
		Command:            shortChatGuestCommand(chatDirectory, command),
	})
	if err != nil {
		return "", err
	}
	if level != SandboxMicroVM || wrapped == "" {
		return "", errors.New("short-chat execution was not staged inside a managed MicroVM")
	}

	commandContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return RunPipe(commandContext, connection, wrapped, attachments)
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
	prepare := "python3 -c " + Quote(createPrivateWorkspace) + " " + Quote(workspace) +
		" && " + PrepareProjectScratchCommand(temporary)
	_, err := Run(ctx, connection, prepare)
	return err
}

func shortChatGuestCommand(chatDirectory, command string) string {
	directory := Quote("/home/zentrale/project/" + chatDirectory)
	return "umask 077\n" +
		"if [ -L " + directory + " ]; then exit 78; fi\n" +
		"mkdir -p -- " + directory + " && cd -- " + directory + " || exit 78\n" +
		"[ \"$(pwd -P)\" = " + directory + " ] || exit 78\n" +
		"exec /usr/bin/bwrap --unshare-all --die-with-parent --new-session --clearenv " +
		"--ro-bind /usr /usr --ro-bind /etc /etc --symlink usr/bin /bin --symlink usr/sbin /sbin " +
		"--symlink usr/lib /lib --symlink usr/lib64 /lib64 --dev /dev --proc /proc --tmpfs /tmp " +
		"--bind . /home/zentrale/project --chdir /home/zentrale/project " +
		"--setenv HOME /home/zentrale --setenv PATH /usr/bin:/bin --setenv TMPDIR /tmp " +
		"-- /bin/sh -c " + Quote(command) + "\n"
}
