package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestWarmEditorDoesNotWaitForAgentWorkspaceReceipt(t *testing.T) {
	var blockReceipt, blockProbe atomic.Bool
	receiptEntered, probeEntered := make(chan struct{}), make(chan struct{})
	releaseReceipt, releaseProbe := make(chan struct{}), make(chan struct{})
	var receiptOnce, probeOnce sync.Once
	defer receiptOnce.Do(func() { close(releaseReceipt) })
	defer probeOnce.Do(func() { close(releaseProbe) })
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, record, project, machine := managedWorkspaceEngine(t, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		normalized := strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(normalized, "guest dispatcher is unresponsive") {
			if blockProbe.CompareAndSwap(true, false) {
				close(probeEntered)
				<-releaseProbe
			}
			_, _ = io.Copy(io.Discard, input)
			fmt.Fprintln(stdout, strings.Repeat("a", 64))
			return 0
		}
		if strings.Contains(normalized, "<zentrale-microvm>") {
			_, _ = io.Copy(io.Discard, input)
			switch {
			case strings.Contains(normalized, " 'ensure'"), strings.Contains(normalized, " 'select'"):
				fmt.Fprintln(stdout, "microvm")
			case strings.Contains(normalized, " 'prepare'"):
				fmt.Fprintln(stdout, `{"bootId":"11111111-2222-3333-4444-555555555555"}`)
			case strings.Contains(normalized, " 'stage'"):
				fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
			}
			return 0
		}
		if strings.Contains(normalized, "Prepare a project directory inside configured persistent storage") && blockReceipt.CompareAndSwap(true, false) {
			close(receiptEntered)
			<-releaseReceipt
		}
		return shell(command, input, stdout, stderr)
	})
	machine.Sandbox = remote.SandboxMicroVM
	if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := engine.requireConnection(ctx, record.ID); err != nil {
		t.Fatal(err)
	}

	// Simulate the agent's normal connection check stuck in the real SSH
	// managed-workspace receipt script. Its VM/SSH transport are already warm.
	blockReceipt.Store(true)
	agentDone := make(chan error, 1)
	go func() {
		connection, _, _, err := engine.connect(ctx, record)
		if connection != nil {
			connection.Close()
		}
		agentDone <- err
	}()
	select {
	case <-receiptEntered:
	case <-ctx.Done():
		t.Fatal("agent receipt did not reach the SSH barrier")
	}
	opened := make(chan error, 1)
	var phaseMutex sync.Mutex
	var warmPhases []string
	openerContext := remote.WithStartupProgress(ctx, func(phase string) {
		phaseMutex.Lock()
		warmPhases = append(warmPhases, phase)
		phaseMutex.Unlock()
	})
	go func() {
		_, err := engine.withEditorEnvironment(openerContext, project.ID, record.ID, func(_ context.Context, _ *remote.Connection, _ domain.Machine, _ domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
			if target.PreparedSandbox != remote.SandboxMicroVM {
				return domain.EditorSession{}, fmt.Errorf("validated native selection was not passed to terminal preparation")
			}
			return domain.EditorSession{}, nil
		})
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("warm workspace opener waited for another agent's remote receipt")
	}
	phaseMutex.Lock()
	for _, phase := range warmPhases {
		if phase == "vm_starting" || phase == "preparing_environment" || phase == "preparing_sources" {
			t.Errorf("warm validated environment reported a cold-start phase: %v", warmPhases)
		}
	}
	phaseMutex.Unlock()
	receiptOnce.Do(func() { close(releaseReceipt) })
	if err := <-agentDone; err != nil {
		t.Fatal(err)
	}

	// An old generation probe must not invalidate the replacement connection
	// or erase a newer receipt installed while its SSH request was in flight.
	old, _, _, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	blockProbe.Store(true)
	replacementReady := make(chan *remote.Connection, 1)
	reopened := make(chan error, 1)
	go func() {
		_, err := engine.withEditorEnvironment(ctx, project.ID, record.ID, func(_ context.Context, connection *remote.Connection, _ domain.Machine, _ domain.Project, _ remote.EditorTarget) (domain.EditorSession, error) {
			// Publish through a Go synchronization primitive too: SSH network I/O
			// alone is not a race-detector-visible memory publication barrier.
			var replacement *remote.Connection
			select {
			case replacement = <-replacementReady:
			case <-ctx.Done():
				return domain.EditorSession{}, ctx.Err()
			}
			if connection.Client != replacement.Client || connection.Client == old.Client {
				return domain.EditorSession{}, fmt.Errorf("editor continued preparation on its stale retained connection")
			}
			return domain.EditorSession{}, nil
		})
		reopened <- err
	}()
	select {
	case <-probeEntered:
	case <-ctx.Done():
		t.Fatal("readiness probe did not reach the SSH barrier")
	}
	engine.releaseConnection(record.ID)
	replacement, _, _, err := engine.connect(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	replacementReady <- replacement
	entry := engine.connectionEntry(record.ID)
	// A sentinel owned by the replacement, never consumed as a readiness proof.
	newer := &connectionReadiness{generation: "replacement-owned-receipt"}
	entry.mutex.Lock()
	entry.ready, entry.sourcesKey = newer, "replacement-owned-sources"
	entry.mutex.Unlock()
	probeOnce.Do(func() { close(releaseProbe) })
	select {
	case err := <-reopened:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("editor did not reconnect after its generation probe became stale")
	}
	entry.mutex.Lock()
	// Cold preparation may replace the sentinel with a newly validated proof,
	// but it must use and preserve the replacement's transport, never the old one.
	preserved := entry.connection != nil && entry.connection.Client == replacement.Client && entry.ready != nil && entry.sourcesKey != ""
	entry.mutex.Unlock()
	if !preserved {
		t.Fatal("old readiness probe released or invalidated a newer connection")
	}
}
