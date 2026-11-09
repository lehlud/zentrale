package remote

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed scripts/editor.py
var editorSupervisor string

func runEditorProcess(ctx context.Context, client *ssh.Client, command string, onOutput func(string)) error {
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	defer stdin.Close()

	output := &limitedOutput{onOutput: onOutput}
	session.Stdout, session.Stderr = output, output

	if err := session.Start(shellCommand(command)); err != nil {
		return err
	}

	completed := make(chan error, 1)
	go func() { completed <- session.Wait() }()

	select {
	case err := <-completed:
		details := output.String()
		if len(details) > 4000 {
			details = details[len(details)-4000:]
		}

		return fmt.Errorf("remote VS Code stopped: %v\n%s", err, details)
	case <-ctx.Done():
		// Graceful shutdown lets the remote supervisor clean up the server's entire
		// process group. Generic harness commands deliberately use a harder stop.
		_ = stdin.Close()
		_ = session.Signal(ssh.SIGTERM)

		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			_ = session.Signal(ssh.SIGKILL)
			_ = session.Close()
			<-completed
		}

		return ctx.Err()
	}
}
