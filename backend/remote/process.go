package remote

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Process is a bidirectional SSH channel, used for remote protocol servers.
// Closing it also terminates its sandbox invocation. No local process is started.
type Process struct {
	reader    *io.PipeReader
	writer    *io.PipeWriter
	session   *ssh.Session
	stderr    limitedOutput
	closeOnce sync.Once
	cleanupMu sync.Mutex
	closed    bool
	cleanup   []func()
	done      chan struct{}
	exitError error // Read only after done is closed.
}

func StartProcess(ctx context.Context, client *ssh.Client, command string) (*Process, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	var stdin io.WriteCloser
	if err == nil {
		stdin, err = session.StdinPipe()
	}
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	reader, output := io.Pipe()
	input, writer := io.Pipe()
	process := &Process{session: session, reader: reader, writer: writer, done: make(chan struct{})}
	stopCancellation := context.AfterFunc(ctx, func() { _ = process.Close() })
	process.OnClose(func() { stopCancellation() })
	session.Stderr = &process.stderr
	err = session.Start(shellCommand(command))
	if err != nil {
		_ = process.Close()
		return nil, err
	}
	// Local pipes let cancellation unblock SDK reads/writes even if the SSH
	// peer ignores channel closure. SSH pumps exit when the transport recovers
	// or its retained connection is released.
	go func() {
		_, err := io.Copy(output, stdout)
		_ = output.CloseWithError(err)
	}()
	go func() {
		_, err := io.Copy(stdin, input)
		_ = input.CloseWithError(err)
		_ = stdin.Close()
	}()
	go func() {
		process.exitError = session.Wait()
		close(process.done)
	}()
	return process, nil
}

func (process *Process) Read(buffer []byte) (int, error) {
	count, err := process.reader.Read(buffer)
	if err != nil {
		// stdout EOF can overtake the separate stderr copier. Wait briefly for
		// SSH's exit receipt before collecting startup diagnostics.
		if err == io.EOF {
			select {
			case <-process.done:
				if process.exitError != nil {
					err = fmt.Errorf("%v: %w", process.exitError, err)
				}
			case <-time.After(50 * time.Millisecond):
			}
		}
		if diagnostic := strings.TrimSpace(process.stderr.String()); diagnostic != "" {
			if len(diagnostic) > 4000 {
				diagnostic = strings.ToValidUTF8(diagnostic[len(diagnostic)-4000:], "�")
			}
			err = fmt.Errorf("remote protocol stream closed: %s: %w", diagnostic, err)
		}
	}
	return count, err
}

func (process *Process) Write(buffer []byte) (int, error) { return process.writer.Write(buffer) }

func (process *Process) CloseWrite() error { return process.writer.Close() }

// OnClose attaches bookkeeping to the channel, including channels that closed
// during startup. Callbacks must not call Close on this process recursively.
func (process *Process) OnClose(cleanup func()) {
	process.cleanupMu.Lock()
	if !process.closed {
		process.cleanup = append(process.cleanup, cleanup)
		process.cleanupMu.Unlock()
		return
	}
	process.cleanupMu.Unlock()
	cleanup()
}

func (process *Process) Close() error {
	process.closeOnce.Do(func() {
		process.cleanupMu.Lock()
		process.closed = true
		callbacks := process.cleanup
		process.cleanup = nil
		process.cleanupMu.Unlock()
		_ = process.reader.CloseWithError(context.Canceled)
		_ = process.writer.CloseWithError(context.Canceled)
		// Network shutdown must not delay local cancellation during an outage.
		go func() {
			_ = process.session.Signal(ssh.SIGKILL)
			_ = process.session.Close()
		}()
		for _, cleanup := range callbacks {
			cleanup()
		}
	})
	return nil
}
