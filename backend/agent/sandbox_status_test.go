package agent

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestSandboxSelectionTracksPreflightExecutionAndBypass(t *testing.T) {
	var selection atomic.Value
	selection.Store("bwrap")
	var probes atomic.Int32
	handler := func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			probes.Add(1)
			level := selection.Load().(string)
			if level == "error" {
				_, _ = fmt.Fprintln(stderr, "sandbox probe unavailable")
				return 127
			}

			_, _ = fmt.Fprintln(stdout, level)
		} else if strings.Contains(command, "written") {
			_, _ = io.Copy(io.Discard, input)
			_, _ = fmt.Fprintln(stdout, `{"written":true}`)
		}

		return 0
	}

	engine, record, project := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("sandbox checks must not call a provider")
		return providers.Result{}, nil
	}), handler)

	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	machine.MicroVMCommand = "/opt/zentrale/test-microvm"

	if _, err := engine.SaveMachine(machine); err != nil {
		t.Fatal(err)
	}

	prepared := engine.CheckAgentConnection(context.Background(), record.ID)
	if !prepared.Connected || prepared.Sandbox.Level != "bwrap" || prepared.Sandbox.Mode != "auto" {
		t.Fatalf("preflight selection: %+v", prepared)
	}

	assertSelection := func(expected string) {
		t.Helper()
		statuses, err := engine.SandboxStatuses()
		if err != nil ||
			len(statuses) != 1 ||
			statuses[0].Level != expected ||
			statuses[0].MachineID != project.MachineIDs[0] ||
			statuses[0].CheckedAt.IsZero() {
			t.Fatalf("persisted sandbox selection: %+v, %v", statuses, err)
		}
	}

	assertSelection("bwrap")

	// Execution probes again: a machine's available isolation can change.
	selection.Store("microvm")
	result, err := engine.file(
		context.Background(),
		record,
		"write_file",
		map[string]any{
			"path":    "example.py",
			"content": "print(42)",
		},
		false,
		false,
	)
	if err != nil || result["sandbox"] != "microvm" {
		t.Fatalf("file execution selection: %+v, %v", result, err)
	}

	assertSelection("microvm")
	beforeBypass := probes.Load()

	if _, err := engine.execute(context.Background(), record, "true", false, true); err != nil {
		t.Fatal(err)
	}

	if probes.Load() != beforeBypass {
		t.Fatal("explicit bypass unexpectedly probed auto selection")
	}

	assertSelection("microvm")

	selection.Store("none")
	checked := engine.CheckMachineConnection(context.Background(), project.MachineIDs[0])
	if !checked.Connected || checked.Sandbox.Level != "none" {
		t.Fatalf("connection check omitted unsandboxed selection: %+v", checked)
	}

	assertSelection("none")
	selection.Store("error")
	checked = engine.CheckMachineConnection(context.Background(), project.MachineIDs[0])
	if !checked.Connected ||
		checked.Sandbox.Level != "" ||
		!strings.Contains(checked.Sandbox.Error, "probe unavailable") {
		t.Fatalf("failed detection was presented as a selection: %+v", checked)
	}

	assertSelection("")

	machine, err = engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	machine.Sandbox = "bwrap"

	if _, err := engine.SaveMachine(machine); err != nil {
		t.Fatal(err)
	}

	statuses, err := engine.SandboxStatuses()
	if err != nil || len(statuses) != 0 {
		t.Fatalf("changed configuration retained an old selection: %+v, %v", statuses, err)
	}
}

func TestFailedMicroVMBootOnCachedSSHBlocksProviderAndPreservesPrompt(t *testing.T) {
	var broken atomic.Bool
	var requests atomic.Int32
	handler := func(command string, input io.Reader, stdout, stderr io.Writer) int {
		command = strings.ReplaceAll(command, `'"'"'`, `'`)
		if strings.Contains(command, "test -c /dev/kvm") {
			if broken.Load() {
				_, _ = fmt.Fprintln(stderr, "MicroVM test boot failed")
				return 1
			}

			_, _ = fmt.Fprintln(stdout, "microvm")
		} else if strings.Contains(command, " 'stage' ") {
			_, _ = io.Copy(io.Discard, input)
			_, _ = fmt.Fprintln(stdout, "/tmp/test-vm/session/requests/0123456789abcdef0123456789abcdef")
		} else if strings.Contains(command, "MicroVM requires an agent and project identity") {
			_, _ = io.Copy(io.Discard, input)
			_, _ = fmt.Fprintln(stdout, `{"os":"fedora","version":"45","bootId":"guest-boot"}`)
		}

		return 0
	}

	engine, record, _ := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		requests.Add(1)
		return providers.Result{Text: "must not answer"}, nil
	}), handler)
	if result := engine.CheckAgentConnection(context.Background(), record.ID); !result.Connected {
		t.Fatalf("initial VM preflight failed: %+v", result)
	}

	broken.Store(true)

	if err := engine.Prompt(record.ID, "Create the project"); err != nil {
		t.Fatal(err)
	}

	if final := waitForAgent(t, engine, record.ID); final.Status != "error" ||
		!strings.Contains(final.Error, "MicroVM test boot failed") {
		t.Fatal(final)
	}

	if requests.Load() != 0 {
		t.Fatal("provider was called before its VM runtime was ready")
	}

	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 {
		t.Fatalf("failed VM setup consumed the prompt: %v %v", history, err)
	}

	statuses, err := engine.SandboxStatuses()
	if err != nil ||
		len(statuses) != 1 ||
		statuses[0].Level != "" ||
		!strings.Contains(statuses[0].Error, "MicroVM test boot failed") {
		t.Fatalf("VM failure was not visible in diagnostics: %+v %v", statuses, err)
	}
}

func TestSandboxSelectionFromOlderRunnerMustBeRechecked(t *testing.T) {
	engine, record := testEngine(t, nil)
	project, err := engine.project(record)
	if err != nil {
		t.Fatal(err)
	}

	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}

	legacy := recordedSandboxStatus{
		SandboxStatus: domain.SandboxStatus{
			MachineID: machine.ID,
			Mode:      "auto",
			Level:     "bwrap",
			CheckedAt: time.Now().UTC(),
		},
		Configuration: JSON(machine),
	}
	if err := engine.Store.Save("sandboxStatus", machine.ID, "", legacy); err != nil {
		t.Fatal(err)
	}

	statuses, err := engine.SandboxStatuses()
	if err != nil || len(statuses) != 0 {
		t.Fatalf("old runner selection remained current: %+v %v", statuses, err)
	}

	if err := engine.rememberExecutionSandbox(machine, remote.SandboxMicroVM, false); err != nil {
		t.Fatal(err)
	}

	statuses, err = engine.SandboxStatuses()
	if err != nil || len(statuses) != 1 || statuses[0].Level != remote.SandboxMicroVM {
		t.Fatalf("new observation missing: %+v %v", statuses, err)
	}
}

func TestPoolSandboxObservationUsesQualifiedMachineAndRejectsOldConfiguration(t *testing.T) {
	engine, _ := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("listing sandbox observations must not call a provider")
		return providers.Result{}, nil
	}))

	var original domain.Machine
	machines, err := loadResources[domain.Machine](engine, "machine")
	if err != nil || len(machines) == 0 {
		t.Fatal(err)
	}

	original = machines[0]
	original.Sandbox = "auto"
	pool := domain.Pool{ID: "pool", Machines: []domain.Machine{original}}
	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	qualified := original
	qualified.ID = pool.ID + "/" + original.ID

	if err := engine.rememberExecutionSandbox(qualified, "bwrap", false); err != nil {
		t.Fatal(err)
	}

	statuses, err := engine.SandboxStatuses()
	if err != nil || len(statuses) != 1 || statuses[0].MachineID != qualified.ID {
		t.Fatalf("pool observation: %+v, %v", statuses, err)
	}

	pool.Machines[0].Host = "changed.example.org"

	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}

	if err := engine.rememberExecutionSandbox(qualified, "microvm", false); err != nil {
		t.Fatal(err)
	}

	statuses, err = engine.SandboxStatuses()
	if err != nil || len(statuses) != 0 {
		t.Fatalf("a check of the old host overwrote the pool's configuration: %+v, %v", statuses, err)
	}
}

func TestSandboxPreflightDoesNotWaitForLifecycleLock(t *testing.T) {
	probeStarted := make(chan struct{})
	var probeSignaled atomic.Bool
	finishProbe := make(chan struct{})
	engine, record, _ := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("preflight must not call a provider")
		return providers.Result{}, nil
	}), func(command string, _ io.Reader, stdout, _ io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			// Source preparation may inspect the sandbox again. Notify the
			// waiter once, while retaining the barrier for every probe.
			if probeSignaled.CompareAndSwap(false, true) {
				close(probeStarted)
			}
			<-finishProbe
			_, _ = fmt.Fprintln(stdout, "bwrap")
		}

		return 0
	})

	completed := make(chan domain.MachineConnectionCheck, 1)
	go func() {
		completed <- engine.CheckAgentConnection(context.Background(), record.ID)
	}()

	select {
	case <-probeStarted:
	case <-time.After(5 * time.Second):
		close(finishProbe)
		t.Fatal("sandbox probe did not start")
	}

	// Worker completion can hold configMu while waiting to close this connection.
	engine.configMu.Lock()
	close(finishProbe)

	select {
	case result := <-completed:
		engine.configMu.Unlock()

		if !result.Connected || result.Sandbox.Level != "bwrap" {
			t.Fatalf("preflight failed: %+v", result)
		}
	case <-time.After(2 * time.Second):
		engine.configMu.Unlock()
		<-completed
		t.Fatal("sandbox observation waited for lifecycle lock while holding connection lock")
	}
}
