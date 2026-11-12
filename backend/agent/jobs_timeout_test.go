package agent

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestCommandTimeoutValidationAndCompatibilityDefault(t *testing.T) {
	for _, test := range []struct {
		value, want time.Duration
		invalid     bool
	}{
		{0, 30 * time.Minute, false},
		{time.Millisecond, time.Millisecond, false},
		{7 * 24 * time.Hour, 7 * 24 * time.Hour, false},
		{-time.Millisecond, 0, true},
		{time.Nanosecond, 0, true},
		{7*24*time.Hour + time.Millisecond, 0, true},
	} {
		got, err := jobTimeout(test.value)
		if (err != nil) != test.invalid || got != test.want {
			t.Fatalf("jobTimeout(%s) = %s, %v", test.value, got, err)
		}
	}
}

func TestJobDeadlineFinalizesForegroundBackgroundAndInteractive(t *testing.T) {
	for _, mode := range []string{"foreground", "background", "interactive"} {
		t.Run(mode, func(t *testing.T) {
			engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				t.Error("job deadline must not invoke or retry the model")
				return providers.Result{}, nil
			}))
			// This in-process peer deliberately does not finish the command.
			// Release it even on failure so a regression cannot hang test cleanup.
			release := make(chan struct{})
			var starts atomic.Int32
			peer, err := testssh.StartWithCommandHandler(func(_ string, _ io.Reader, out, _ io.Writer) int {
				starts.Add(1)
				_, _ = io.WriteString(out, "output before deadline\n")
				<-release
				return 0
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(peer.Close)
			t.Cleanup(func() { close(release) })
			var machine domain.Machine
			if err := engine.Store.Load("machine", "test-worker", &machine); err != nil {
				t.Fatal(err)
			}
			public, err := remote.DecodePublicKey(peer.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := engine.Store.TrustSSHKey(public); err != nil {
				t.Fatal(err)
			}
			machine.Host, machine.Port = peer.Host, peer.Port
			if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
				t.Fatal(err)
			}
			connection, err := engine.Remote.Connector.Dial(context.Background(), machine.ID)
			if err != nil {
				t.Fatal(err)
			}
			var project domain.Project
			if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
				connection.Close()
				t.Fatal(err)
			}
			request := executionRequest{
				Timeout:    500 * time.Millisecond,
				Background: mode == "background", Interactive: mode == "interactive",
			}
			running, err := engine.startJob(record, machine, project, connection, "deadline-fixture", "deadline-fixture", request, remote.SandboxDisabled)
			if err != nil {
				connection.Close()
				t.Fatal(err)
			}
			select {
			case <-running.done:
			case <-time.After(3 * time.Second):
				t.Fatal("deadline did not finalize job")
			}
			running.mu.Lock()
			job := running.job
			running.mu.Unlock()
			if job.Status != "timed_out" || job.TimeoutMS != 500 ||
				!strings.Contains(job.Error, "context deadline exceeded") ||
				!strings.Contains(job.Output, "output before deadline") {
				t.Fatalf("deadline lost status, timeout or output: %+v", job)
			}
			if starts.Load() != 1 {
				t.Fatal("timed-out job was replayed", starts.Load())
			}
			var persisted domain.Job
			if err := engine.Store.Load("job", job.ID, &persisted); err != nil || persisted.Status != "timed_out" || persisted.Output != job.Output {
				t.Fatal("deadline result not persisted", persisted, err)
			}
			if mode == "background" {
				var completion map[string]any
				if err := engine.Store.Load("job_completion", job.ID, &completion); err != nil || completion["status"] != "timed_out" {
					t.Fatal("background timeout completion missing", completion, err)
				}
			}
		})
	}
}
