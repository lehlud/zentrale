package agent

import (
	"reflect"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestMachineSearchPagination(t *testing.T) {
	machines := []domain.Machine{
		{ID: "pool/c", Name: "Compute", Properties: []string{"GPU", "a100"}},
		{ID: "a", Host: "build.example", Properties: []string{"cpu"}},
		{ID: "pool/b", Name: "Training", Properties: []string{"GPU", "a100"}},
	}
	page, err := filterMachinePage(machines, map[string]any{"search": " gpU A100 ", "limit": float64(1)})
	if err != nil || page.Total != 2 || !page.HasMore || len(page.Machines) != 1 || page.Machines[0].ID != "pool/b" {
		t.Fatal(page, err)
	}
	page, err = filterMachinePage(machines, map[string]any{"search": "gpu a100", "limit": float64(1), "offset": float64(1)})
	if err != nil || page.HasMore || page.Total != 2 || page.Machines[0].ID != "pool/c" {
		t.Fatal(page, err)
	}
	for _, search := range []string{"BUILD.example", "training", "pool/b"} {
		page, err := filterMachinePage(machines, map[string]any{"search": search})
		if err != nil || len(page.Machines) != 1 {
			t.Fatal(search, page, err)
		}
	}
	page, err = filterMachinePage(machines, map[string]any{"offset": float64(100)})
	if err != nil || page.Machines == nil || len(page.Machines) != 0 || page.HasMore || page.Total != 3 {
		t.Fatal(page, err)
	}
	page, err = filterMachinePage(machines, nil)
	if err != nil || len(page.Machines) != 3 || page.Machines[0].ID != "a" {
		t.Fatal(page, err)
	}
}

func TestMachineSearchRejectsInvalidArguments(t *testing.T) {
	for _, arguments := range []map[string]any{
		{"limit": float64(0)}, {"limit": float64(101)}, {"limit": "2"},
		{"offset": float64(-1)}, {"offset": 1.5}, {"offset": float64(1e30)}, {"search": true},
	} {
		if _, err := filterMachinePage(nil, arguments); err == nil {
			t.Fatal("accepted invalid arguments", arguments)
		}
	}
}

func TestMachinePropertyCatalogIsSeparateSortedAndCountsMachines(t *testing.T) {
	machines := []domain.Machine{
		{ID: "a", Properties: []string{"gpu", "gpu", "", "a100"}},
		{ID: "pool/b", Properties: []string{"gpu", "cpu"}},
	}
	want := []machinePropertyCount{{"a100", 1}, {"cpu", 1}, {"gpu", 2}}
	if got := countMachineProperties(machines); !reflect.DeepEqual(got, want) {
		t.Fatal(got)
	}
	if got := countMachineProperties(nil); got == nil || len(got) != 0 {
		t.Fatal(got)
	}
	if policy, ok := toolPolicies["machine_properties"]; !ok || policy.Review != reviewRequired {
		t.Fatal("property catalog is not independently available")
	}
}

func TestMachineCatalogIncludesAssignedPoolMembersButNotOtherProjects(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, machine := range []domain.Machine{
		{ID: "direct", Properties: []string{"cpu"}},
		{ID: "foreign", Properties: []string{"private"}},
	} {
		if err := store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	pool := domain.Pool{ID: "pool", Machines: []domain.Machine{{ID: "gpu", Properties: []string{"cuda", "gpu"}}}}
	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project", MachineIDs: []string{"direct", "pool/gpu"}, PoolIDs: []string{"pool"}}
	if err := store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	engine := &Engine{Store: store, Remote: remote.NewManager(&remote.Connector{Store: store})}
	agent := domain.Agent{ProjectID: project.ID}
	page, err := engine.listProjectMachines(agent, map[string]any{"search": "CUDA"})
	if err != nil || page.Total != 1 || page.Machines[0].ID != "pool/gpu" {
		t.Fatal(page, err)
	}
	properties, err := engine.listMachineProperties(agent)
	if err != nil || !reflect.DeepEqual(properties, []machinePropertyCount{{"cpu", 1}, {"cuda", 1}, {"gpu", 1}}) {
		t.Fatal(properties, err)
	}
}
