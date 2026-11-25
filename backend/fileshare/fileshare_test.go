package fileshare

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jfjallid/go-smb/smb"
)

func TestResolveScope(t *testing.T) {
	share, err := New(Config{Protocol: ProtocolWebDAV, Endpoint: "https://host.example/dav", Root: "/users/{username}/exports/"})
	if err != nil {
		t.Fatal(err)
	}

	root, full, err := share.resolve("alice", "invoices/new bill.txt")
	if err != nil || strings.Join(root, "/") != "users/alice/exports" ||
		strings.Join(full, "/") != "users/alice/exports/invoices/new bill.txt" {
		t.Fatalf("root=%q full=%q err=%v", root, full, err)
	}

	for _, username := range []string{"", "..", "alice/bob", "bob\\sub", "%2e%2e", "{username}"} {
		if _, _, err := share.resolve(username, ""); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("username %q: unexpected error %v", username, err)
		}
	}

	for _, path := range []string{"/file", "a/../b", "./file", "a//b", "a/", `a\b`, `\\host\share`, "%2e%2e/a", "a%2fb", "a\x00b", "https://foreign/a", "a:b", "..;ignored/a"} {
		if _, err := share.List(context.Background(), "alice", path); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("path %q: unexpected error %v", path, err)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	fingerprint := "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, cfg := range []Config{
		{Protocol: ProtocolWebDAV, Endpoint: "https://host.example/a", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example/base", Root: "share"},
		{Protocol: ProtocolSFTP, Endpoint: "sftp://host.example", Root: "home/{username}", HostKeySHA256: fingerprint},
		{Protocol: ProtocolSMB, Endpoint: "smb://host.example:1445/Share$", Root: "team"},
	} {
		if _, err := New(cfg); err != nil {
			t.Errorf("protocol %q: %v", cfg.Protocol, err)
		}
	}

	for _, cfg := range []Config{
		{Protocol: "ftp", Endpoint: "ftp://host.example", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://user:secret@host.example/", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example/a?token=secret", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example:0/", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example/a%2f..", Root: "/"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example/", Root: "prefix-{username}"},
		{Protocol: ProtocolWebDAV, Endpoint: "http://host.example/", Root: ""},
		{Protocol: ProtocolSFTP, Endpoint: "sftp://host.example", Root: "/"},
		{Protocol: ProtocolSFTP, Endpoint: "sftp://host.example/sub", Root: "/", HostKeySHA256: fingerprint},
		{Protocol: ProtocolSMB, Endpoint: "smb://host.example/IPC$", Root: "/"},
		{Protocol: ProtocolSMB, Endpoint: "smb://host.example/Share", Root: "trailing. /a"},
	} {
		if _, err := New(cfg); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("protocol %q: unexpected error %v", cfg.Protocol, err)
		}
	}
}

func TestSMBOpenUsesNoFollowReadOnly(t *testing.T) {
	for _, tc := range []struct {
		isDir, final bool
		access       uint32
		options      uint32
	}{
		{true, false, smb.FAccMaskFileReadAttributes, smb.FileDirectoryFile},
		{true, true, smb.FAccMaskFileReadAttributes | smb.FAccMaskFileReadData, smb.FileDirectoryFile},
		{false, true, smb.FAccMaskFileReadAttributes | smb.FAccMaskFileReadData, smb.FileNonDirectoryFile},
	} {
		got := smbCreateOptions(tc.isDir, tc.final)
		if got.CreateDisp != smb.FileOpen || got.CreateOpts != tc.options|smb.FileOpenReparsePoint ||
			got.ShareAccess != smb.FileShareRead || got.DesiredAccess != tc.access {
			t.Errorf("isDir=%t final=%t: %+v", tc.isDir, tc.final, got)
		}
	}
}
