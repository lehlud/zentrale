package fileshare

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestSFTPVerifiesHostKeyAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "alice")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "outside.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, "link-dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(dir, "bob")); err != nil {
		t.Fatal(err)
	}

	endpoint, fingerprint, authAttempts := startTestSFTP(t)
	config := Config{
		Protocol:      ProtocolSFTP,
		Endpoint:      endpoint,
		Root:          filepath.Join(dir, "{username}"),
		Username:      "share-read-only",
		Password:      "test-only",
		HostKeySHA256: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	}
	share, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := share.List(ctx, "alice", ""); !errors.Is(err, ErrHostKey) || authAttempts.Load() != 0 {
		t.Fatalf("wrong host key: err=%v auth attempts=%d", err, authAttempts.Load())
	}

	config.HostKeySHA256 = fingerprint
	share, err = New(config)
	if err != nil {
		t.Fatal(err)
	}

	entries, err := share.List(ctx, "alice", "")
	if err != nil || len(entries) != 1 || entries[0] != (Entry{Name: "note.txt", Size: 4}) {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	data, err := share.Read(ctx, "alice", "note.txt", 4)
	if err != nil || string(data) != "file" {
		t.Fatalf("data=%q err=%v", data, err)
	}

	for _, tc := range [][2]string{
		{"alice", "link.txt"},
		{"alice", "link-dir/outside.txt"},
		{"bob", "outside.txt"},
	} {
		if data, err := share.Read(ctx, tc[0], tc[1], 20); !errors.Is(err, ErrUnsafePath) || data != nil {
			t.Errorf("username %q path %q: data=%q err=%v", tc[0], tc[1], data, err)
		}
	}
}

func startTestSFTP(t *testing.T) (string, string, *atomic.Int32) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}

	var attempts atomic.Int32
	config := &ssh.ServerConfig{
		PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			attempts.Add(1)
			if conn.User() != "share-read-only" || string(password) != "test-only" {
				return nil, errors.New("authentication failed")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSFTP(socket, config)
		}
	}()
	return "sftp://" + listener.Addr().String(), ssh.FingerprintSHA256(signer.PublicKey()), &attempts
}

func serveTestSFTP(socket net.Conn, config *ssh.ServerConfig) {
	defer socket.Close()
	conn, channels, requests, err := ssh.NewServerConn(socket, config)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(requests)

	request, ok := <-channels
	if !ok || request.ChannelType() != "session" {
		return
	}
	channel, requests, err := request.Accept()
	if err != nil {
		return
	}
	defer channel.Close()

	req, ok := <-requests
	if !ok {
		return
	}
	var payload struct{ Name string }
	ok = req.Type == "subsystem" && ssh.Unmarshal(req.Payload, &payload) == nil && payload.Name == "sftp"
	req.Reply(ok, nil)
	if !ok {
		return
	}

	server, err := sftp.NewServer(channel, sftp.ReadOnly())
	if err != nil {
		return
	}
	defer server.Close()
	server.Serve()
}
