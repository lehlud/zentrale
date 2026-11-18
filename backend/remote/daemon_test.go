package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
)

func TestDaemonTunnelChangingDeadlineInterruptsPendingRead(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		socket, err := listener.Accept()
		if err == nil {
			accepted <- socket
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	tunnel, err := connection.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	socket := <-accepted
	defer socket.Close()
	done := make(chan error, 1)
	go func() { _, err := tunnel.Read(make([]byte, 1)); done <- err }()
	time.Sleep(25 * time.Millisecond)
	if err := tunnel.SetReadDeadline(time.Now().Add(25 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("deadline error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("deadline did not interrupt the pending read")
	}
	if err := tunnel.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := socket.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := io.ReadFull(tunnel, buffer); err != nil || string(buffer) != "x" {
		t.Fatalf("connection did not recover after clearing deadline: %q %v", buffer, err)
	}
}

func TestOneDaemonPerInstallationAndPhysicalHostAcrossAliases(t *testing.T) {
	connector, store := daemonTestConnector(t)
	machine, err := connector.Machine("daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	machine.ID = "host-alias"
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	alias, err := connector.Dial(ctx, "host-alias")
	if err != nil {
		t.Fatal(err)
	}
	defer alias.Close()
	firstWire, err := first.daemon.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	aliasWire, err := alias.daemon.connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if firstWire.generation != aliasWire.generation {
		t.Fatal("host aliases spawned separate daemons")
	}
	peer := &Connector{Store: store}
	defer peer.Close()
	reattached, err := peer.Dial(ctx, "host-alias")
	if err != nil {
		t.Fatal(err)
	}
	defer reattached.Close()
	wire, err := reattached.daemon.connect(ctx)
	if err != nil || wire.generation != firstWire.generation {
		t.Fatalf("new client spawned another installation daemon: %v", err)
	}
}

type brokenDaemonInput struct{}

func (brokenDaemonInput) Read([]byte) (int, error) { return 0, errors.New("broken input source") }

func TestDaemonInputFailureInterruptsTheOutputWait(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = RunPipe(ctx, connection, "cat", brokenDaemonInput{})
	if err == nil || !strings.Contains(err.Error(), "broken input source") {
		t.Fatalf("input failure was hidden: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("failed input hung until command timeout")
	}
}

func daemonTestConnector(t *testing.T) (*Connector, *datastore.Instance) {
	t.Helper()
	peer, err := testssh.StartWithCommandHandler(func(command string, input io.Reader, output, diagnostic io.Writer) int {
		process := exec.Command("/bin/sh", "-c", command)
		process.Stdout, process.Stderr = output, diagnostic
		stdin, err := process.StdinPipe()
		if err != nil {
			return 1
		}
		go func() { _, _ = io.Copy(stdin, input); _ = stdin.Close() }()
		if err := process.Run(); err != nil {
			fmt.Fprintln(diagnostic, err)
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	store, err := datastore.Open(filepath.Join(t.TempDir(), "daemon.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := DecodePublicKey(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "daemon-host", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID, Sandbox: SandboxDisabled}
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	connector := &Connector{Store: store}
	t.Cleanup(func() {
		connector.daemonMu.Lock()
		clients := make([]*daemonClient, 0, len(connector.daemons))
		for _, client := range connector.daemons {
			clients = append(clients, client)
		}
		connector.daemonMu.Unlock()
		for _, client := range clients {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = client.call(ctx, map[string]any{"op": "shutdown"}, nil)
			cancel()
		}
		connector.Close()
	})
	return connector, store
}

func TestDaemonReconnectPreservesExecutionAndExitStatus(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	gate := filepath.Join(t.TempDir(), "continue")
	starts := filepath.Join(t.TempDir(), "starts")
	first := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		output, err := RunStreaming(ctx, connection, "printf x >> "+Quote(starts)+"; printf before; while ! test -f "+Quote(gate)+"; do sleep 0.02; done; printf after; exit 7", func(output string) {
			if strings.Contains(output, "before") {
				select {
				case first <- struct{}{}:
				default:
				}
			}
		})
		var status interface{ ExitStatus() int }
		if !strings.Contains(output, "beforeafter") || !errors.As(err, &status) || status.ExitStatus() != 7 {
			err = fmt.Errorf("output=%q exit=%v", output, err)
		} else {
			err = nil
		}
		done <- err
	}()
	select {
	case <-first:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	connection.daemon.mu.Lock()
	wire := connection.daemon.wire
	connection.daemon.mu.Unlock()
	wire.close()
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(starts)
	if err != nil || string(value) != "x" {
		t.Fatalf("command replayed: %q %v", value, err)
	}
}

func TestDaemonStartsMicroVMTerminalWhileAgentCommandRuns(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the VM control commands so this runs without KVM. The SSH
	// transport, daemon, and terminal relay are the production implementations.
	dir := t.TempDir()
	runner := `#!/bin/sh
case "$1:$2:$4" in
    -I:-c:ensure) exit 0 ;;
    -I:-c:stage) cat >/dev/null && printf '%s\n' /vm/session/requests/0123456789abcdef0123456789abcdef ;;
    -I:-c:run) exec ` + terminalCommandWithProtocol("exec /bin/sh -i", true) + ` ;;
    *) exec ` + Quote(python) + ` "$@" ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "python3"), []byte(runner), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	connector, _ := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	gate := filepath.Join(dir, "continue")
	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		jobContext := WithInvocation(ctx, Invocation{ID: "working-agent-job"})
		command := "printf started; while ! test -f " + Quote(gate) + "; do sleep 0.02; done; printf completed"
		output, err := RunStreaming(jobContext, connection, command, func(output string) {
			if strings.Contains(output, "started") {
				select {
				case started <- struct{}{}:
				default:
				}
			}
		})
		if err != nil || output != "startedcompleted" {
			err = fmt.Errorf("agent command: output=%q, error=%v", output, err)
		}
		done <- err
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("agent exited before terminal start: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	persisted := 0
	terminalContext := WithInvocation(ctx, Invocation{ID: "terminal-user", Persist: func(string) error {
		persisted++
		return nil
	}})
	machine := domain.Machine{Sandbox: SandboxMicroVM}
	project := domain.Project{ID: "test-project", Directory: dir}
	terminal, err := StartTerminal(terminalContext, connection, machine, project, EditorTarget{}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if persisted != 1 {
		t.Fatalf("terminal receipt saved by %d operations, want 1", persisted)
	}
	if err := terminal.Input([]byte("touch " + Quote(gate) + "; printf 'terminal-%s\\n' ready; exit\n")); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(terminal)
	if err != nil || !strings.Contains(string(output), "terminal-ready") {
		t.Fatalf("terminal: output=%q, error=%v", output, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDaemonNewConnectorReattachesWithoutReplaying(t *testing.T) {
	connector, store := daemonTestConnector(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	id := daemonResourceID()
	resource, err := newDaemonResource(WithInvocation(ctx, Invocation{ID: id}), connection, map[string]any{"kind": "process", "command": "sleep 0.1; printf survived", "merge": true})
	if err != nil {
		t.Fatal(err)
	}
	connector.Close()
	restarted := &Connector{Store: store}
	t.Cleanup(restarted.Close)
	connection, err = restarted.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	output, err := Run(WithInvocation(ctx, Invocation{ID: id, Generation: resource.generation, AttachOnly: true}), connection, "this command must never execute")
	if err != nil || output != "survived" {
		t.Fatalf("reattach: %q %v", output, err)
	}
	_ = connection.daemon.call(ctx, map[string]any{"op": "shutdown"}, nil)
}

func TestDaemonTunnelSurvivesTransportLoss(t *testing.T) {
	connector, _ := daemonTestConnector(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		socket, err := listener.Accept()
		if err == nil {
			accepted <- socket
			_, _ = io.Copy(socket, socket)
			_ = socket.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	tunnel, err := connection.DialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	socket := <-accepted
	defer socket.Close()
	for index, text := range []string{"before", "after"} {
		if index == 1 {
			connection.daemon.mu.Lock()
			wire := connection.daemon.wire
			connection.daemon.mu.Unlock()
			wire.close()
		}
		if _, err := tunnel.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
		buffer := make([]byte, len(text))
		if _, err := io.ReadFull(tunnel, buffer); err != nil || string(buffer) != text {
			t.Fatalf("tunnel: %q %v", buffer, err)
		}
	}
}
