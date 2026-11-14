package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestTerminalPreparationSharesOwnerAndHoldsBusyUntilDisconnect(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, main, project, _ := managedWorkspaceEngine(t, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "ZENTRALE_EDITOR_READY") {
			t.Error("terminal installed VS Code")
		}
		return shell(command, input, stdout, stderr)
	})
	child, err := engine.Create(project.ID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := engine.withEditorEnvironment(ctx, project.ID, child.ID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, resolved domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
			if target.AgentID != main.ID || resolved.Directory == "" || target.TemporaryDirectory != agentTemporaryDirectory(main.ID) {
				t.Errorf("wrong environment: %+v %+v", resolved, target)
			}
			close(entered)
			<-ctx.Done()
			return domain.EditorSession{}, ctx.Err()
		})
		done <- err
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("preparation stalled")
	}
	if !engine.environmentBusy(map[string]bool{main.ID: true}, main.MachineID) {
		t.Fatal("open terminal not busy")
	}
	engine.mu.Lock()
	if engine.editorPreparing[project.ID] != 1 || engine.preparing[main.ID] != 1 {
		t.Error("missing lifecycle holds")
	}
	engine.mu.Unlock()
	if _, err := engine.manageVM(context.Background(), nil, domain.Machine{ID: main.MachineID}, "reset", remote.Execution{AgentID: main.ID}); err == nil {
		t.Error("reset accepted during terminal")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect leaked preparation")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.editorPreparing[project.ID] != 0 || engine.preparing[main.ID] != 0 {
		t.Error("activity leaked")
	}
}

func TestTerminalRealPTYDisconnectReleasesActivity(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	engine, main, project, _ := managedWorkspaceEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- engine.Terminal(ctx, project.ID, main.ID, 80, 24, func(ctx context.Context, terminal *remote.Terminal) error {
			if err := terminal.Input([]byte("printf 'terminal-session-ok\\n'\n")); err != nil {
				return err
			}
			buffer := make([]byte, 4096)
			var output strings.Builder
			for !strings.Contains(output.String(), "terminal-session-ok\r\n") {
				count, err := terminal.Read(buffer)
				output.Write(buffer[:count])
				if err != nil {
					return err
				}
			}
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PTY disconnect leaked")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.preparing[main.ID] != 0 || engine.editorPreparing[project.ID] != 0 {
		t.Fatal("terminal activity leaked")
	}
}

func TestTerminalSurvivesAgentConnectionRelease(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	engine, main, project, _ := managedWorkspaceEngine(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entered := make(chan struct{})
	released := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- engine.Terminal(ctx, project.ID, main.ID, 80, 24, func(ctx context.Context, terminal *remote.Terminal) error {
			close(entered)
			select {
			case <-released:
			case <-ctx.Done():
				return ctx.Err()
			}
			// Match the output, not the PTY's echo of the command itself.
			if err := terminal.Input([]byte("printf 'retained-%s\\n' 'pty-ok'\n")); err != nil {
				return err
			}
			var output strings.Builder
			buffer := make([]byte, 4096)
			for !strings.Contains(output.String(), "retained-pty-ok\r\n") {
				count, err := terminal.Read(buffer)
				output.Write(buffer[:count])
				if err != nil {
					return fmt.Errorf("read PTY output %q: %w", output.String(), err)
				}
			}
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("terminal failed before opening: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	entry := engine.connectionEntry(main.ID)
	entry.mutex.Lock()
	cached := entry.connection != nil
	entry.mutex.Unlock()
	if !cached {
		t.Fatal("test requires an agent-cached connection")
	}
	// Agent turns and cache expiry must not own the open terminal's lifetime.
	engine.releaseConnection(main.ID)
	if !engine.environmentBusy(map[string]bool{main.ID: true}, main.MachineID) {
		t.Error("cache release dropped the terminal's environment activity")
	}
	close(released)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cache release interrupted the terminal: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("terminal stopped responding after agent connection release")
	}
}
