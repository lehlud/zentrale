package remote

import (
	"context"
	"errors"
	"os/exec"

	"github.com/lehlud/zentrale/domain"
)

// Both paths are derived from validated synthetic IDs. This script never
// follows a workspace symlink or a symlink left inside the chat directory.
var shortChatCleanupScript = projectScratchScript + `
import sys

workspace, chat, scratch = sys.argv[1:]
if not shutil.rmtree.avoids_symlink_attacks:
    raise RuntimeError("descriptor-relative directory removal is unavailable")

if chat:
    try:
        fd = open_owned_directory(Path(workspace))
    except FileNotFoundError:
        pass
    else:
        try:
            try:
                os.unlink(chat, dir_fd=fd)
            except IsADirectoryError:
                # Anchor traversal in the open directory on Python 3.10 too.
                shutil.rmtree(Path(f"/proc/self/fd/{fd}") / chat)
            except FileNotFoundError:
                pass
        finally:
            os.close(fd)
else:
    root = Path(workspace)
    if root.exists() or root.is_symlink():
        retire_scratch(root)
    remove_project_scratch(Path(scratch))
`

func RemoveShortChatDirectory(ctx context.Context, connection *Connection, scope, chat string) error {
	if !shortChatDirectoryPattern.MatchString(chat) {
		return errors.New("invalid short-chat directory")
	}
	return removeShortChatFiles(ctx, connection, scope, chat)
}

func RemoveShortChatVM(ctx context.Context, connection *Connection, machine domain.Machine, scope string) error {
	if err := shortChatVMLifecycle(ctx, connection, machine, scope, "remove"); err != nil {
		return err
	}
	return removeShortChatFiles(ctx, connection, scope, "")
}

func removeShortChatFiles(ctx context.Context, connection *Connection, scope, chat string) error {
	workspace, err := ShortChatWorkspacePath(connection, scope)
	if err != nil {
		return err
	}
	scratch := connection.ProjectTemporaryDirectory(scope)

	if connection.localShortChatRoot != "" {
		command := exec.CommandContext(ctx, "python3", "-I", "-c", shortChatCleanupScript, workspace, chat, scratch)
		command.Env = localVMEnvironment(connection)
		return command.Run()
	}
	command := "exec python3 -I -c " + Quote(shortChatCleanupScript) + " " + Quote(workspace) + " " + Quote(chat) + " " + Quote(scratch)
	_, err = Run(ctx, connection, command)
	return err
}
