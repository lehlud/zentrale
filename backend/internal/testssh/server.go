// Package testssh provides a loopback-only SSH peer for integration tests.
// Its default handler returns fixed output; tests may provide a command handler.
package testssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

type Server struct {
	rejectAuthentication atomic.Bool

	Host      string `json:"host"`
	Port      int    `json:"port"`
	PublicKey string `json:"publicKey"`

	listener net.Listener
	mutex    sync.Mutex
	sockets  []net.Conn
	workers  sync.WaitGroup
	closing  bool

	commandHandler      CommandHandler
	allowUnixForwarding bool
}

type CommandHandler func(command string, stdin io.Reader, stdout, stderr io.Writer) int

func Start() (*Server, error) {
	return StartWithCommandHandler(nil)
}

func StartWithCommandHandler(handler CommandHandler) (*Server, error) {
	return start(handler, false)
}

// StartEditorPeer enables Unix-socket forwarding for remote-agent tests.
func StartEditorPeer(handler CommandHandler) (*Server, error) {
	return start(handler, true)
}

func start(handler CommandHandler, allowUnixForwarding bool) (*Server, error) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	server := &Server{
		Host:      "127.0.0.1",
		Port:      listener.Addr().(*net.TCPAddr).Port,
		PublicKey: strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))),
		listener:  listener,
	}

	server.commandHandler = handler
	server.allowUnixForwarding = allowUnixForwarding
	configuration := &ssh.ServerConfig{
		PublicKeyCallback: func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
			if server.rejectAuthentication.Load() {
				return nil, errors.New("test authentication refused")
			}

			return &ssh.Permissions{}, nil
		},
	}

	configuration.AddHostKey(signer)
	server.workers.Go(func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}

			server.mutex.Lock()

			if server.closing {
				server.mutex.Unlock()
				_ = socket.Close()
				continue
			}

			server.sockets = append(server.sockets, socket)
			server.mutex.Unlock()
			server.workers.Go(func() { server.serve(socket, configuration) })
		}
	})

	return server, nil
}

func (server *Server) Close() {
	_ = server.listener.Close()
	server.mutex.Lock()
	server.closing = true

	for _, socket := range server.sockets {
		_ = socket.Close()
	}

	server.mutex.Unlock()
	server.workers.Wait()
}

func (server *Server) serve(socket net.Conn, configuration *ssh.ServerConfig) {
	connection, channels, requests, err := ssh.NewServerConn(socket, configuration)
	if err != nil {
		_ = socket.Close()
		return
	}
	defer connection.Close()

	go ssh.DiscardRequests(requests)

	for incoming := range channels {
		if incoming.ChannelType() == "direct-streamlocal@openssh.com" && server.allowUnixForwarding {
			server.workers.Go(func() { forwardUnixSocket(incoming) })
			continue
		}

		if incoming.ChannelType() != "session" {
			_ = incoming.Reject(ssh.UnknownChannelType, "test peer supports sessions only")
			continue
		}

		channel, requests, err := incoming.Accept()
		if err != nil {
			continue
		}

		server.workers.Go(func() {
			defer channel.Close()

			for request := range requests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}

				_ = request.Reply(true, nil)
				var payload struct{ Command string }
				_ = ssh.Unmarshal(request.Payload, &payload)
				status := 0

				if server.commandHandler != nil {
					status = server.commandHandler(payload.Command, channel, channel, channel.Stderr())
				} else if strings.Contains(payload.Command, "Prepare a project directory inside configured persistent storage") {
					// The browser peer simulates protocols; execution and persistence
					// are tested separately against real shell-backed SSH peers.
					var configuration struct {
						ProjectID string `json:"projectId"`
						Root      string `json:"root"`
						Directory string `json:"directory"`
					}
					if err := json.NewDecoder(channel).Decode(&configuration); err != nil {
						status = 1
					} else {
						directory := configuration.Directory
						if directory == "" {
							root := strings.Replace(configuration.Root, "~/", "/home/test/", 1)
							directory = path.Join(root, "project-"+configuration.ProjectID)
						}

						_ = json.NewEncoder(channel).Encode(map[string]string{"directory": directory})
					}
				} else if strings.Contains(payload.Command, "test -c /dev/kvm") {
					// This fixture has no isolation runtime; return the probe protocol's
					// explicit fallback instead of arbitrary command output.
					_, _ = io.WriteString(channel, "none\n")
				} else {
					_, _ = io.WriteString(channel, "executed by test peer")
				}

				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{
					uint32(status),
				}))

				return
			}
		})
	}
}

func (server *Server) RejectAuthentication(reject bool) {
	server.rejectAuthentication.Store(reject)
}

func forwardUnixSocket(request ssh.NewChannel) {
	var destination struct {
		SocketPath, Reserved string
		ReservedPort         uint32
	}
	if err := ssh.Unmarshal(request.ExtraData(), &destination); err != nil {
		_ = request.Reject(ssh.ConnectionFailed, "invalid socket path")
		return
	}

	target, err := net.Dial("unix", destination.SocketPath)
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
	completed := make(chan struct{})
	go func() {
		_, _ = io.Copy(target, channel)
		_ = target.Close()
		close(completed)
	}()
	_, _ = io.Copy(channel, target)
	_ = channel.Close()
	<-completed
}
