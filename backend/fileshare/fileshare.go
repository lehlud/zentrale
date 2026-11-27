// Package fileshare provides backend-only, read-only access to configured shares.
package fileshare

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	ProtocolSMB         = "smb"
	ProtocolSFTP        = "sftp"
	ProtocolWebDAV      = "webdav"
	ProtocolDomainShare = "domainshare"

	// MaxReadBytes bounds a single file read independently of the caller's limit.
	MaxReadBytes = 16 << 20

	maxListEntries   = 10_000
	maxPathBytes     = 2048
	maxPathSegments  = 32
	dialTimeout      = 5 * time.Second
	operationTimeout = 20 * time.Second
	usernameSegment  = "{username}"
)

var (
	ErrInvalidPath  = errors.New("invalid fileshare path")
	ErrUnsafePath   = errors.New("fileshare path is a link or is not a regular file or directory")
	ErrNotFound     = errors.New("fileshare path not found")
	ErrAccessDenied = errors.New("fileshare access denied")
	ErrTooLarge     = errors.New("fileshare response exceeds the size limit")
	ErrUnavailable  = errors.New("fileshare connection or operation failed")
	ErrHostKey      = errors.New("fileshare SSH host key does not match its configured fingerprint")
)

// Config defines a single share. Endpoint is smb://host[:port]/share,
// sftp://host[:port], or http(s)://host[:port][/dav-base]. Root is a share-
// relative path for SMB/WebDAV and relative to the SSH server's / for SFTP.
// It may start with /; use / to explicitly select the protocol's root.
// {username} is supported only as a complete Root segment. For SFTP,
// HostKeySHA256 must be the server fingerprint in OpenSSH SHA256:… format.
// For SMB, Username may be written as DOMAIN\user. For DomainShare, Endpoint
// is the HTTPS instance URL and Password is the agent token. DomainShare
// determines the root and access rights from appUsername on every request.
type Config struct {
	Protocol      string
	Endpoint      string
	Root          string
	Username      string
	Password      string
	HostKeySHA256 string
}

// Entry describes one immediate child of a listed directory. Names are single
// path segments, usable as relative path components in subsequent calls.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Label string `json:"label,omitempty"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
}

// Share keeps its connection details private. It is safe to use concurrently.
type Share struct {
	protocol    string
	endpoint    url.URL
	address     string
	base        []string
	root        []string
	username    string
	password    string
	fingerprint string
}

// New validates an administrative share definition without making a connection.
func New(config Config) (*Share, error) {
	protocol := strings.ToLower(strings.TrimSpace(config.Protocol))
	if protocol != ProtocolSMB && protocol != ProtocolSFTP && protocol != ProtocolWebDAV && protocol != ProtocolDomainShare {
		return nil, errors.New("unsupported fileshare protocol")
	}

	endpoint, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Opaque != "" ||
		endpoint.ForceQuery || endpoint.RawQuery != "" || strings.Contains(config.Endpoint, "#") ||
		strings.Contains(endpoint.Host, "%") {
		return nil, errors.New("invalid fileshare endpoint; credentials belong in the separate credentials fields")
	}

	port := endpoint.Port()
	if port != "" {
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil || value == 0 {
			return nil, errors.New("invalid fileshare endpoint port")
		}
		port = strconv.FormatUint(value, 10)
	} else if strings.HasSuffix(endpoint.Host, ":") {
		return nil, errors.New("invalid fileshare endpoint port")
	}

	base, err := urlPathSegments(endpoint)
	if err != nil || !validSegments(base, protocol, false) {
		return nil, errors.New("invalid fileshare endpoint path")
	}

	switch protocol {
	case ProtocolSMB:
		if endpoint.Scheme != "smb" || len(base) != 1 || isReservedSMBShare(base[0]) {
			return nil, errors.New("SMB endpoint must identify a disk share: smb://host[:port]/share")
		}
		if port == "" {
			port = "445"
		}
	case ProtocolSFTP:
		if endpoint.Scheme != "sftp" || len(base) != 0 {
			return nil, errors.New("SFTP endpoint must be sftp://host[:port]; specify the remote path in the root")
		}
		if port == "" {
			port = "22"
		}
		if !validFingerprint(config.HostKeySHA256) {
			return nil, errors.New("SFTP requires an OpenSSH SHA256 host key fingerprint")
		}
	case ProtocolWebDAV:
		if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
			return nil, errors.New("WebDAV endpoint must use HTTP or HTTPS")
		}
	case ProtocolDomainShare:
		if endpoint.Scheme != "https" {
			return nil, errors.New("DomainShare endpoint must use HTTPS")
		}
		if config.Password == "" || len(config.Password) > 512 || strings.ContainsFunc(config.Password, func(r rune) bool {
			return r < '!' || r > '~'
		}) {
			return nil, errors.New("a valid DomainShare agent token is required")
		}
		config.Root = "/"
	}

	if config.Root == "" {
		return nil, errors.New("fileshare root is required; use / to explicitly select the protocol root")
	}

	root, err := splitPath(strings.TrimSuffix(strings.TrimPrefix(config.Root, "/"), "/"), protocol, true)
	if err != nil || strings.Contains(config.Root, "//") {
		return nil, errors.New("invalid fileshare root; {username} must be an entire path segment")
	}

	return &Share{
		protocol:    protocol,
		endpoint:    *endpoint,
		address:     net.JoinHostPort(endpoint.Hostname(), port),
		base:        base,
		root:        root,
		username:    config.Username,
		password:    config.Password,
		fingerprint: config.HostKeySHA256,
	}, nil
}

// List returns the immediate children of relativePath (empty for the root).
// appUsername is the authenticated application's username, not the share login.
func (s *Share) List(ctx context.Context, appUsername, relativePath string) ([]Entry, error) {
	root, path, err := s.resolve(appUsername, relativePath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()

	var entries []Entry
	switch s.protocol {
	case ProtocolSMB:
		entries, err = s.listSMB(ctx, path)
	case ProtocolSFTP:
		entries, err = s.listSFTP(ctx, path)
	case ProtocolWebDAV:
		entries, err = s.listDAV(ctx, root, path)
	case ProtocolDomainShare:
		entries, err = s.listDomainShare(ctx, appUsername, path)
	default:
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(entries) > maxListEntries {
		return nil, ErrTooLarge
	}

	slices.SortFunc(entries, func(a, b Entry) int { return strings.Compare(a.Name, b.Name) })
	prefix := relativePath
	if prefix != "" {
		prefix += "/"
	}
	for i := range entries {
		entries[i].Path = prefix + entries[i].Name
	}
	return entries, nil
}

// Read returns all bytes from a regular file, never partial results. The caller
// must specify a positive limit that does not exceed MaxReadBytes.
func (s *Share) Read(ctx context.Context, appUsername, relativePath string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes > MaxReadBytes {
		return nil, fmt.Errorf("fileshare read limit must be between 1 and %d bytes", MaxReadBytes)
	}
	return s.read(ctx, appUsername, relativePath, readWindow{limit: maxBytes})
}

// ReadChunk returns up to limit+1 bytes, including one lookahead byte to report
// whether there is more data. The offset and limit apply to the extracted text
// for an indexed DomainShare document, and to file bytes otherwise.
func (s *Share) ReadChunk(ctx context.Context, appUsername, relativePath string, offset, limit int64) ([]byte, error) {
	if offset < 0 || offset > MaxReadBytes || limit <= 0 || limit >= MaxReadBytes {
		return nil, ErrInvalidPath
	}
	return s.read(ctx, appUsername, relativePath, readWindow{offset: offset, limit: limit, partial: true})
}

func (s *Share) read(ctx context.Context, appUsername, relativePath string, window readWindow) ([]byte, error) {
	if relativePath == "" {
		return nil, ErrInvalidPath
	}

	root, path, err := s.resolve(appUsername, relativePath)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()

	switch s.protocol {
	case ProtocolSMB:
		return s.readSMB(ctx, path, window)
	case ProtocolSFTP:
		return s.readSFTP(ctx, path, window)
	case ProtocolWebDAV:
		return s.readDAV(ctx, root, path, window)
	case ProtocolDomainShare:
		return s.readDomainShare(ctx, appUsername, path, window)
	default:
		return nil, ErrUnavailable
	}
}

type readWindow struct {
	offset  int64
	limit   int64
	partial bool
}

func (w readWindow) exceeds(size int64) bool {
	return !w.partial && size > w.limit
}

func (w readWindow) read(ctx context.Context, source io.Reader) ([]byte, error) {
	if !w.partial {
		return readBounded(ctx, source, w.limit)
	}

	if w.offset > 0 {
		if file, ok := source.(io.Seeker); ok {
			if _, err := file.Seek(w.offset, io.SeekStart); err != nil {
				return nil, operationError(ctx)
			}
		} else if _, err := io.CopyN(io.Discard, source, w.offset); err != nil {
			if errors.Is(err, io.EOF) && ctx.Err() == nil {
				return nil, nil
			}
			return nil, operationError(ctx)
		}
	}
	return readBounded(ctx, io.LimitReader(source, w.limit+1), w.limit+1)
}

func (s *Share) resolve(appUsername, relative string) (root, full []string, err error) {
	root = slices.Clone(s.root)
	for i, part := range root {
		if part != usernameSegment {
			continue
		}
		if !validSegment(appUsername, s.protocol) || appUsername == usernameSegment {
			return nil, nil, ErrInvalidPath
		}
		root[i] = appUsername
	}

	parts, err := splitPath(relative, s.protocol, false)
	if err != nil {
		return nil, nil, err
	}

	full = slices.Concat(root, parts)
	if len(full)+len(s.base) > maxPathSegments ||
		len(strings.Join(full, "/"))+len(strings.Join(s.base, "/")) > maxPathBytes {
		return nil, nil, ErrInvalidPath
	}

	return root, full, nil
}

func splitPath(path, protocol string, allowTemplate bool) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if len(path) > maxPathBytes {
		return nil, ErrInvalidPath
	}

	parts := strings.Split(path, "/")
	if !validSegments(parts, protocol, allowTemplate) {
		return nil, ErrInvalidPath
	}
	return parts, nil
}

func validSegments(parts []string, protocol string, allowTemplate bool) bool {
	if len(parts) > maxPathSegments {
		return false
	}

	for _, part := range parts {
		if !validSegment(part, protocol) ||
			(allowTemplate && strings.Contains(part, usernameSegment) && part != usernameSegment) {
			return false
		}
	}
	return true
}

func validSegment(segment, protocol string) bool {
	if segment == "" || segment == "." || segment == ".." || len(segment) > 255 ||
		!utf8.ValidString(segment) || strings.ContainsAny(segment, `/\%`) ||
		(protocol != ProtocolDomainShare && strings.ContainsRune(segment, ':')) ||
		strings.ContainsFunc(segment, unicode.IsControl) {
		return false
	}
	if protocol == ProtocolSMB {
		return !strings.ContainsAny(segment, `"*?<>|`) &&
			!strings.HasSuffix(segment, ".") && !strings.HasSuffix(segment, " ")
	}
	if protocol == ProtocolWebDAV {
		// Some DAV servers interpret semicolons as path parameters.
		return !strings.ContainsRune(segment, ';')
	}
	return true
}

func isReservedSMBShare(name string) bool {
	return strings.EqualFold(name, "IPC$") || strings.EqualFold(name, "PRINT$")
}

// urlPathSegments deliberately splits the escaped path before unescaping it.
// A slash encoded inside a segment must not turn into a path separator.
func urlPathSegments(u *url.URL) ([]string, error) {
	path := u.EscapedPath()
	if path == "" || path == "/" {
		return nil, nil
	}
	if path[0] != '/' || len(path) > maxPathBytes*3 || strings.Contains(path, "//") {
		return nil, ErrInvalidPath
	}

	parts := strings.Split(strings.TrimSuffix(path[1:], "/"), "/")
	for i, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return nil, ErrInvalidPath
		}
		parts[i] = decoded
	}
	return parts, nil
}

func validFingerprint(fingerprint string) bool {
	digest, ok := strings.CutPrefix(fingerprint, "SHA256:")
	if !ok {
		return false
	}
	decoded, err := base64.RawStdEncoding.Strict().DecodeString(digest)
	return err == nil && len(decoded) == 32 && base64.RawStdEncoding.EncodeToString(decoded) == digest
}

func readBounded(ctx context.Context, reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, operationError(ctx)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

// Transport errors can contain the configured hostname, remote path, or text
// supplied by the remote server; never return them to a caller.
func operationError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}
