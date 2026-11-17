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

const maximumOutputBytes = 1024 * 1024

// CommandError preserves SSH's exit status and the remote diagnostic text.
// Callers that only return the error must not silently discard stderr.
type CommandError struct {
	Cause  error
	Output string
}

func (failure *CommandError) Error() string {
	if failure.Output == "" {
		return failure.Cause.Error()
	}

	return fmt.Sprintf("%v\n%s", failure.Cause, failure.Output)
}

func (failure *CommandError) Unwrap() error {
	return failure.Cause
}

func commandFailure(cause error, output string) error {
	diagnostic := strings.TrimSpace(output)
	if len(diagnostic) > 8192 {
		diagnostic = "[last output]\n" + strings.ToValidUTF8(diagnostic[len(diagnostic)-8192:], "�")
	}
	return &CommandError{Cause: cause, Output: diagnostic}
}

// shellCommand passes the command through the SSH user's shell to /bin/sh.
// Fish interprets backslashes even inside single quotes. Emit each backslash
// in its own double-quoted segment, which fish and POSIX shells read identically.
// No login profile is sourced; the SSH session's existing PATH is preserved.
func shellCommand(command string) string {
	argument := strings.ReplaceAll(Quote(command), `\`, `'"\\"'`)
	return "exec /bin/sh -c " + argument
}

func Run(ctx context.Context, connection *Connection, command string) (string, error) {
	return runCommand(ctx, connection, command, nil, nil)
}

func RunStreaming(
	ctx context.Context,
	connection *Connection,
	command string,
	onOutput func(string),
) (string, error) {
	return runCommand(ctx, connection, command, nil, onOutput)
}

func RunPipe(ctx context.Context, connection *Connection, command string, input io.Reader) (string, error) {
	return runCommand(ctx, connection, command, input, nil)
}

func RunStreamingPipe(
	ctx context.Context,
	connection *Connection,
	command string,
	input io.Reader,
	onOutput func(string),
) (string, error) {
	return runCommand(ctx, connection, command, input, onOutput)
}

func runCommand(
	ctx context.Context,
	connection *Connection,
	command string,
	input io.Reader,
	onOutput func(string),
) (string, error) {
	return RunStreamingInput(ctx, connection, command, input, false, onOutput, nil)
}

// Interactive stdin must not hold session.Wait open after the process exits.
// onChunk receives ordered raw bytes before the formatted display snapshot.
func RunStreamingInput(
	ctx context.Context,
	connection *Connection,
	command string,
	input io.Reader,
	interactive bool,
	onOutput func(string),
	onChunk func([]byte),
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if connection.daemon != nil {
		return runDaemonCommand(ctx, connection, command, input, interactive, onOutput, onChunk)
	}
	client := connection.Client
	session, err := commandSession(ctx, client)
	if err != nil {
		return "", err
	}
	defer func() {
		if ctx.Err() != nil {
			interruptCommandSession(session)
		} else {
			_ = session.Close()
		}
	}()

	output := &limitedOutput{onOutput: onOutput}
	capture := &commandCapture{output: output, onChunk: onChunk}

	var stdin io.WriteCloser
	if input != nil {
		stdin, err = session.StdinPipe()
		if err != nil {
			return "", err
		}
	}

	session.Stdout = capture
	session.Stderr = capture

	started := make(chan error, 1)
	if err := ctx.Err(); err != nil {
		return output.String(), err
	}
	go func() { started <- session.Start(shellCommand(command)) }()
	select {
	case err := <-started:
		if err != nil {
			return output.String(), err
		}
	case <-ctx.Done():
		// Cancellation alone is not evidence of a dead transport. A late
		// acknowledgement is given a grace period before declaring a stall.
		go func() {
			select {
			case <-started:
			case <-time.After(time.Second):
				select {
				case <-started:
				default:
					_ = client.Close()
				}
			}
		}()
		return output.String(), ctx.Err()
	}
	if stdin != nil {
		go func() {
			_, _ = io.Copy(stdin, input)
			_ = stdin.Close()
		}()
	}

	completed := make(chan error, 1)
	go func() { completed <- session.Wait() }()

	select {
	case err := <-completed:
		text := output.String()

		if err != nil {
			err = commandFailure(err, text)
		}

		return text, err
	case <-ctx.Done():
		return output.String(), ctx.Err()
	}
}

func commandSession(ctx context.Context, client *ssh.Client) (*ssh.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type result struct {
		session *ssh.Session
		err     error
	}
	ready := make(chan result, 1)
	go func() {
		session, err := client.NewSession()
		ready <- result{session, err}
	}()
	select {
	case opened := <-ready:
		if err := ctx.Err(); err != nil {
			if opened.session != nil {
				interruptCommandSession(opened.session)
			}
			return nil, err
		}
		return opened.session, opened.err
	case <-ctx.Done():
		go func() {
			closeLate := func(opened result) {
				if opened.session != nil {
					interruptCommandSession(opened.session)
				}
			}
			select {
			case opened := <-ready:
				closeLate(opened)
			case <-time.After(time.Second):
				select {
				case opened := <-ready:
					closeLate(opened)
				default:
					// The pending open still has not responded: confirmed setup
					// stall. Closing this transport unblocks NewSession itself.
					_ = client.Close()
				}
			}
		}()
		return nil, ctx.Err()
	}
}

func interruptCommandSession(session *ssh.Session) {
	go func() {
		fallback := time.AfterFunc(time.Second, func() { _ = session.Close() })
		_ = session.Signal(ssh.SIGKILL)
		fallback.Stop()
		_ = session.Close()
	}()
}

type commandCapture struct {
	mutex   sync.Mutex
	output  *limitedOutput
	onChunk func([]byte)
}

func (capture *commandCapture) Write(chunk []byte) (int, error) {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()

	if capture.onChunk != nil {
		capture.onChunk(chunk)
	}

	return capture.output.Write(chunk)
}
