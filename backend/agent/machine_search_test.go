package agent

import (
	"reflect"
	"testing"

	"github.com/lehlud/zentrale/domain"
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
	if _, ok := reviewedTools["machine_properties"]; !ok {
		t.Fatal("property catalog is not independently available")
	}
}
