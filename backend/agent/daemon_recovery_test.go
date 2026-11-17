package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestDaemonJobSurvivesEngineRestartWithoutReplay(t *testing.T) {
	engine, record, _ := executionTestEngine(t, nil, shellPeerHandler("/bin/sh", os.Environ()))
	engine.Remote.Connector.NativeSessionsForTests = false
	gate := filepath.Join(t.TempDir(), "finish")
	starts := filepath.Join(t.TempDir(), "starts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := engine.executeRemote(ctx, record, executionRequest{
		Command:    "printf x >> " + remote.Quote(starts) + "; printf before; while ! test -f " + remote.Quote(gate) + "; do sleep 0.02; done; printf after",
		Background: true, Timeout: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	id := stringArg(result.(map[string]any), "jobId")
	for {
		if _, err := os.Stat(starts); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	engine.Close()
	var saved domain.Job
	if err := engine.Store.Load("job", id, &saved); err != nil || saved.Status != "running" {
		t.Fatalf("shutdown lost durable attachment: %+v %v", saved, err)
	}
	restarted, err := New(engine.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if err := engine.Store.Load("job", id, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Status != "running" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if saved.Status != "completed" || !strings.Contains(saved.Output, "beforeafter") {
		t.Fatalf("recovery failed: %+v", saved)
	}
	value, err := os.ReadFile(starts)
	if err != nil || string(value) != "x" {
		t.Fatalf("command replayed: %q %v", value, err)
	}
}

func TestDurableDaemonStopIsConfirmedAfterServiceRestart(t *testing.T) {
	engine, record, _ := executionTestEngine(t, nil, shellPeerHandler("/bin/sh", os.Environ()))
	engine.Remote.Connector.NativeSessionsForTests = false
	directory := t.TempDir()
	started, gate, finished := filepath.Join(directory, "started"), filepath.Join(directory, "gate"), filepath.Join(directory, "finished")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := engine.executeRemote(ctx, record, executionRequest{Command: "printf started > " + remote.Quote(started) + "; while ! test -f " + remote.Quote(gate) + "; do sleep 0.02; done; printf late > " + remote.Quote(finished), Background: true, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	id := stringArg(result.(map[string]any), "jobId")
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	var job domain.Job
	if err := engine.Store.Load("job", id, &job); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash after accepting the stop but before its remote ACK.
	if err := engine.Store.SaveResources([]datastore.ResourceWrite{jobStopWrite(job)}); err != nil {
		t.Fatal(err)
	}
	engine.Remote.Connector.Close()
	engine.Close()
	restarted, err := New(engine.Store, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	for {
		if err := engine.Store.Load("job", id, &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "stopped" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := os.Stat(finished); !os.IsNotExist(err) {
		t.Fatal("stopped process continued after recovery", err)
	}
}
