package remote

import (
	"errors"
	"path"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

const (
	ManagedGuestWorkspace          = "/home/zentrale/project"
	ManagedGuestTemporaryDirectory = "/tmp/zentrale"
)

// ExecutionPaths separates host-backed storage from paths visible to a process.
// The managed guest uses the same paths for agents, editors and terminals.
type ExecutionPaths struct {
	Workspace          string
	TemporaryDirectory string
}

func PathsForExecution(machine domain.Machine, level, workspace, temporary string) ExecutionPaths {
	if level == SandboxMicroVM && machine.MicroVMCommand == "" {
		return ExecutionPaths{
			Workspace:          ManagedGuestWorkspace,
			TemporaryDirectory: ManagedGuestTemporaryDirectory,
		}
	}

	return ExecutionPaths{
		Workspace:          workspace,
		TemporaryDirectory: temporary,
	}
}

// WorkspaceRelative validates a project path before making it independent of
// the host mount point. The remote helper still checks resolved symlink bounds.
func WorkspaceRelative(projectDirectory, requestedPath string) (string, error) {
	resolved, err := Workspace(projectDirectory, requestedPath)
	if err != nil {
		return "", err
	}

	root := path.Clean(projectDirectory)
	if resolved == root {
		return ".", nil
	}

	return "./" + strings.TrimPrefix(resolved, root+"/"), nil
}

// Quote escapes one argument for a POSIX shell. It does not assemble commands or
// allow callers to provide unquoted shell fragments through argument values.
func Quote(argument string) string {
	return "'" + strings.ReplaceAll(argument, "'", "'\"'\"'") + "'"
}

// Workspace resolves a path lexically within a remote project directory. File
// operations also resolve symlinks on the remote machine before accessing data.
func Workspace(projectDirectory, requestedPath string) (string, error) {
	if err := validateProjectDirectory(projectDirectory); err != nil {
		return "", err
	}

	if strings.ContainsRune(requestedPath, '\x00') {
		return "", errors.New("file path must not contain a NUL byte")
	}

	projectDirectory = path.Clean(projectDirectory)
	// Resolve guest paths against host-backed storage for boundary validation.
	// Execution paths are selected separately; the host root is not a VM mount.
	if requestedPath == ManagedGuestWorkspace {
		requestedPath = ""
	} else if strings.HasPrefix(requestedPath, ManagedGuestWorkspace+"/") {
		requestedPath = strings.TrimPrefix(requestedPath, ManagedGuestWorkspace+"/")
	}
	resolvedPath := path.Join(projectDirectory, requestedPath)

	if path.IsAbs(requestedPath) {
		resolvedPath = path.Clean(requestedPath)
	}

	if resolvedPath == projectDirectory {
		return resolvedPath, nil
	}

	if !strings.HasPrefix(resolvedPath, projectDirectory+"/") {
		return "", errors.New("path leaves project directory")
	}

	return resolvedPath, nil
}

func validateProjectDirectory(directory string) error {
	if strings.ContainsRune(directory, '\x00') {
		return errors.New("project directory must not contain a NUL byte")
	}

	if !path.IsAbs(directory) || path.Clean(directory) == "/" {
		return errors.New("workspace must be an absolute directory other than /")
	}

	return nil
}
