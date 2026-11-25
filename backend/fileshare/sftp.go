package fileshare

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func (s *Share) connectSFTP(ctx context.Context) (*sftp.Client, func(), error) {
	conn, closeSocket, err := dialTCP(ctx, s.address)
	if err != nil {
		return nil, nil, err
	}

	sshConn, channels, requests, err := ssh.NewClientConn(conn, s.address, &ssh.ClientConfig{
		User: s.username,
		Auth: []ssh.AuthMethod{ssh.Password(s.password)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if ssh.FingerprintSHA256(key) != s.fingerprint {
				return ErrHostKey
			}
			return nil
		},
	})
	if err != nil {
		closeSocket()
		if errors.Is(err, ErrHostKey) {
			return nil, nil, ErrHostKey
		}
		return nil, nil, operationError(ctx)
	}

	sshClient := ssh.NewClient(sshConn, channels, requests)
	client, err := sftp.NewClient(sshClient)
	if err != nil {
		closeSocket()
		sshClient.Close()
		return nil, nil, operationError(ctx)
	}

	return client, func() {
		closeSocket()
		client.Close()
		sshClient.Close()
	}, nil
}

func (s *Share) listSFTP(ctx context.Context, parts []string) ([]Entry, error) {
	client, closeClient, err := s.connectSFTP(ctx)
	if err != nil {
		return nil, err
	}
	defer closeClient()

	if _, err := verifySFTPPath(ctx, client, parts, true); err != nil {
		return nil, err
	}
	infos, err := client.ReadDirContext(ctx, absoluteSFTPPath(parts))
	if err != nil {
		return nil, sftpError(ctx, err)
	}
	if len(infos) > maxListEntries {
		return nil, ErrTooLarge
	}
	if _, err := verifySFTPPath(ctx, client, parts, true); err != nil {
		return nil, err
	}

	var entries []Entry
	for _, info := range infos {
		if !validSegment(info.Name(), ProtocolSFTP) || info.Mode()&fs.ModeSymlink != 0 ||
			(!info.IsDir() && !info.Mode().IsRegular()) {
			continue
		}
		entries = append(entries, Entry{Name: info.Name(), IsDir: info.IsDir(), Size: max(0, info.Size())})
	}
	return entries, nil
}

func (s *Share) readSFTP(ctx context.Context, parts []string, maxBytes int64) ([]byte, error) {
	client, closeClient, err := s.connectSFTP(ctx)
	if err != nil {
		return nil, err
	}
	defer closeClient()

	beforeOpen, err := verifySFTPPath(ctx, client, parts, false)
	if err != nil {
		return nil, err
	}
	if beforeOpen.Size() > maxBytes {
		return nil, ErrTooLarge
	}
	file, err := client.Open(absoluteSFTPPath(parts))
	if err != nil {
		return nil, sftpError(ctx, err)
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return nil, sftpError(ctx, err)
	}
	if !sameSFTPFile(beforeOpen, opened) {
		return nil, ErrUnsafePath
	}
	afterOpen, err := verifySFTPPath(ctx, client, parts, false)
	if err != nil {
		return nil, err
	}
	if !sameSFTPFile(afterOpen, opened) {
		return nil, ErrUnsafePath
	}

	data, err := readBounded(ctx, file, maxBytes)
	if err != nil {
		return nil, err
	}
	afterRead, err := verifySFTPPath(ctx, client, parts, false)
	if err != nil {
		return nil, err
	}
	if !sameSFTPFile(afterRead, opened) {
		return nil, ErrUnsafePath
	}
	return data, nil
}

// SFTP v3 has no handle-relative, no-follow open operation. Reject each
// advertised symlink and require the canonical path to match exactly; use a
// server-side jail or prevent untrusted writers from renaming/replacing path
// components concurrently if the share is used as a security boundary.
func verifySFTPPath(ctx context.Context, client *sftp.Client, parts []string, wantDir bool) (fs.FileInfo, error) {
	var info fs.FileInfo
	for i := min(1, len(parts)); i <= len(parts); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		current := absoluteSFTPPath(parts[:i])
		stat, err := client.Lstat(current)
		if err != nil {
			return nil, sftpError(ctx, err)
		}
		if stat.Mode()&fs.ModeSymlink != 0 ||
			((wantDir || i < len(parts)) && !stat.IsDir()) ||
			(!wantDir && i == len(parts) && !stat.Mode().IsRegular()) {
			return nil, ErrUnsafePath
		}
		info = stat
	}

	fullPath := absoluteSFTPPath(parts)
	canonical, err := client.RealPath(fullPath)
	if err != nil {
		return nil, sftpError(ctx, err)
	}
	if canonical != fullPath {
		return nil, ErrUnsafePath
	}
	return info, nil
}

func absoluteSFTPPath(parts []string) string {
	return "/" + strings.Join(parts, "/")
}

func sameSFTPFile(a, b fs.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func sftpError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission):
		return ErrAccessDenied
	default:
		return ErrUnavailable
	}
}
