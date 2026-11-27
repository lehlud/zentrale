package fileshare

import (
	"context"
	"errors"
	"io"
	"math"
	"net"
	"strconv"
	"strings"

	"github.com/jfjallid/go-smb/smb"
	"github.com/jfjallid/go-smb/spnego"
)

// smbSocket supplies an already-connected socket to the library's dial hook.
// It is not an outbound proxy and never performs a follow-up connection.
type smbSocket struct{ conn net.Conn }

func (s smbSocket) Dial(string, string) (net.Conn, error) { return s.conn, nil }

func (s smbSocket) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.conn, nil
}

func (s *Share) connectSMB(ctx context.Context) (*smb.Connection, func(), error) {
	conn, closeSocket, err := dialTCP(ctx, s.address)
	if err != nil {
		return nil, nil, err
	}
	_, portText, _ := net.SplitHostPort(s.address)
	port, _ := strconv.Atoi(portText)
	domain, user, hasDomain := strings.Cut(s.username, `\`)
	if !hasDomain {
		domain, user = "", s.username
	}

	client, err := smb.NewConnection(smb.Options{
		Host:                  s.endpoint.Hostname(),
		Port:                  port,
		SMB2Only:              true,
		RequireMessageSigning: true,
		DialTimeout:           dialTimeout,
		CreditReserveTimeout:  dialTimeout,
		ProxyDialer:           smbSocket{conn},
		Initiator: &spnego.NTLMInitiator{
			User:     user,
			Password: s.password,
			Domain:   domain,
			AuthMode: spnego.NTLMAuthCredentials,
		},
	})
	if err != nil {
		closeSocket()
		return nil, nil, smbError(ctx, err)
	}
	cleanup := func() {
		closeSocket()
		client.Close()
	}

	if client.IsGuestSession() {
		cleanup()
		return nil, nil, ErrAccessDenied
	}

	if err := client.TreeConnect(s.base[0]); err != nil {
		cleanup()
		return nil, nil, smbError(ctx, err)
	}
	flags, capabilities, _, ok := client.TreeConnectInfo(s.base[0])
	if !ok || flags&(smb.ShareFlagDFS|smb.ShareFlagDFSRoot) != 0 || capabilities&smb.ShareCapDFS != 0 {
		cleanup()
		return nil, nil, ErrUnsafePath
	}

	return client, cleanup, nil
}

func (s *Share) listSMB(ctx context.Context, parts []string) ([]Entry, error) {
	client, closeClient, err := s.connectSMB(ctx)
	if err != nil {
		return nil, err
	}
	defer closeClient()

	dir, closePath, err := openSMBPath(ctx, client, s.base[0], parts, true)
	if err != nil {
		return nil, err
	}
	defer closePath()

	var entries []Entry
	var flags byte = smb.RestartScans
	count := 0
	for {
		page, err := dir.QueryDirectoryContext(ctx, "*", flags, 0, 16<<10)
		if err != nil {
			return nil, smbError(ctx, err)
		}
		if len(page) == 0 {
			break
		}
		count += len(page)
		if count > maxListEntries {
			return nil, ErrTooLarge
		}

		for _, item := range page {
			if !item.IsJunction && validSegment(item.Name, ProtocolSMB) && item.Size <= math.MaxInt64 {
				entries = append(entries, Entry{Name: item.Name, IsDir: item.IsDir, Size: int64(item.Size)})
			}
		}
		flags = 0
	}
	return entries, nil
}

func (s *Share) readSMB(ctx context.Context, parts []string, window readWindow) ([]byte, error) {
	client, closeClient, err := s.connectSMB(ctx)
	if err != nil {
		return nil, err
	}
	defer closeClient()

	file, closePath, err := openSMBPath(ctx, client, s.base[0], parts, false)
	if err != nil {
		return nil, err
	}
	defer closePath()

	if !window.partial && file.EndOfFile > uint64(window.limit) {
		return nil, ErrTooLarge
	}
	offset := uint64(window.offset)
	if window.partial && offset >= file.EndOfFile {
		return nil, nil
	}
	window.offset = 0 // SMB can start the first network read at the requested offset.
	reader := smbReader(func(p []byte) (int, error) {
		n, err := file.ReadFileContext(ctx, p, offset)
		offset += uint64(n)
		if n == 0 && err == nil {
			return 0, io.ErrNoProgress
		}
		return n, err
	})
	return window.read(ctx, reader)
}

type smbReader func([]byte) (int, error)

func (r smbReader) Read(p []byte) (int, error) { return r(p) }

func openSMBPath(ctx context.Context, client *smb.Connection, share string, parts []string, wantDir bool) (*smb.File, func(), error) {
	opened := make([]*smb.File, 0, len(parts)+1)
	closePath := func() {
		for i := len(opened) - 1; i >= 0; i-- {
			if ctx.Err() != nil {
				// Connection teardown releases all handles on cancellation.
				return
			}
			opened[i].CloseFile()
		}
	}

	for i := min(1, len(parts)); i <= len(parts); i++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		isDir := i < len(parts) || wantDir
		opts := smbCreateOptions(isDir, i == len(parts))
		file, err := client.OpenFileExt(share, strings.Join(parts[:i], `\`), opts)
		if err != nil {
			closePath()
			return nil, nil, smbError(ctx, err)
		}

		opened = append(opened, file)
		if file.Attributes&smb.FileAttrReparsePoint != 0 || (file.Attributes&smb.FileAttrDirectory != 0) != isDir {
			closePath()
			return nil, nil, ErrUnsafePath
		}
	}
	return opened[len(opened)-1], closePath, nil
}

func smbCreateOptions(isDir, final bool) *smb.CreateReqOpts {
	// FileOpenReparsePoint applies to the last component only. Open each
	// component separately and retain its handle without delete/write sharing
	// until the operation finishes. Refuse any kind of reparse point.
	opts := &smb.CreateReqOpts{
		OpLockLevel:        smb.OpLockLevelNone,
		ImpersonationLevel: smb.ImpersonationLevelImpersonation,
		DesiredAccess:      smb.FAccMaskFileReadAttributes,
		ShareAccess:        smb.FileShareRead,
		CreateDisp:         smb.FileOpen,
		CreateOpts:         smb.FileOpenReparsePoint,
	}
	if isDir {
		opts.CreateOpts |= smb.FileDirectoryFile
	} else {
		opts.CreateOpts |= smb.FileNonDirectoryFile
	}
	if final {
		// The wire bit for listing a directory equals the bit for reading a file.
		opts.DesiredAccess |= smb.FAccMaskFileReadData
	}
	return opts
}

func smbError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	switch {
	case errors.Is(err, smb.StatusMap[smb.StatusNoSuchFile]),
		errors.Is(err, smb.StatusMap[smb.StatusObjectNameNotFound]),
		errors.Is(err, smb.StatusMap[smb.StatusObjectPathNotFound]):
		return ErrNotFound
	case errors.Is(err, smb.StatusMap[smb.StatusAccessDenied]),
		errors.Is(err, smb.StatusMap[smb.StatusLogonFailure]):
		return ErrAccessDenied
	case errors.Is(err, smb.StatusMap[smb.StatusFileIsADirectory]),
		errors.Is(err, smb.StatusMap[smb.StatusNotADirectory]):
		return ErrUnsafePath
	default:
		return ErrUnavailable
	}
}
