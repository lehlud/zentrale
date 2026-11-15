package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func TestRuntimeProjectUsesCurrentExecutionPathsWithoutChangingStorage(t *testing.T) {
	for _, test := range []struct {
		name, mode, observed, runner string
		guest, worker                bool
	}{
		{name: "existing main in managed VM", mode: "microvm", guest: true},
		{name: "new worker in existing project", mode: "microvm", guest: true, worker: true},
		{name: "automatic VM", mode: "auto", observed: "microvm", guest: true},
		{name: "automatic host", mode: "auto", observed: "none"},
		{name: "unobserved auto", mode: "auto"},
		{name: "explicit host", mode: "none"},
		{name: "bubblewrap", mode: "bwrap"},
		{name: "custom VM", mode: "microvm", runner: "/usr/bin/custom-vm"},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				t.Error("runtime context must not call inference")
				return providers.Result{}, nil
			}))
			var project domain.Project
			if err := engine.Store.Load("project", record.ProjectID, &project); err != nil {
				t.Fatal(err)
			}
			record.MachineID = project.MachineIDs[0]
			if test.worker {
				record.ParentID = record.ID
				record.ID, record.Role = NewID(), "worker"
			}
			if err := engine.Store.Save("agent", record.ID, record.ProjectID, record); err != nil {
				t.Fatal(err)
			}

			machine, err := engine.Remote.Connector.Machine(record.MachineID)
			if err != nil {
				t.Fatal(err)
			}
			machine.Sandbox, machine.MicroVMCommand = test.mode, test.runner
			if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
				t.Fatal(err)
			}
			if test.observed != "" {
				if err := engine.rememberExecutionSandbox(machine, test.observed, false); err != nil {
					t.Fatal(err)
				}
			}

			snapshots, err := engine.captureRuntimeContext(record, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, content, ok := strings.Cut(snapshots[len(snapshots)-1].Content, "\n")
			var state struct {
				Project domain.Project `json:"project"`
			}
			if !ok || json.Unmarshal([]byte(content), &state) != nil {
				t.Fatal("invalid runtime context")
			}
			expected := project.Directory
			if test.guest {
				expected = remote.ManagedGuestWorkspace
			}
			if state.Project.Directory != expected {
				t.Fatalf("runtime directory = %q, want %q", state.Project.Directory, expected)
			}
			var stored domain.Project
			if err := engine.Store.Load("project", project.ID, &stored); err != nil || stored.Directory != project.Directory {
				t.Fatal("runtime presentation changed host-backed storage", stored.Directory, err)
			}
		})
	}
}
