package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"golang.org/x/crypto/ssh"
)

func TestTerminalSSHProtocolAndCancellation(t *testing.T) {
	frames := make(chan map[string]any, 4)
	closed := make(chan struct{})
	peer, err := testssh.StartWithCommandHandler(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		defer close(closed)
		if !strings.Contains(command, "--protocol") || strings.Contains(command, "vscode") {
			t.Error("wrong terminal command")
		}
		decoder := json.NewDecoder(stdin)
		var frame map[string]any
		if err := decoder.Decode(&frame); err != nil {
			return 1
		}
		frames <- frame
		io.WriteString(stdout, "\x00ZENTRALE_TERMINAL_READY\n")
		stdout.Write([]byte{0, 255, 'x'})
		for {
			var frame map[string]any
			if decoder.Decode(&frame) != nil {
				return 0
			}
			frames <- frame
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(peer.Host, strconv.Itoa(peer.Port)), &ssh.ClientConfig{User: "test", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	terminal, err := StartTerminal(ctx, &Connection{Client: client}, domain.Machine{Sandbox: SandboxDisabled}, domain.Project{ID: "project", Directory: "/tmp"}, EditorTarget{AgentID: "owner", TemporaryDirectory: "/tmp/terminal"}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	initial := <-frames
	if initial["cols"] != float64(80) || initial["rows"] != float64(24) {
		t.Fatal(initial)
	}
	output := make([]byte, 3)
	if _, err := io.ReadFull(terminal, output); err != nil || string(output) != string([]byte{0, 255, 'x'}) {
		t.Fatal(output, err)
	}
	if err := terminal.Input([]byte{3, 255}); err != nil {
		t.Fatal(err)
	}
	input := <-frames
	if input["data"] != base64.StdEncoding.EncodeToString([]byte{3, 255}) {
		t.Fatal(input)
	}
	if err := terminal.Resize(132, 43); err != nil {
		t.Fatal(err)
	}
	resize := <-frames
	if fmt.Sprint(resize["type"]) != "resize" || resize["cols"] != float64(132) {
		t.Fatal(resize)
	}
	if terminal.Resize(0, 24) == nil || terminal.Input(make([]byte, 65537)) == nil {
		t.Fatal("bounds not enforced")
	}
	cancel()
	terminal.Close()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("SSH stdin/session leaked")
	}
}

type terminalKeepalivePeer struct {
	ssh.Conn
	started   chan struct{}
	reply     chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	requests  atomic.Int32
}

func (peer *terminalKeepalivePeer) SendRequest(name string, wantReply bool, payload []byte) (bool, []byte, error) {
	if name != "keepalive@openssh.com" || !wantReply || len(payload) != 0 {
		return false, nil, errors.New("unexpected keepalive request")
	}
	peer.requests.Add(1)
	peer.startOnce.Do(func() { close(peer.started) })
	select {
	case <-peer.reply:
		// Servers may reject this request: a response still proves liveness.
		return false, nil, nil
	case <-peer.closed:
		return false, nil, io.EOF
	}
}

func (peer *terminalKeepalivePeer) Close() error {
	peer.closeOnce.Do(func() { close(peer.closed) })
	return nil
}

func TestTerminalKeepaliveStopPreservesRetainedSSHTransport(t *testing.T) {
	peer := &terminalKeepalivePeer{started: make(chan struct{}), reply: make(chan struct{}), closed: make(chan struct{})}
	client := &ssh.Client{Conn: peer}
	connection := &Connection{Client: client, shared: &sharedConnection{users: 1, chain: []*ssh.Client{client}}}
	defer connection.Close()
	retained, err := connection.Retain()
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- terminalKeepalive(ctx, time.Millisecond, time.Second, retained.Ping) }()
	select {
	case <-peer.started:
	case <-time.After(time.Second):
		t.Fatal("no SSH keepalive sent")
	}
	// Model the process's OnClose callback while an SSH probe is in flight.
	stop()
	retained.Close()
	select {
	case <-peer.closed:
		t.Fatal("closing one terminal disconnected the shared SSH transport")
	case <-time.After(20 * time.Millisecond):
	}
	close(peer.reply)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("keepalive worker did not stop")
	}
	probe, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := connection.Ping(probe); err != nil {
		t.Fatalf("other retained user lost its SSH transport: %v", err)
	}
	select {
	case <-peer.closed:
		t.Fatal("healthy transport was closed")
	default:
	}
}

func TestTerminalKeepaliveTimesOutUnresponsiveSSH(t *testing.T) {
	peer := &terminalKeepalivePeer{started: make(chan struct{}), reply: make(chan struct{}), closed: make(chan struct{})}
	connection := &Connection{Client: &ssh.Client{Conn: peer}}
	err := terminalKeepalive(context.Background(), time.Millisecond, 20*time.Millisecond, connection.Ping)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wanted bounded SSH timeout, got %v", err)
	}
	select {
	case <-peer.closed:
	default:
		t.Fatal("unresponsive SSH transport was not invalidated")
	}
}

func TestTerminalKeepaliveRepeatsForIdleShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var probes int
	err := terminalKeepalive(ctx, time.Millisecond, time.Second, func(probe context.Context) error {
		if _, ok := probe.Deadline(); !ok {
			t.Error("keepalive has no deadline")
		}
		probes++
		if probes == 3 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) || probes != 3 {
		t.Fatal(err, probes)
	}
}

func TestTerminalExitDoesNotInventSuccessAfterTransportLoss(t *testing.T) {
	for _, exitError := range []error{nil, &ssh.ExitMissingError{}, io.ErrUnexpectedEOF} {
		done := make(chan struct{})
		close(done)
		terminal := &Terminal{Process: &Process{done: done, exitError: exitError}}
		code := terminal.Exit(context.Background())
		if exitError == nil {
			if code == nil || *code != 0 {
				t.Fatal("clean exit lost its status", code)
			}
		} else if code != nil {
			t.Fatalf("transport loss reported as shell exit %d: %v", *code, exitError)
		}
	}
}
