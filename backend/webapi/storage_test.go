package webapi

import (
	"strings"
	"testing"
)

func TestPoolStorageAndHostQuotaRoundTripThroughAPI(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"valid test password",commitIdentity:{name:"Test",email:"test@example.test"})}`,
			nil,
		),
	)
	yaml := `id: storage-pool
name: Storage
max_machines: 1
shared_home: true
sandbox: auto
storage:
  - id: large
    path: /proj/large/user
    shared: true
    description: Scratch without backup
quota_command: [/usr/bin/cip-quota]
machines:
  - id: worker
    name: Worker
    host: worker.example
    user: developer
`

	query := `mutation($yaml:String!){savePool(yaml:$yaml){id storage{id path shared description} quotaCommand machines{id storage{id path shared description} quotaCommand}}}`
	pool := requireData(t, fixture.query(t, query, map[string]any{"yaml": yaml}))["savePool"].(map[string]any)
	volumes := pool["storage"].([]any)
	member := pool["machines"].([]any)[0].(map[string]any)
	if len(volumes) != 1 ||
		volumes[0].(map[string]any)["description"] != "Scratch without backup" ||
		len(member["storage"].([]any)) != 1 ||
		member["quotaCommand"].([]any)[0] != "/usr/bin/cip-quota" {
		t.Fatal(pool)
	}

	if fixture.query(t, query, map[string]any{
		"yaml": strings.Replace(yaml, "/usr/bin/cip-quota", "cip-quota", 1),
	})["errors"] == nil {
		t.Fatal("API accepted an executable resolved through arbitrary host PATH")
	}
}
