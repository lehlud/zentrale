package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestSharedEnvironmentStopsAfterLastWorkerAndRestartsOnResume(t *testing.T) {
	var mainID string
	mainDone, childDone := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-mainDone:
		default:
			close(mainDone)
		}
		select {
		case <-childDone:
		default:
			close(childDone)
		}
	}()
	started := make(chan string, 8)
	provider := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		started <- request.CacheKey
		finished := childDone
		if request.CacheKey == mainID {
			finished = mainDone
		}
		select {
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		case <-finished:
			return providers.Result{Text: "Done", UsageKnown: true}, nil
		}
	})
	var stops atomic.Int32
	var mutex sync.Mutex
	var prepared []remote.Execution
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, main, project := executionTestEngine(t, provider, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "MicroVM requires an agent and project identity") {
			command = strings.ReplaceAll(command, `'"'"'`, `'`)
			if strings.Contains(command, " 'select' ") || strings.Contains(command, " 'ensure' ") {
				fmt.Fprintln(stdout, "microvm")
				return 0
			}
			if strings.Contains(command, " 'stage' ") || strings.Contains(command, " 'run' ") {
				io.Copy(io.Discard, input)
				if strings.Contains(command, " 'stage' ") {
					fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
				}
				return 0
			}
			var payload struct {
				Execution remote.Execution `json:"execution"`
			}
			if err := json.NewDecoder(input).Decode(&payload); err != nil {
				t.Error(err)
				return 1
			}
			if strings.Contains(command, " 'prepare' ") {
				mutex.Lock()
				prepared = append(prepared, payload.Execution)
				mutex.Unlock()
			} else if strings.Contains(command, " 'stop' ") {
				stops.Add(1)
			} else {
				t.Error("unexpected lifecycle action")
				return 1
			}
			fmt.Fprint(stdout, `{"os":"fedora","version":"45"}`)
			return 0
		}
		if strings.Contains(command, "test -c /dev/kvm") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		return shell(command, input, stdout, stderr)
	})
	mainID = main.ID
	child, err := engine.Create(project.ID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	waitStarted := func(id string) {
		t.Helper()
		select {
		case got := <-started:
			if got != id {
				t.Fatalf("started %s, wanted %s", got, id)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not reach inference")
		}
	}
	for _, id := range []string{main.ID, child.ID} {
		if err := engine.Prompt(id, "Inspect this project"); err != nil {
			t.Fatal(err)
		}
		waitStarted(id)
	}
	close(mainDone)
	if final := waitForAgent(t, engine, main.ID); final.Status != "idle" {
		t.Fatal(final)
	}
	if err := engine.stopIdleVMs(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 0 {
		t.Fatal("main completion stopped the active subagent")
	}
	close(childDone)
	if final := waitForAgent(t, engine, child.ID); final.Status != "idle" {
		t.Fatal(final)
	}
	deadline := time.Now().Add(5 * time.Second)
	for stops.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stops.Load() != 1 {
		t.Fatalf("automatic stops: %d", stops.Load())
	}
	if err := engine.Prompt(child.ID, "Continue the inspection"); err != nil {
		t.Fatal(err)
	}
	waitStarted(child.ID)
	if final := waitForAgent(t, engine, child.ID); final.Status != "idle" {
		t.Fatal(final)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if len(prepared) != 3 {
		t.Fatalf("preparations: %d", len(prepared))
	}
	for _, execution := range prepared {
		if execution.AgentID != main.ID || execution.TemporaryDirectory != agentTemporaryDirectory(main.ID) {
			t.Fatalf("worker received a separate environment: %+v", execution)
		}
	}
	records, err := engine.listAll("microvm", &main.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("shared VM records: %v %v", records, err)
	}
}

func TestSubagentCannotResetSharedEnvironmentOrReachApproval(t *testing.T) {
	var approvals atomic.Int32
	engine, main := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		approvals.Add(1)
		return providers.Result{}, nil
	}))
	child, err := engine.Create(main.ProjectID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.dispatch(context.Background(), child, providers.Call{Name: "harness", Arguments: map[string]any{
		"action": "microvm_reset", "arguments": "{}", "justification": "recover",
	}})
	if err == nil || !strings.Contains(err.Error(), "only the main agent") || approvals.Load() != 0 {
		t.Fatalf("subagent reset: %v approvals=%d", err, approvals.Load())
	}
}

func TestResetPreservesActiveSubagentsAndPreparingCommands(t *testing.T) {
	engine, main := testEngine(t, nil)
	child, err := engine.Create(main.ProjectID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	reset := engine.useEnvironment(main.ID)
	defer reset()
	check := func() {
		t.Helper()
		// Busy work must reject the reset before touching a remote connection.
		_, err := engine.manageVM(context.Background(), nil, domain.Machine{ID: "worker"}, "reset", remote.Execution{AgentID: main.ID})
		if err == nil || !strings.Contains(err.Error(), "before resetting") {
			t.Fatalf("reset did not preserve active work: %v", err)
		}
	}
	preparing := engine.useEnvironment(child.ID)
	check()
	preparing()
	preparing = engine.useEnvironment(main.ID)
	check()
	preparing()
}

func TestMainAndSubagentOpenTheSameEditorSession(t *testing.T) {
	shell := shellPeerHandler("/bin/sh", os.Environ())
	engine, main, project, _ := managedWorkspaceEngine(t, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "ZENTRALE_EDITOR_READY ") {
			fmt.Fprintln(stdout, `ZENTRALE_EDITOR_READY {"socketPath":"/tmp/ze-shared-editor/vscode.sock"}`)
			io.Copy(io.Discard, input)
			return 0
		}
		return shell(command, input, stdout, stderr)
	})
	child, err := engine.Create(project.ID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := engine.StartEditor(context.Background(), project.ID, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.StartEditor(context.Background(), project.ID, main.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || first.AgentID != main.ID {
		t.Fatalf("separate editors: %+v %+v", first, second)
	}
	if err := engine.Editors.Stop(project.ID, first.ID); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentBusyIncludesBackgroundJobsAndPreparationOnTheirHost(t *testing.T) {
	engine, main := testEngine(t, nil)
	child, err := engine.Create(main.ProjectID, "Worker", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	family, err := engine.agentFamily(main)
	if err != nil {
		t.Fatal(err)
	}
	release := engine.useEnvironment(child.ID)
	if !engine.environmentBusy(family, "gpu-a") {
		t.Fatal("preparation did not hold the environment")
	}
	release()
	engine.mu.Lock()
	engine.jobs["training"] = &runningJob{job: domain.Job{AgentID: child.ID, MachineID: "gpu-a"}, cancel: func() {}}
	engine.mu.Unlock()
	defer func() { engine.mu.Lock(); delete(engine.jobs, "training"); engine.mu.Unlock() }()
	if !engine.environmentBusy(family, "gpu-a") || engine.environmentBusy(family, "gpu-b") {
		t.Fatal("background job did not hold only its own host")
	}
}

func TestEnvironmentLockWaitCanBeCancelled(t *testing.T) {
	operation := &vmOperation{gate: make(chan struct{}, 1)}
	if err := operation.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := operation.Lock(ctx); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestIdleCleanupDoesNotWaitForAnEnvironmentBeingPrepared(t *testing.T) {
	engine, main := testEngine(t, nil)
	family := map[string]bool{main.ID: true}
	release := engine.useEnvironment(main.ID)
	defer release()
	operation := engine.vmOperation(main.ID, "host")
	if err := operation.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer operation.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := engine.stopVMRecord(ctx, map[string]any{"agentId": main.ID, "machineId": "host"}, family); err != nil {
		t.Fatalf("idle cleanup waited behind a busy startup: %v", err)
	}
}

func TestFailedStagingStillTracksGuestForAutomaticCleanup(t *testing.T) {
	var stops atomic.Int32
	engine, main, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, " 'stage' ") {
			io.Copy(io.Discard, input)
			fmt.Fprintln(stderr, "guest startup failed")
			return 1
		}
		if strings.Contains(command, " 'stop' ") {
			io.Copy(io.Discard, input)
			stops.Add(1)
			fmt.Fprint(stdout, `{}`)
		}
		return 0
	})
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	machine.Sandbox = remote.SandboxMicroVM
	connection, err := engine.Remote.Connector.Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	operation := engine.vmOperation(main.ID, machine.ID)
	if err := operation.Lock(context.Background()); err != nil {
		t.Fatal(err)
	}
	operation.stopped = true
	operation.Unlock()
	_, _, err = engine.sandbox(context.Background(), connection, machine, remote.Execution{
		AgentID: main.ID, ProjectID: project.ID, Workspace: project.Directory, Command: "printf ready",
	})
	if err == nil || !strings.Contains(err.Error(), "guest startup failed") {
		t.Fatal(err)
	}
	if err := engine.stopIdleVMs(context.Background(), main); err != nil {
		t.Fatal(err)
	}
	if stops.Load() != 1 {
		t.Fatalf("failed guest was left running: stops=%d", stops.Load())
	}
}

func TestDeletingFamilyRemovesDescendantsButChildKeepsSharedDisk(t *testing.T) {
	var removals atomic.Int32
	engine, main, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, _ io.Writer) int {
		io.Copy(io.Discard, input)
		if strings.Contains(strings.ReplaceAll(command, `'"'"'`, `'`), " 'remove' ") {
			removals.Add(1)
		}
		fmt.Fprint(stdout, `{}`)
		return 0
	})
	first, err := engine.Create(project.ID, "First", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.Create(project.ID, "Second", main.ProviderID, "approver-provider", main.ID)
	if err != nil {
		t.Fatal(err)
	}
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.rememberMicroVM(first, machine, project, remote.SandboxMicroVM); err != nil {
		t.Fatal(err)
	}
	if err := engine.Delete("agent", first.ID); err != nil {
		t.Fatal(err)
	}
	if removals.Load() != 0 {
		t.Fatal("removing a child deleted the shared disk")
	}
	records, err := engine.listAll("microvm", &main.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("parent disk record: %v %v", records, err)
	}
	if err := engine.Delete("agent", main.ID); err != nil {
		t.Fatal(err)
	}
	if removals.Load() != 1 {
		t.Fatalf("disk removals: %d", removals.Load())
	}
	for _, id := range []string{main.ID, main.ApproverID, first.ID, first.ApproverID, second.ID, second.ApproverID} {
		var record domain.Agent
		if err := engine.Store.Load("agent", id, &record); err == nil {
			t.Fatalf("orphan agent: %s", id)
		}
	}
}
