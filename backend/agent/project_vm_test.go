package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestProjectRequiresExactlyOneMachineOrPool(t *testing.T) {
	engine, record := testEngine(t, nil)
	var project domain.Project
	if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Save("pool", "test-pool", "", domain.Pool{ID: "test-pool", Name: "Test", MaxMachines: 2}); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		machines, pools []string
		valid           bool
	}{
		{project.MachineIDs, nil, true},
		{nil, []string{"test-pool"}, true},
		{nil, nil, false},
		{project.MachineIDs, []string{"test-pool"}, false},
		{[]string{"a", "b"}, nil, false},
		{nil, []string{"test-pool", "test-pool"}, false},
	} {
		candidate := project
		candidate.ID = NewID()
		candidate.MachineIDs, candidate.PoolIDs = target.machines, target.pools
		_, err := engine.SaveProject(candidate)
		if (err == nil) != target.valid {
			t.Fatalf("machines=%v pools=%v: %v", target.machines, target.pools, err)
		}
	}
}

func TestProjectResetUsesRecordedHostsAndPreservesOtherProjects(t *testing.T) {
	var mu sync.Mutex
	var resets []remote.Execution
	engine, record, project := vmExecutionTestEngine(t, nil, func(command string, input io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "test -c /dev/kvm") {
			fmt.Fprintln(stdout, "microvm")
			return 0
		}
		var payload struct {
			Execution remote.Execution `json:"execution"`
		}
		if err := json.NewDecoder(input).Decode(&payload); err != nil {
			t.Error(err)
			return 1
		}
		if strings.Contains(strings.ReplaceAll(command, `'"'"'`, `'`), " 'reset'") {
			mu.Lock()
			resets = append(resets, payload.Execution)
			mu.Unlock()
		}
		fmt.Fprint(stdout, `{"stopped":true}`)
		return 0
	})
	machine, err := engine.Remote.Connector.Machine(project.MachineIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	otherHost := machine
	otherHost.ID = "second-project-host"
	if err := engine.Store.Save("machine", otherHost.ID, "", otherHost); err != nil {
		t.Fatal(err)
	}
	for _, host := range []domain.Machine{machine, otherHost} {
		if err := engine.rememberMicroVM(record, host, project, remote.SandboxMicroVM); err != nil {
			t.Fatal(err)
		}
	}
	other := project
	other.ID = "untouched-project"
	if err := engine.Store.Save("project", other.ID, "", other); err != nil {
		t.Fatal(err)
	}
	otherAgent := domain.Agent{ID: "unrelated-agent", ProjectID: other.ID}
	if err := engine.Store.Save("agent", otherAgent.ID, other.ID, otherAgent); err != nil {
		t.Fatal(err)
	}
	if err := engine.rememberMicroVM(otherAgent, machine, other, remote.SandboxMicroVM); err != nil {
		t.Fatal(err)
	}
	release := engine.useEnvironment(remote.EditorScopeID(project.ID, ""))
	if err := engine.ResetProjectVM(context.Background(), project.ID); err == nil {
		t.Fatal("reset accepted while project preparation was active")
	}
	release()
	if err := engine.ResetProjectVM(context.Background(), project.ID); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(resets) != 2 {
		t.Fatalf("wanted reset on both recorded hosts, got %d", len(resets))
	}
	for _, execution := range resets {
		if execution.ProjectID != project.ID || execution.AgentID != remote.EditorScopeID(project.ID, "") || !strings.HasPrefix(execution.TemporaryDirectory, "/tmp/zentrale-") {
			t.Fatalf("wrong reset scope: %+v", execution)
		}
	}
}
