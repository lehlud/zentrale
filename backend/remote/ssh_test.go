package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"golang.org/x/crypto/ssh"
)

type sshFixture struct {
	listener     net.Listener
	hostKey      ssh.PublicKey
	occupancy    string
	editorSocket string
	editorStarts int
	mutex        sync.Mutex
	connections  []net.Conn
	workers      sync.WaitGroup
}

func newSSHFixture(t *testing.T, occupancy string) *sshFixture {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return newSSHFixtureWithSigners(t, occupancy, signer)
}

func newSSHFixtureWithSigners(t *testing.T, occupancy string, signers ...ssh.Signer) *sshFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture := &sshFixture{listener: listener, hostKey: signers[0].PublicKey(), occupancy: occupancy}
	configuration := &ssh.ServerConfig{PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) { return &ssh.Permissions{}, nil }}
	for _, signer := range signers {
		configuration.AddHostKey(signer)
	}
	fixture.workers.Go(func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			fixture.mutex.Lock()
			fixture.connections = append(fixture.connections, socket)
			fixture.mutex.Unlock()
			fixture.workers.Go(func() { fixture.serve(socket, configuration) })
		}
	})
	t.Cleanup(func() {
		_ = listener.Close()
		fixture.mutex.Lock()
		for _, connection := range fixture.connections {
			_ = connection.Close()
		}
		fixture.mutex.Unlock()
		fixture.workers.Wait()
	})
	return fixture
}

func (fixture *sshFixture) serve(socket net.Conn, configuration *ssh.ServerConfig) {
	connection, channels, requests, err := ssh.NewServerConn(socket, configuration)
	if err != nil {
		_ = socket.Close()
		return
	}
	defer connection.Close()
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		fixture.workers.Go(func() {
			if channelRequest.ChannelType() == "direct-tcpip" || channelRequest.ChannelType() == "direct-streamlocal@openssh.com" {
				fixture.forward(channelRequest)
				return
			}
			if channelRequest.ChannelType() != "session" {
				_ = channelRequest.Reject(ssh.UnknownChannelType, "unsupported")
				return
			}
			channel, requests, err := channelRequest.Accept()
			if err != nil {
				return
			}
			defer channel.Close()
			for request := range requests {
				if request.Type == "exec" {
					var payload struct{ Command string }
					_ = ssh.Unmarshal(request.Payload, &payload)
					_ = request.Reply(true, nil)
					if strings.Contains(payload.Command, "ZENTRALE_EDITOR_READY ") && fixture.editorSocket != "" {
						fixture.mutex.Lock()
						fixture.editorStarts++
						fixture.mutex.Unlock()
						_, _ = io.WriteString(channel, "ZENTRALE_EDITOR_READY {\"socketPath\":\""+fixture.editorSocket+"\"}\n")
						for request := range requests {
							if request.Type == "signal" {
								_ = request.Reply(true, nil)
								_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							}
						}
						return
					}
					if strings.Contains(payload.Command, "who |") {
						_, _ = io.WriteString(channel, fixture.occupancy)
					} else if payload.Command == shellCommand("stream") {
						_, _ = io.WriteString(channel, "first chunk\n")
						time.Sleep(20 * time.Millisecond)
						_, _ = io.WriteString(channel, "second chunk\n")
					} else if payload.Command == shellCommand("wait") {
						for request := range requests {
							if request.Type == "signal" {
								_ = request.Reply(true, nil)
								return
							}
						}
						return
					} else {
						_, _ = io.WriteString(channel, "executed remotely")
					}
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					return
				}
				_ = request.Reply(false, nil)
			}
		})
	}
}

func (fixture *sshFixture) forward(request ssh.NewChannel) {
	var destination struct {
		Host       string
		Port       uint32
		Origin     string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(request.ExtraData(), &destination); request.ChannelType() == "direct-tcpip" && err != nil {
		_ = request.Reject(ssh.ConnectionFailed, "bad destination")
		return
	}
	network, address := "tcp", net.JoinHostPort(destination.Host, formatPort(int(destination.Port)))
	if request.ChannelType() == "direct-streamlocal@openssh.com" {
		var destination struct {
			SocketPath, Reserved string
			ReservedPort         uint32
		}
		if err := ssh.Unmarshal(request.ExtraData(), &destination); err != nil {
			_ = request.Reject(ssh.ConnectionFailed, "bad socket destination")
			return
		}
		network, address = "unix", destination.SocketPath
	}
	target, err := net.Dial(network, address)
	if err != nil {
		_ = request.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	defer target.Close()
	channel, requests, err := request.Accept()
	if err != nil {
		return
	}
	defer channel.Close()
	go ssh.DiscardRequests(requests)
	copied := make(chan struct{})
	go func() { _, _ = io.Copy(target, channel); _ = target.Close(); close(copied) }()
	_, _ = io.Copy(channel, target)
	_ = channel.Close()
	<-copied
}
func formatPort(port int) string { return strconv.Itoa(port) }
func (fixture *sshFixture) machine(id string, keyID int) domain.Machine {
	address := fixture.listener.Addr().(*net.TCPAddr)
	return domain.Machine{ID: id, Name: id, Host: "127.0.0.1", Port: address.Port, User: "developer", KeyID: keyID, Sandbox: "none", SingleUser: true}
}

func TestSSHTrustJumpHostsStreamingAndCancellation(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	jump := newSSHFixture(t, "developer\n")
	target := newSSHFixture(t, "developer\n")
	jumpMachine := jump.machine("jump", key.ID)
	targetMachine := target.machine("target", key.ID)
	targetMachine.Jumps = []string{"jump"}
	for _, machine := range []domain.Machine{jumpMachine, targetMachine} {
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	connector := &Connector{Store: store}
	if _, err := connector.Dial(context.Background(), "target"); err == nil {
		t.Fatal("untrusted host key accepted")
	} else {
		var hostError *HostKeyError
		if !errors.As(err, &hostError) || !strings.Contains(hostError.PublicKey, "ssh-ed25519") {
			t.Fatalf("host identity challenge: %v", err)
		}
	}
	for _, fixture := range []*sshFixture{jump, target} {
		if err := store.TrustSSHKey(fixture.hostKey.Marshal()); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := connector.Dial(context.Background(), "target")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	updates := []string{}
	output, err := RunStreaming(context.Background(), connection.Client, "stream", func(chunk string) { updates = append(updates, chunk) })
	if err != nil || !strings.Contains(output, "second chunk") || len(updates) < 2 {
		t.Fatalf("streamed remote output: %q updates=%d error=%v", output, len(updates), err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, connection.Client, "wait"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancel remote command: %v", err)
	}
}

func TestSingleUserFailoverSkipsOccupiedMachine(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	busy := newSSHFixture(t, "developer\nother-user\n")
	available := newSSHFixture(t, "developer\n")
	for index, fixture := range []*sshFixture{busy, available} {
		machine := fixture.machine([]string{"busy", "available"}[index], key.ID)
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
		if err := store.TrustSSHKey(fixture.hostKey.Marshal()); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(&Connector{Store: store})
	project := domain.Project{ID: "project", Directory: "/srv/project", MachineIDs: []string{"busy", "available"}}
	connection, machine, err := manager.Connect(context.Background(), project, "agent", "busy", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if machine.ID != "available" {
		t.Fatalf("occupied machine was selected: %s", machine.ID)
	}
}
