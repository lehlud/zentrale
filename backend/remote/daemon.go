package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed scripts/daemon.py
var daemonProgram string

//go:embed scripts/daemon_bridge.py
var daemonBridge string

type DaemonError struct{ Message string }

func (e *DaemonError) Error() string { return "remote daemon: " + e.Message }

type daemonReply struct {
	ID     uint64          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

type daemonWire struct {
	connection *Connection
	session    *ssh.Session
	input      io.WriteCloser
	output     io.Reader
	generation string
	writeMu    sync.Mutex
	mu         sync.Mutex
	pending    map[uint64]chan daemonReply
	closed     chan struct{}
	once       sync.Once
}

func (wire *daemonWire) close() {
	wire.once.Do(func() {
		close(wire.closed)
		_ = wire.connection.Client.Close()
		wire.connection.Close()
	})
}

func readDaemonFrame(reader io.Reader, value any) error {
	var size uint32
	if err := binary.Read(reader, binary.BigEndian, &size); err != nil {
		return err
	}
	if size > 1024*1024 {
		return errors.New("remote daemon frame exceeds limit")
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(reader, frame); err != nil {
		return err
	}
	return json.Unmarshal(frame, value)
}

func writeDaemonFrame(writer io.Writer, value any) error {
	frame, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(frame) > 1024*1024 {
		return errors.New("remote daemon frame exceeds limit")
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(frame))); err != nil {
		return err
	}
	_, err = writer.Write(frame)
	return err
}

func (wire *daemonWire) read() {
	defer wire.close()
	for {
		var reply daemonReply
		if err := readDaemonFrame(wire.output, &reply); err != nil {
			return
		}
		wire.mu.Lock()
		channel := wire.pending[reply.ID]
		delete(wire.pending, reply.ID)
		wire.mu.Unlock()
		if channel != nil {
			channel <- reply
		}
	}
}

type daemonClient struct {
	users                            atomic.Int64
	configuration                    string
	connector                        *Connector
	machineID, installationID, token string
	gate                             chan struct{}
	mu                               sync.Mutex
	wire                             *daemonWire
	sequence                         atomic.Uint64
	ctx                              context.Context
	cancel                           context.CancelFunc
}

func (client *daemonClient) connect(ctx context.Context) (*daemonWire, error) {
	client.mu.Lock()
	wire := client.wire
	client.mu.Unlock()
	if wire != nil {
		select {
		case <-wire.closed:
		default:
			return wire, nil
		}
	}
	configuration, err := client.connector.routeIdentity(client.machineID)
	if err != nil {
		return nil, &DaemonError{Message: "original SSH configuration is unavailable"}
	}
	if configuration != client.configuration {
		return nil, &DaemonError{Message: "SSH configuration changed; an existing process was not migrated or replayed"}
	}
	select {
	case client.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-client.ctx.Done():
		return nil, client.ctx.Err()
	}
	defer func() { <-client.gate }()
	client.mu.Lock()
	wire = client.wire
	client.mu.Unlock()
	if wire != nil {
		select {
		case <-wire.closed:
		default:
			return wire, nil
		}
	}
	connection, err := client.connector.dialNative(ctx, client.machineID)
	if err != nil {
		return nil, err
	}
	session, err := commandSession(ctx, connection.Client)
	if err != nil {
		connection.Close()
		return nil, err
	}
	input, err := session.StdinPipe()
	if err != nil {
		connection.Close()
		return nil, err
	}
	output, err := session.StdoutPipe()
	if err != nil {
		connection.Close()
		return nil, err
	}
	wire = &daemonWire{connection: connection, session: session, input: input, output: output, pending: make(map[uint64]chan daemonReply), closed: make(chan struct{})}
	stop := context.AfterFunc(ctx, wire.close)
	defer stop()
	program := daemonBridge + "\nbridge(" + strconv.Quote(daemonProgram) + "," + strconv.Quote(client.installationID) + ")\n"
	command := "python3 -u -c " + Quote(program)
	if err := session.Start(shellCommand(command)); err != nil {
		wire.close()
		return nil, err
	}
	credentials, _ := json.Marshal(map[string]string{"token": client.token})
	if _, err := input.Write(append(credentials, '\n')); err != nil {
		wire.close()
		return nil, err
	}
	if err := writeDaemonFrame(input, map[string]any{"id": 0, "op": "hello", "token": client.token, "version": 1}); err != nil {
		wire.close()
		return nil, err
	}
	var reply daemonReply
	if err := readDaemonFrame(output, &reply); err != nil {
		wire.close()
		return nil, fmt.Errorf("attach remote daemon: %w", err)
	}
	var hello struct {
		Version    int    `json:"version"`
		Generation string `json:"generation"`
	}
	if reply.Error != "" {
		wire.close()
		return nil, &DaemonError{reply.Error}
	}
	if err := json.Unmarshal(reply.Result, &hello); err != nil || hello.Version != 1 || hello.Generation == "" {
		wire.close()
		return nil, errors.New("incompatible remote daemon")
	}
	wire.generation = hello.Generation
	client.mu.Lock()
	client.wire = wire
	client.mu.Unlock()
	go wire.read()
	return wire, nil
}

func (client *daemonClient) call(ctx context.Context, request map[string]any, result any) error {
	backoff := 100 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := client.ctx.Err(); err != nil {
			return err
		}
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		wire, err := client.connect(attempt)
		cancel()
		if err == nil {
			if _, exists := request["generation"]; !exists {
				request["generation"] = wire.generation
			}
			identity := client.sequence.Add(1)
			request["id"] = identity
			channel := make(chan daemonReply, 1)
			wire.mu.Lock()
			wire.pending[identity] = channel
			wire.mu.Unlock()
			// Each frame is bounded; a stalled transport is closed to release the
			// writer. This never closes a remote process or upstream socket.
			writeDone := make(chan error, 1)
			var frame bytes.Buffer
			if err := writeDaemonFrame(&frame, request); err != nil {
				return err
			}
			go func() {
				wire.writeMu.Lock()
				defer wire.writeMu.Unlock()
				_, err := io.Copy(wire.input, &frame)
				writeDone <- err
			}()
			select {
			case err = <-writeDone:
			case <-ctx.Done():
				err = ctx.Err()
				wire.close()
			case <-client.ctx.Done():
				err = client.ctx.Err()
				wire.close()
			case <-time.After(25 * time.Second):
				err = errors.New("daemon transport write stalled")
				wire.close()
			}
			if err == nil {
				select {
				case reply := <-channel:
					if reply.Error != "" {
						return &DaemonError{reply.Error}
					}
					if result != nil {
						return json.Unmarshal(reply.Result, result)
					}
					return nil
				case <-wire.closed:
				case <-ctx.Done():
					wire.mu.Lock()
					delete(wire.pending, identity)
					wire.mu.Unlock()
					return ctx.Err()
				case <-client.ctx.Done():
					return client.ctx.Err()
				case <-time.After(30 * time.Second):
					wire.close()
				}
			} else {
				wire.close()
			}
			wire.mu.Lock()
			delete(wire.pending, identity)
			wire.mu.Unlock()
		}
		var trust *HostKeyError
		var protocol *DaemonError
		if errors.As(err, &trust) || errors.As(err, &protocol) {
			return err
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		case <-client.ctx.Done():
			return client.ctx.Err()
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func (client *daemonClient) heartbeat() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-client.ctx.Done():
			return
		case <-ticker.C:
		}
		if client.users.Load() == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(client.ctx, 25*time.Second)
		_ = client.call(ctx, map[string]any{"op": "ping"}, nil)
		cancel()
	}
}

func (client *daemonClient) release() {
	if client.users.Add(-1) != 0 {
		return
	}
	client.mu.Lock()
	if client.users.Load() == 0 && client.wire != nil {
		client.wire.close()
	}
	client.mu.Unlock()
}

func daemonResourceID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value)
}

type daemonState struct {
	State       string `json:"state"`
	Code        *int   `json:"code"`
	Error       string `json:"error"`
	Data        string `json:"data"`
	Offset      int64  `json:"offset"`
	EOF         bool   `json:"eof"`
	InputOffset int64  `json:"inputOffset"`
	InputEOF    bool   `json:"inputEOF"`
}

type daemonResource struct {
	client         *daemonClient
	id, generation string
	writeMu        sync.Mutex
	inputOffset    int64
	inputEOF       bool
	beforeInput    func(int64, []byte, bool) error
	afterInput     func() error
}

func (resource *daemonResource) call(ctx context.Context, op string, values map[string]any, result any) error {
	if values == nil {
		values = map[string]any{}
	}
	values["op"], values["resource"], values["generation"] = op, resource.id, resource.generation
	return resource.client.call(ctx, values, result)
}

func (resource *daemonResource) write(ctx context.Context, data []byte, eof bool) (int, error) {
	resource.writeMu.Lock()
	defer resource.writeMu.Unlock()
	if resource.inputEOF {
		return 0, io.ErrClosedPipe
	}
	count := 0
	for len(data) > 0 || eof {
		size := min(len(data), 32768)
		last := size == len(data)
		if resource.beforeInput != nil {
			if err := resource.beforeInput(resource.inputOffset, data[:size], eof && last); err != nil {
				return count, err
			}
		}
		var receipt daemonState
		err := resource.call(ctx, "write", map[string]any{"offset": resource.inputOffset, "data": base64.StdEncoding.EncodeToString(data[:size]), "eof": eof && last}, &receipt)
		if err != nil {
			return count, err
		}
		resource.inputOffset, resource.inputEOF = receipt.InputOffset, receipt.InputEOF
		if resource.afterInput != nil {
			if err := resource.afterInput(); err != nil {
				return count, err
			}
		}
		count += size
		data = data[size:]
		if last {
			break
		}
	}
	return count, nil
}

func (resource *daemonResource) read(ctx context.Context, stream string, offset int64) ([]byte, daemonState, error) {
	var state daemonState
	err := resource.call(ctx, "read", map[string]any{"stream": stream, "offset": offset}, &state)
	if err != nil {
		return nil, state, err
	}
	data, err := base64.StdEncoding.DecodeString(state.Data)
	return data, state, err
}

func (resource *daemonResource) stop() {
	ctx, cancel := context.WithTimeout(resource.client.ctx, 10*time.Second)
	defer cancel()
	_ = resource.call(ctx, "stop", nil, nil)
}

// DaemonExitError is a completed remote command, not a lost connection.
type DaemonExitError struct {
	Code       int
	Diagnostic string
}

func (e *DaemonExitError) Error() string {
	return fmt.Sprintf("remote command exited with status %d: %s", e.Code, e.Diagnostic)
}
func (e *DaemonExitError) ExitStatus() int { return e.Code }
