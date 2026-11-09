package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestFedoraInstallAcceptsNamesButRejectsOptionsAndShellInput(t *testing.T) {
	command, err := fedoraInstallCommand(map[string]any{"packages": []any{"python3", "gcc-c++", "libstdc++-devel.x86_64"}})
	if err != nil || !strings.Contains(command, "'gcc-c++'") || !strings.Contains(command, "microdnf") {
		t.Fatalf("valid installation: %q %v", command, err)
	}
	for _, packages := range [][]any{nil, {"--installroot=/"}, {"x; touch /tmp/host"}, {"https://example.test/root.rpm"}, {"$(id)"}, {42}} {
		if _, err := fedoraInstallCommand(map[string]any{"packages": packages}); err == nil {
			t.Fatalf("accepted invalid packages: %#v", packages)
		}
	}
}

func TestStopAgentVMPreservesScopeAndDoesNotPrepareOrRemoveTheGuest(t *testing.T) {
	var calls atomic.Int32
	engine, record, project := executionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		calls.Add(1)
		if !strings.Contains(strings.ReplaceAll(command, `'"'"'`, `'`), " 'stop' ") {
			t.Error("stopping a VM invoked an action other than stop")
			return 1
		}
		var payload struct {
			Execution map[string]any `json:"execution"`
		}
		if err := json.NewDecoder(input).Decode(&payload); err != nil || payload.Execution["agentId"] == "" || payload.Execution["projectId"] == "" {
			t.Errorf("missing persistent VM identity: %v", err)
			return 1
		}
		fmt.Fprint(stdout, `{"stopped":true}`)
		return 0
	})
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.rememberMicroVM(record, machine, project, remote.SandboxMicroVM); err != nil {
		t.Fatal(err)
	}
	if err := engine.StopAgentVM(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	var retained domain.Agent
	records, err := engine.listAll("microvm", &record.ID)
	if err != nil || len(records) != 1 || engine.Store.Load("agent", record.ID, &retained) != nil || retained.Status != record.Status || calls.Load() != 1 {
		t.Fatalf("stop removed persistent resources or prepared a new VM: records=%v calls=%d err=%v", records, calls.Load(), err)
	}
}

func TestStopAgentVMRejectsActiveWorkAndApprovers(t *testing.T) {
	for _, state := range []string{"running", "waiting", "worker", "preparing", "job", "approver"} {
		t.Run(state, func(t *testing.T) {
			engine, record := testEngine(t, nil)
			t.Cleanup(func() {
				engine.mu.Lock()
				delete(engine.running, record.ID)
				delete(engine.jobs, "active")
				engine.mu.Unlock()
			})
			switch state {
			case "running", "waiting":
				record.Status = state
			case "worker":
				engine.running[record.ID] = func() {}
			case "preparing":
				engine.preparing[record.ID] = 1
			case "job":
				engine.jobs["active"] = &runningJob{job: domain.Job{AgentID: record.ID}}
			case "approver":
				record.Role = "approver"
			}
			if err := engine.saveAgent(record); err != nil {
				t.Fatal(err)
			}
			if err := engine.StopAgentVM(context.Background(), record.ID); err == nil {
				t.Fatalf("accepted %s", state)
			}
		})
	}
}

func TestMicroVMActionsRequireApprovalAndPackagesRequireNetwork(t *testing.T) {
	var remoteCalls, approvals atomic.Int32
	engine, record, _ := executionTestEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		approvals.Add(1)
		return providers.Result{Text: `{"allowed":false,"reason":"test denial","blanket":false}`, UsageKnown: true}, nil
	}), func(_ string, _ io.Reader, _, _ io.Writer) int {
		remoteCalls.Add(1)
		return 0
	})
	call := func(action string, network bool) providers.Call {
		return providers.Call{Name: "harness", Arguments: map[string]any{"action": action, "arguments": `{"packages":["python3"]}`, "network": network, "justification": "test guest administration"}}
	}
	if _, err := engine.dispatch(context.Background(), record, call("packages_install", false)); err == nil || !strings.Contains(err.Error(), "network=true") {
		t.Fatalf("installation without network grant: %v", err)
	}
	if approvals.Load() != 0 || remoteCalls.Load() != 0 {
		t.Fatal("missing network grant reached approval or remote execution")
	}
	for _, action := range []string{"packages_install", "microvm_reset", "microvm_info"} {
		if _, err := engine.dispatch(context.Background(), record, call(action, true)); err == nil || !strings.Contains(err.Error(), "test denial") {
			t.Fatalf("%s bypassed approval: %v", action, err)
		}
	}
	if approvals.Load() != 3 || remoteCalls.Load() != 0 {
		t.Fatalf("denied actions reached the SSH host: approvals=%d remote=%d", approvals.Load(), remoteCalls.Load())
	}
}

func TestAgentMicroVMStartupFailureBlocksInference(t *testing.T) {
	var providerCalls atomic.Int32
	engine, record, _ := executionTestEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		providerCalls.Add(1)
		return providers.Result{}, nil
	}), func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			fmt.Fprintln(stdout, "microvm")
		} else if strings.Contains(command, "MicroVM requires an agent and project identity") {
			io.Copy(io.Discard, input)
			fmt.Fprintln(stderr, "Fedora guest disk cannot be mounted; reset required")
			return 1
		}
		return 0
	})
	if err := engine.Prompt(record.ID, "work in the guest"); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "error" || !strings.Contains(final.Error, "guest disk cannot be mounted") {
		t.Fatal(final)
	}
	messages, err := engine.loadHistory(record.ID)
	if err != nil || len(messages) != 1 || providerCalls.Load() != 0 {
		t.Fatalf("broken guest consumed prompt or called provider: messages=%v provider=%d err=%v", messages, providerCalls.Load(), err)
	}
}
