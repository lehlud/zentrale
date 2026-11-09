package remote

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestPoolStorageAndQuotaConfiguration(t *testing.T) {
	configuration := strings.Replace(
		validPool,
		"sandbox: bwrap",
		"sandbox: auto\nquota_command: [/usr/bin/cip-quota]\nstorage:\n  - id: ciptmp\n    path: /proj/ciptmp/user\n    shared: true",
		1,
	)

	pool, err := ParsePool(configuration)
	if err != nil ||
		len(pool.Machines[0].Storage) != 1 ||
		pool.Machines[0].Storage[0].Path != "/proj/ciptmp/user" ||
		pool.Machines[0].QuotaCommand[0] != "/usr/bin/cip-quota" {
		t.Fatal(pool, err)
	}

	for _, invalid := range []string{
		strings.Replace(configuration, "/proj/ciptmp/user", "/proc/data", 1),
		strings.Replace(configuration, "/usr/bin/cip-quota", "cip-quota", 1),
		strings.Replace(configuration, "sandbox: auto", "sandbox: none", 1),
		strings.Replace(configuration, "id: ciptmp", "id: ../escape", 1),
		strings.Replace(configuration, "shared: true", "shared: true\n  - id: ciptmp\n    path: /another", 1),
	} {
		if _, err := ParsePool(invalid); err == nil {
			t.Fatal("accepted invalid storage configuration", invalid)
		}
	}

	copy := WithMachineDefaults(pool.Machines[0])
	copy.Storage[0].Path = "/different"
	copy.QuotaCommand[0] = "/different"

	if pool.Machines[0].Storage[0].Path != "/proj/ciptmp/user" ||
		pool.Machines[0].QuotaCommand[0] != "/usr/bin/cip-quota" {
		t.Fatal("defaults mutated shared configuration")
	}
}

func TestGPUJobReservationsSurviveHandoffAndRespectPoolLimit(t *testing.T) {
	manager := NewManager(nil)
	pool := domain.Pool{ID: "gpu", MaxMachines: 2}
	one := machineCandidate{id: "gpu/one", pool: &pool}
	two := machineCandidate{id: "gpu/two", pool: &pool}
	three := machineCandidate{id: "gpu/three", pool: &pool}

	if !manager.reserveJobCandidate(one, "launch-a", true) ||
		!manager.reserveJobCandidate(two, "launch-b", true) {
		t.Fatal("parallel GPU hosts not reserved")
	}

	manager.Hold("launch-a", "a")
	manager.Hold("launch-b", "b")
	manager.Release("launch-a")
	manager.Release("launch-b")

	if manager.reserveJobCandidate(one, "collision", true) ||
		manager.reserveJobCandidate(three, "over-limit", true) {
		t.Fatal("GPU exclusivity or pool limit bypassed")
	}

	manager.Release("job:a")

	if !manager.reserveJobCandidate(three, "replacement", true) {
		t.Fatal("completed job did not free pool capacity")
	}
}
