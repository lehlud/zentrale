package remote

import (
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestResourceCatalogIncludesGPUDiscoveryAndExplicitAssignments(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, machine := range []domain.Machine{
		{ID: "automatic", Properties: []string{"GPU"}},
		{ID: "explicit", GPUDevices: []string{"0000:01:00.0"}},
		{ID: "cpu"},
	} {
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(&Connector{Store: store})
	state, err := manager.ResourceState(domain.Project{MachineIDs: []string{"automatic", "explicit", "cpu"}})
	if err != nil {
		t.Fatal(err)
	}
	hosts := state["configuredGPUHosts"].([]any)
	if len(hosts) != 2 || hosts[0].(map[string]any)["gpuDiscovery"] != true || hosts[1].(map[string]any)["gpuDiscovery"] != false {
		t.Fatal(state)
	}
	candidate := machineCandidate{id: "automatic"}
	if !manager.reserveJobCandidate(candidate, "first", true) || manager.reserveJobCandidate(candidate, "second", true) {
		t.Fatal("GPU discovery bypassed exclusive scheduling")
	}
	manager.Release("first")
	if !manager.reserveJobCandidate(candidate, "second", true) {
		t.Fatal("released autodetection host remains reserved")
	}
}
