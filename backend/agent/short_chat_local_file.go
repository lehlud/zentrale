package agent

import (
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func streamLocalChatFile(workspace, relative string, send func(int64, io.Reader) error) error {
	if _, err := shortChatDeliveryPath(relative); err != nil {
		return ErrWorkspaceFileNotFound
	}

	// Resolve every model-influenced component relative to a directory
	// descriptor, rejecting symlinks at every step, including the chat root.
	const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	dir, err := unix.Open(workspace, dirFlags, 0)
	if err != nil {
		return ErrWorkspaceFileNotFound
	}
	defer func() { _ = unix.Close(dir) }()

	parts := strings.Split(relative, "/")
	for _, part := range parts[:len(parts)-1] {
		child, err := unix.Openat(dir, part, dirFlags, 0)
		if err != nil {
			return ErrWorkspaceFileNotFound
		}
		_ = unix.Close(dir)
		dir = child
	}

	const fileFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	fd, err := unix.Openat(dir, parts[len(parts)-1], fileFlags, 0)
	if err != nil {
		return ErrWorkspaceFileNotFound
	}
	file := os.NewFile(uintptr(fd), parts[len(parts)-1])
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return ErrWorkspaceFileNotFound
	}
	if info.Size() > MaxAttachmentBytes {
		return ErrWorkspaceFileTooLarge
	}
	return send(info.Size(), io.LimitReader(file, info.Size()))
}
