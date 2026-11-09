package webapi

import (
	"fmt"
	"testing"
)

func TestPoolConnectionMutationIsAuthenticatedAndChecksSampledMembers(t *testing.T) {
	fixture := newAPIFixture(t)
	mutation := `mutation($id:String!){testPool(id:$id){poolId totalMachines machines{machineId name host port connected error hostKey{publicKey} sandbox{machineId level}}}}`
	if fixture.query(t, mutation, map[string]any{"id": "pool"})["errors"] == nil {
		t.Fatal("unauthenticated pool check accepted")
	}
	requireData(t, fixture.query(t,
		`mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	yaml := "id: pool\nname: Pool\nmax_machines: 1\nshared_home: true\nsandbox: none\nmachines:\n"
	for index := range 40 {
		yaml += fmt.Sprintf("  - id: worker-%d\n    name: Worker %d\n    host: %s\n    port: %d\n    user: test\n", index, index, fixture.ssh.Host, fixture.ssh.Port)
	}
	requireData(t, fixture.query(t, `mutation($yaml:String!){savePool(yaml:$yaml){id}}`, map[string]any{"yaml": yaml}))

	result := requireData(t, fixture.query(t, mutation, map[string]any{"id": "pool"}))["testPool"].(map[string]any)
	members := result["machines"].([]any)
	if result["poolId"] != "pool" || result["totalMachines"] != float64(40) || len(members) != 4 {
		t.Fatalf("incorrect HTTP sample: %+v", result)
	}

	seen := make(map[string]bool)
	for _, value := range members {
		member := value.(map[string]any)
		id := member["machineId"].(string)
		check := member
		if seen[id] || check["connected"] != true || check["error"] != "" {
			t.Fatalf("invalid member result: %+v", member)
		}
		seen[id] = true
		if check["sandbox"].(map[string]any)["machineId"] != id {
			t.Fatalf("member sandbox identity lost: %+v", member)
		}
	}

	if fixture.query(t, mutation, map[string]any{"id": "missing"})["errors"] == nil {
		t.Fatal("missing pool reported a successful check")
	}
}
