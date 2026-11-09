package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func interactiveEngine(t *testing.T, reviews *atomic.Int32) (*Engine, domain.Agent, domain.Project) {
	t.Helper()
	provider := providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		reviews.Add(1)
		current := request.Messages[len(request.Messages)-1].Content
		if strings.Contains(current, "denied.txt") {
			return providers.Result{Text: `{"allowed":false,"reason":"Input denied","blanket":false}`, UsageKnown: true}, nil
		}
		if strings.Contains(current, `"action":"write_stdin"`) && !strings.Contains(current, `"runningCommand"`) {
			t.Error("input review is missing the real launch command")
		}
		return providers.Result{Text: `{"allowed":true,"reason":"","blanket":false}`, UsageKnown: true}, nil
	})
	engine, record, project := executionTestEngine(t, provider, shellPeerHandler("/bin/sh", os.Environ()))
	machine, _ := engine.Remote.Connector.Machine(project.MachineIDs[0])
	machine.Sandbox = "none"
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	return engine, record, project
}

func inputCall(id, chars string, eof bool) providers.Call {
	return providers.Call{Name: "write_stdin", Arguments: map[string]any{
		"id": id, "chars": chars, "eof": eof, "yield_time_ms": float64(500),
	}}
}

func TestInteractivePipePreservesProcessAndReviewsInput(t *testing.T) {
	var reviews atomic.Int32
	engine, record, _ := interactiveEngine(t, &reviews)
	command := "python3 -u -c " + remote.Quote("import sys\nprint('ready',flush=True)\nfor line in sys.stdin:\n print('reply:'+line.strip(),flush=True)\nprint('closed',flush=True)")
	result, err := engine.executeRemote(context.Background(), record, executionRequest{Command: command, Interactive: true, YieldTime: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["jobId"].(string)
	invalid := inputCall(id, "should-not-arrive\n", false)
	invalid.Arguments["max_output_bytes"] = float64(-1)
	if _, err := engine.dispatch(context.Background(), record, invalid); err == nil || reviews.Load() != 0 {
		t.Fatal("invalid settings were reviewed or sent", err)
	}
	read, err := engine.dispatch(context.Background(), record, inputCall(id, "hello\n", false))
	if err != nil || !strings.Contains(read.(jobOutputResult).Output, "reply:hello") {
		t.Fatal(read, err)
	}
	before := reviews.Load()
	poll, err := engine.dispatch(context.Background(), record, inputCall(id, "", false))
	if err != nil || poll.(jobOutputResult).Output != "" || reviews.Load() != before {
		t.Fatal("poll replayed output or requested approval", poll, err)
	}
	final, err := engine.dispatch(context.Background(), record, inputCall(id, "", true))
	if err != nil || !strings.Contains(final.(jobOutputResult).Output, "closed") {
		t.Fatal(final, err)
	}
	completed, err := engine.jobTool(context.Background(), record, "job_wait", id)
	if err != nil || completed.(jobOutputResult).Status != "completed" {
		t.Fatal(completed, err)
	}
	if reviews.Load() != before+1 {
		t.Fatal("EOF bypassed review")
	}
	if _, err := engine.dispatch(context.Background(), record, inputCall(id, "late\n", false)); err == nil {
		t.Fatal("input delivered after exit")
	}
	other := record
	other.ID = "different-agent"
	if _, err := engine.dispatch(context.Background(), other, inputCall(id, "", false)); err == nil {
		t.Fatal("another agent polled a private process")
	}
}

func TestInteractivePTYHasControllingTerminalAndReviewedControlCharacters(t *testing.T) {
	var reviews atomic.Int32
	engine, record, _ := interactiveEngine(t, &reviews)
	code := "import sys\nprint('tty='+str(sys.stdin.isatty()),flush=True)\nwhile True:\n try:\n  value=input('value> ')\n  if value=='quit': break\n  print('reply:'+value,flush=True)\n except KeyboardInterrupt:\n  print('interrupted',flush=True)\n except EOFError: break\nprint('bye',flush=True)"
	result, err := engine.executeRemote(context.Background(), record, executionRequest{Command: "python3 -u -c " + remote.Quote(code), TTY: true, YieldTime: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["jobId"].(string)
	first, err := engine.dispatch(context.Background(), record, inputCall(id, "hello\n", false))
	if err != nil {
		t.Fatal(err)
	}
	log := first.(jobOutputResult).Output + result.(map[string]any)["output"].(string)
	if !strings.Contains(log, "tty=True") || !strings.Contains(log, "reply:hello") {
		t.Fatal("PTY unavailable", log)
	}
	interrupted, err := engine.dispatch(context.Background(), record, inputCall(id, "\x03", false))
	if err != nil || !strings.Contains(interrupted.(jobOutputResult).Output, "interrupted") {
		t.Fatal(interrupted, err)
	}
	final, err := engine.dispatch(context.Background(), record, inputCall(id, "quit\n", false))
	if err != nil || !strings.Contains(final.(jobOutputResult).Output, "bye") {
		t.Fatal("exit waited for stdin EOF", final, err)
	}
	completed, err := engine.jobTool(context.Background(), record, "job_wait", id)
	if err != nil || completed.(jobOutputResult).Status != "completed" {
		t.Fatal(completed, err)
	}
	if reviews.Load() != 3 {
		t.Fatal("control input escaped approval", reviews.Load())
	}
}

func TestDeniedTerminalInputNeverReachesShell(t *testing.T) {
	var reviews atomic.Int32
	engine, record, project := interactiveEngine(t, &reviews)
	result, err := engine.executeRemote(context.Background(), record, executionRequest{Command: "/bin/sh", TTY: true, YieldTime: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["jobId"].(string)
	if _, err := engine.dispatch(context.Background(), record, inputCall(id, "printf denied > denied.txt\n", false)); err == nil {
		t.Fatal("input denial ignored")
	}
	if _, err := engine.dispatch(context.Background(), record, inputCall(id, "printf allowed > allowed.txt\nexit\n", false)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(project.Directory, "denied.txt")); !os.IsNotExist(err) {
		t.Fatal("denied input executed")
	}
	if data, err := os.ReadFile(filepath.Join(project.Directory, "allowed.txt")); err != nil || string(data) != "allowed" {
		t.Fatal(string(data), err)
	}
}

func TestBackgroundCompletionIsContextDataAndNeverRestartsIdleAgent(t *testing.T) {
	var reviews atomic.Int32
	engine, record, _ := interactiveEngine(t, &reviews)
	result, err := engine.executeRemote(context.Background(), record, executionRequest{
		Command: "sleep 0.1; printf finished", YieldTime: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["jobId"].(string)
	finished, err := engine.jobTool(context.Background(), record, "job_wait", id)
	if err != nil || finished.(jobOutputResult).Output != "finished" {
		t.Fatal(finished, err)
	}
	contexts, err := engine.captureRuntimeContext(record, nil)
	if err != nil || len(contexts) == 0 || !strings.Contains(contexts[len(contexts)-1].Content, id) {
		t.Fatal(contexts, err)
	}
	var current domain.Agent
	_ = engine.Store.Load("agent", record.ID, &current)
	if current.Status != "idle" || reviews.Load() != 0 {
		t.Fatal("completion restarted idle inference", current, reviews.Load())
	}
}
