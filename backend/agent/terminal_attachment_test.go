package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/remote"
)

func TestDaemonTerminalReattachesSameShellAndAcknowledgedInput(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	engine, record, project := executionTestEngine(t, nil, shellPeerHandler("/bin/sh", os.Environ()))
	engine.Remote.Connector.NativeSessionsForTests = false
	starts := filepath.Join(t.TempDir(), "starts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	firstContext, detach := context.WithCancel(ctx)
	attachment := TerminalAttachment{ID: "terminal-restart-test", KeepOnDisconnect: func() bool { return true }}
	var cursor int64
	var firstPID string
	readMarker := func(terminal *remote.Terminal, marker string) string {
		t.Helper()
		var output strings.Builder
		buffer := make([]byte, 4096)
		pattern := regexp.MustCompile(marker + `([0-9]+)__`)
		for {
			count, err := terminal.Read(buffer)
			cursor += int64(count)
			output.Write(buffer[:count])
			if match := pattern.FindStringSubmatch(output.String()); match != nil {
				return match[1]
			}
			if err != nil {
				t.Fatalf("terminal before marker: %q %v", output.String(), err)
			}
		}
	}
	err := engine.TerminalAttached(firstContext, project.ID, record.ID, attachment, 80, 24, func(_ context.Context, terminal *remote.Terminal) error {
		command := "printf x >> " + remote.Quote(starts) + "; printf '__BEFORE__%s__\\n' $$\n"
		if err := terminal.AcceptInput(1, func() error { return terminal.Input([]byte(command)) }); err != nil {
			return err
		}
		firstPID = readMarker(terminal, "__BEFORE__")
		detach()
		return firstContext.Err()
	})
	if err != context.Canceled {
		t.Fatalf("detach: %v", err)
	}
	engine.Close()
	restarted, err := New(engine.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	attachment.OutputOffset = cursor
	err = restarted.TerminalAttached(ctx, project.ID, record.ID, attachment, 100, 30, func(_ context.Context, terminal *remote.Terminal) error {
		if err := terminal.AcceptInput(1, func() error { return fmt.Errorf("acknowledged input was executed again") }); err != nil {
			return err
		}
		if err := terminal.AcceptInput(2, func() error { return terminal.Input([]byte("printf '__AFTER__%s__\\n' $$\n")) }); err != nil {
			return err
		}
		if actual := readMarker(terminal, "__AFTER__"); actual != firstPID {
			return fmt.Errorf("shell restarted: %s -> %s", firstPID, actual)
		}
		return terminal.Terminate()
	})
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(starts)
	if err != nil || string(value) != "x" {
		t.Fatalf("input replay: %q %v", value, err)
	}
}
