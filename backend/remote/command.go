package remote

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

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

// shellCommand passes the command through the SSH user's shell to /bin/sh.
// Fish interprets backslashes even inside single quotes. Emit each backslash
// in its own double-quoted segment, which fish and POSIX shells read identically.
// No login profile is sourced; the SSH session's existing PATH is preserved.
func shellCommand(command string) string {
	argument := strings.ReplaceAll(Quote(command), `\`, `'"\\"'`)
	return "exec /bin/sh -c " + argument
}

func Run(ctx context.Context, client *ssh.Client, command string) (string, error) {
	return runCommand(ctx, client, command, nil, nil)
}

func RunStreaming(
	ctx context.Context,
	client *ssh.Client,
	command string,
	onOutput func(string),
) (string, error) {
	return runCommand(ctx, client, command, nil, onOutput)
}

func RunPipe(ctx context.Context, client *ssh.Client, command string, input io.Reader) (string, error) {
	return runCommand(ctx, client, command, input, nil)
}

func RunStreamingPipe(
	ctx context.Context,
	client *ssh.Client,
	command string,
	input io.Reader,
	onOutput func(string),
) (string, error) {
	return runCommand(ctx, client, command, input, onOutput)
}

func runCommand(
	ctx context.Context,
	client *ssh.Client,
	command string,
	input io.Reader,
	onOutput func(string),
) (string, error) {
	return RunStreamingInput(ctx, client, command, input, false, onOutput, nil)
}

// Interactive stdin must not hold session.Wait open after the process exits.
// onChunk receives ordered raw bytes before the formatted display snapshot.
func RunStreamingInput(
	ctx context.Context,
	client *ssh.Client,
	command string,
	input io.Reader,
	interactive bool,
	onOutput func(string),
	onChunk func([]byte),
) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	output := &limitedOutput{onOutput: onOutput}
	capture := &commandCapture{output: output, onChunk: onChunk}

	if interactive {
		stdin, err := session.StdinPipe()
		if err != nil {
			return "", err
		}

		go func() {
			_, _ = io.Copy(stdin, input)
			_ = stdin.Close()
		}()
	} else {
		session.Stdin = input
	}

	session.Stdout = capture
	session.Stderr = capture

	if err := session.Start(shellCommand(command)); err != nil {
		return "", err
	}

	completed := make(chan error, 1)
	go func() { completed <- session.Wait() }()

	select {
	case err := <-completed:
		text := output.String()

		if err != nil {
			diagnostic := strings.TrimSpace(text)
			if len(diagnostic) > 8192 {
				diagnostic = "[last output]\n" + strings.ToValidUTF8(diagnostic[len(diagnostic)-8192:], "�")
			}

			err = &CommandError{Cause: err, Output: diagnostic}
		}

		return text, err
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		_ = session.Close()
		<-completed

		return output.String(), ctx.Err()
	}
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
