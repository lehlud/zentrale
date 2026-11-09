package remote

import (
	"errors"
	"path"
	"strings"
)

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
