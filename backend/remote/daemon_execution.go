package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Invocation is a durable attachment owned by the caller, not an SSH channel.
// Persist must commit the generation before the daemon admits a side effect.
// Recovery uses AttachOnly, which never creates or replays a missing command.
type Invocation struct {
	ID, Generation string
	AttachOnly     bool
	Timeout        time.Duration
	Persist        func(string) error
	DetachOnCancel func() bool
	BeforeInput    func(int64, []byte, bool) error
	AfterInput     func() error
	ReplayInput    *PendingInput
	InputReady     func(func(context.Context, []byte, bool) error)
	OutputOffset   int64
	IdleTimeout    time.Duration
}
type PendingInput struct {
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
	EOF    bool   `json:"eof"`
}
type invocationKey struct{}

func WithInvocation(ctx context.Context, invocation Invocation) context.Context {
	return context.WithValue(ctx, invocationKey{}, invocation)
}

func (connection *Connection) DaemonEnabled() bool {
	return connection != nil && connection.daemon != nil
}

// StopInvocation confirms the original resource's stop; it never creates work.
func StopInvocation(ctx context.Context, connection *Connection, id, generation string) error {
	if connection.daemon == nil {
		return errors.New("durable stop requires a daemon connection")
	}
	resource := &daemonResource{client: connection.daemon, id: id, generation: generation}
	err := resource.call(ctx, "stop", nil, nil)
	var failure *DaemonError
	if errors.As(err, &failure) && strings.Contains(failure.Message, "resource is unknown") {
		return nil
	}
	return err
}

func newDaemonResource(ctx context.Context, connection *Connection, values map[string]any) (*daemonResource, error) {
	client := connection.daemon
	invocation, _ := ctx.Value(invocationKey{}).(Invocation)
	identity := invocation.ID
	if identity == "" {
		identity = daemonResourceID()
	}
	wire, err := client.connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := client.connector.VerifyTrust(wire.connection); err != nil {
		return nil, err
	}
	generation := invocation.Generation
	if generation == "" {
		generation = wire.generation
	}
	resource := &daemonResource{client: client, id: identity, generation: generation}
	if invocation.Persist != nil {
		if err := invocation.Persist(generation); err != nil {
			return nil, err
		}
	}
	var state daemonState
	if invocation.AttachOnly {
		err = resource.call(ctx, "status", nil, &state)
	} else {
		if invocation.IdleTimeout > 0 {
			values["idleTimeout"] = invocation.IdleTimeout.Seconds()
		}
		if invocation.Timeout > 0 {
			values["timeout"] = invocation.Timeout.Seconds()
		}
		err = resource.call(ctx, "create", values, &state)
	}
	if err != nil {
		if ctx.Err() != nil && (invocation.DetachOnCancel == nil || !invocation.DetachOnCancel()) {
			go resource.stop()
		}
		return nil, err
	}
	if state.State == "failed" {
		return nil, &DaemonError{state.Error}
	}
	resource.inputOffset, resource.inputEOF = state.InputOffset, state.InputEOF
	resource.beforeInput, resource.afterInput = invocation.BeforeInput, invocation.AfterInput
	if pending := invocation.ReplayInput; pending != nil {
		end := pending.Offset + int64(len(pending.Data))
		if resource.inputOffset == pending.Offset && !resource.inputEOF {
			if _, err := resource.write(ctx, pending.Data, pending.EOF); err != nil {
				return nil, err
			}
		} else if resource.inputOffset >= end && (!pending.EOF || resource.inputEOF) {
			if resource.afterInput != nil {
				if err := resource.afterInput(); err != nil {
					return nil, err
				}
			}
		} else {
			return nil, errors.New("remote stdin receipt is inconsistent; input was not replayed")
		}
	}
	if invocation.InputReady != nil {
		invocation.InputReady(func(ctx context.Context, data []byte, eof bool) error {
			_, err := resource.write(ctx, data, eof)
			return err
		})
	}
	return resource, nil
}

func runDaemonCommand(ctx context.Context, connection *Connection, command string, input io.Reader, interactive bool, onOutput func(string), onChunk func([]byte)) (string, error) {
	resource, err := newDaemonResource(ctx, connection, map[string]any{"kind": "process", "command": command, "merge": true})
	if err != nil {
		return "", err
	}
	ioContext, cancelInput := context.WithCancelCause(ctx)
	defer cancelInput(nil)
	invocation, _ := ctx.Value(invocationKey{}).(Invocation)
	defer func() {
		if (ctx.Err() != nil || context.Cause(ioContext) != nil) && (invocation.DetachOnCancel == nil || !invocation.DetachOnCancel()) {
			go resource.stop()
		}
	}()
	if invocation.InputReady == nil && (input != nil || (!interactive && !invocation.AttachOnly)) {
		go func() {
			if input != nil {
				buffer := make([]byte, 32768)
				for {
					count, err := input.Read(buffer)
					if count > 0 {
						if _, e := resource.write(ioContext, buffer[:count], false); e != nil {
							cancelInput(fmt.Errorf("remote stdin: %w", e))
							return
						}
					}
					if err != nil {
						if !errors.Is(err, io.EOF) {
							cancelInput(fmt.Errorf("read command input: %w", err))
							return
						}
						break
					}
				}
			}
			// A command may finish without reading stdin. Closing its already
			// closed input must not turn a successful exit into a race-dependent error.
			_, _ = resource.write(ioContext, nil, true)
		}()
	}
	output := &limitedOutput{onOutput: onOutput}
	var offset int64
	for {
		chunk, state, err := resource.read(ioContext, "stdout", offset)
		if err != nil {
			if cause := context.Cause(ioContext); cause != nil && ctx.Err() == nil {
				return output.String(), cause
			}
			return output.String(), err
		}
		if len(chunk) > 0 {
			if onChunk != nil {
				onChunk(chunk)
			}
			_, _ = output.Write(chunk)
		}
		offset = state.Offset
		if state.EOF {
			if state.Code != nil && *state.Code != 0 {
				return output.String(), commandFailure(&DaemonExitError{*state.Code, state.Error}, output.String())
			}
			if state.Error != "" {
				return output.String(), &DaemonError{state.Error}
			}
			return output.String(), nil
		}
	}
}

func startDaemonProcess(ctx context.Context, connection *Connection, command string) (*Process, error) {
	resource, err := newDaemonResource(ctx, connection, map[string]any{"kind": "process", "command": command, "merge": false})
	if err != nil {
		return nil, err
	}
	reader, output := io.Pipe()
	invocation, _ := ctx.Value(invocationKey{}).(Invocation)
	process := &Process{reader: reader, done: make(chan struct{}), daemon: resource, ioContext: ctx, detach: invocation.DetachOnCancel}
	resource.client.users.Add(1)
	process.OnClose(resource.client.release)
	processContext, cancel := context.WithCancel(ctx)
	process.ioContext = processContext
	process.OnClose(cancel)
	stop := context.AfterFunc(ctx, func() { _ = process.Close() })
	process.OnClose(func() { stop() })
	var streams sync.WaitGroup
	streams.Add(2)
	for _, stream := range []string{"stdout", "stderr"} {
		go func(stream string) {
			defer streams.Done()
			var offset int64
			if stream == "stdout" {
				offset = invocation.OutputOffset
			}
			for {
				chunk, state, err := resource.read(processContext, stream, offset)
				if err != nil {
					if stream == "stdout" {
						_ = output.CloseWithError(err)
					}
					return
				}
				if stream == "stderr" {
					_, _ = process.stderr.Write(chunk)
				} else {
					if _, err := output.Write(chunk); err != nil {
						return
					}
				}
				offset = state.Offset
				if state.EOF {
					if stream == "stdout" {
						if state.Code != nil && *state.Code != 0 {
							process.exitError = &DaemonExitError{*state.Code, state.Error}
						} else if state.Error != "" {
							process.exitError = &DaemonError{state.Error}
						}
						_ = output.Close()
					}
					return
				}
			}
		}(stream)
	}
	go func() { streams.Wait(); close(process.done) }()
	return process, nil
}

type daemonAddress string

func (address daemonAddress) Network() string { return "daemon" }
func (address daemonAddress) String() string  { return string(address) }

type daemonConn struct {
	closeOnce                   sync.Once
	resource                    *daemonResource
	ctx                         context.Context
	cancel                      context.CancelFunc
	readMu                      sync.Mutex
	buffer                      []byte
	offset                      int64
	eof                         bool
	mu                          sync.Mutex
	readDeadline, writeDeadline time.Time
	nextIO                      uint64
	readers, writers            map[uint64]context.CancelCauseFunc
}

func (connection *Connection) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if connection.daemon == nil {
		return connection.Client.DialContext(ctx, network, address)
	}
	values := map[string]any{"kind": "tunnel", "network": network, "address": address}
	if network != "unix" {
		if network != "tcp" && network != "tcp4" && network != "tcp6" {
			return nil, errors.New("unsupported daemon tunnel network")
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		number, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}
		values["host"], values["port"] = host, number
	}
	resource, err := newDaemonResource(ctx, connection, values)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(connection.daemon.ctx)
	connection.daemon.users.Add(1)
	return &daemonConn{resource: resource, ctx: lifetime, cancel: cancel}, nil
}

func (connection *daemonConn) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	connection.readMu.Lock()
	defer connection.readMu.Unlock()
	for len(connection.buffer) == 0 {
		if connection.eof {
			return 0, io.EOF
		}
		ctx, finish := connection.beginIO(false)
		chunk, state, err := connection.resource.read(ctx, "stdout", connection.offset)
		cause := context.Cause(ctx)
		finish()
		if err != nil {
			if errors.Is(cause, errDaemonDeadlineChanged) {
				continue
			}
			return 0, connection.ioError(err, cause)
		}
		connection.buffer, connection.offset, connection.eof = chunk, state.Offset, state.EOF
	}
	count := copy(buffer, connection.buffer)
	connection.buffer = connection.buffer[count:]
	return count, nil
}
func (connection *daemonConn) Write(buffer []byte) (int, error) {
	total := 0
	for len(buffer) > 0 {
		ctx, finish := connection.beginIO(true)
		count, err := connection.resource.write(ctx, buffer, false)
		cause := context.Cause(ctx)
		finish()
		total += count
		buffer = buffer[count:]
		if err != nil {
			if errors.Is(cause, errDaemonDeadlineChanged) {
				continue
			}
			return total, connection.ioError(err, cause)
		}
	}
	return total, nil
}
func (connection *daemonConn) Close() error {
	connection.closeOnce.Do(func() { connection.cancel(); go connection.resource.stop(); connection.resource.client.release() })
	return nil
}
func (connection *daemonConn) LocalAddr() net.Addr { return daemonAddress("zentrale") }
func (connection *daemonConn) RemoteAddr() net.Addr {
	return daemonAddress(fmt.Sprintf("%s/%s", connection.resource.client.machineID, connection.resource.id))
}
func (connection *daemonConn) SetDeadline(deadline time.Time) error {
	return connection.setIODeadline(deadline, true, true)
}
func (connection *daemonConn) SetReadDeadline(deadline time.Time) error {
	return connection.setIODeadline(deadline, true, false)
}
func (connection *daemonConn) SetWriteDeadline(deadline time.Time) error {
	return connection.setIODeadline(deadline, false, true)
}
