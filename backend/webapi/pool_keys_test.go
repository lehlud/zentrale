package webapi

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func TestPoolKeyIsGeneratedAndExposedOnlyAsPublicSetupData(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`,
			nil,
		),
	)
	yaml := "id: pool\nname: Pool\nmax_machines: 1\nshared_home: true\nmachines:\n  - id: first\n    name: First\n    host: first.example.org\n    user: developer\n  - id: second\n    name: Second\n    host: second.example.org\n    user: developer\n"
	mutation := `mutation($yaml:String!){savePool(yaml:$yaml){id keyId machines{id keyId}}}`
	pool := requireData(t, fixture.query(t, mutation, map[string]any{
		"yaml": yaml,
	}))["savePool"].(map[string]any)

	keyID := pool["keyId"]

	for _, value := range pool["machines"].([]any) {
		if value.(map[string]any)["keyId"] != keyID {
			t.Fatal("pool members did not share the pool identity")
		}
	}

	keys := requireData(
		t,
		fixture.query(
			t,
			`query{sshKeys{id publicKey fingerprint purpose purposeId canDelete usages{kind id name}}}`,
			nil,
		),
	)["sshKeys"].([]any)
	if len(keys) != 1 {
		t.Fatal("pool creation did not generate exactly one key")
	}

	key := keys[0].(map[string]any)
	if key["id"] != keyID ||
		key["purpose"] != "pool" ||
		key["purposeId"] != "pool" ||
		key["canDelete"] != false ||
		!strings.HasPrefix(key["publicKey"].(string), "ecdsa-sha2-nistp521 ") {
		t.Fatal("public setup data or ownership was incorrect")
	}

	updated := requireData(
		t,
		fixture.query(
			t,
			mutation,
			map[string]any{
				"yaml": strings.Replace(yaml, "name: Pool", "name: Renamed pool", 1),
			},
		),
	)["savePool"].(map[string]any)
	if updated["keyId"] != keyID {
		t.Fatal("HTTP reimport changed pool identity")
	}

	knownHosts := "@cert-authority *.example.org " + key["publicKey"].(string) + "\n"
	configured := "# Shared pool for test runs\n" + yaml + "known_hosts: |\n  " + knownHosts
	requireData(t, fixture.query(t, mutation, map[string]any{
		"yaml": configured,
	}))
	listed := requireData(t, fixture.query(t, `query{pools{id knownHosts}}`, nil))["pools"].([]any)
	if len(listed) != 1 || listed[0].(map[string]any)["knownHosts"] != knownHosts {
		t.Fatal("pool CA was not persisted or exposed faithfully")
	}

	if fixture.query(t, mutation, map[string]any{"yaml": yaml + "known_hosts: invalid\n"})["errors"] == nil {
		t.Fatal("HTTP pool import accepted invalid host trust")
	}

	if fixture.query(t, mutation, map[string]any{"yaml": yaml + "key_id: 1\n"})["errors"] == nil {
		t.Fatal("HTTP pool import accepted a selected key")
	}

	if fixture.query(t, `query{sshKeys{x509Data}}`, nil)["errors"] == nil {
		t.Fatal("private key material entered the public schema")
	}

	readConfiguration := `query($id:String!){poolConfiguration(poolId:$id)}`
	variables := map[string]any{"id": "pool"}
	if got := requireData(t, fixture.query(t, readConfiguration, variables))["poolConfiguration"]; got != configured {
		t.Fatal("editor did not receive the original YAML")
	}
	edit := `mutation($id:String!,$yaml:String!){savePool(poolId:$id,yaml:$yaml){id name keyId}}`
	if response := fixture.query(t, edit, map[string]any{
		"id": "pool", "yaml": strings.Replace(configured, "id: pool", "id: another", 1),
	}); response["errors"] == nil {
		t.Fatal("edit accepted a different pool ID in the YAML")
	}
	if response := fixture.query(t, edit, map[string]any{
		"id": "missing", "yaml": strings.Replace(configured, "id: pool", "id: missing", 1),
	}); response["errors"] == nil {
		t.Fatal("edit recreated a missing pool")
	}
	editedYAML := strings.Replace(configured, "name: Pool", "name: Edited pool", 1)
	edited := requireData(t, fixture.query(t, edit, map[string]any{
		"id": "pool", "yaml": editedYAML,
	}))["savePool"].(map[string]any)
	if edited["id"] != "pool" || edited["name"] != "Edited pool" || edited["keyId"] != keyID {
		t.Fatalf("edit did not update the original pool in place: %v", edited)
	}
	if got := requireData(t, fixture.query(t, readConfiguration, variables))["poolConfiguration"]; got != editedYAML {
		t.Fatal("editor did not receive the last saved YAML")
	}

	// Pools saved before the editor, or rewritten after losing a member, have no YAML source.
	var stored domain.Pool
	if err := fixture.engine.Store.Load("pool", "pool", &stored); err != nil {
		t.Fatal(err)
	}
	if err := fixture.engine.Store.Save("pool", stored.ID, "", stored); err != nil {
		t.Fatal(err)
	}
	legacyYAML := requireData(t, fixture.query(t, readConfiguration, variables))["poolConfiguration"].(string)
	legacy, err := remote.ParsePool(legacyYAML)
	expected, expectedErr := remote.ParsePool(editedYAML)
	if err != nil || expectedErr != nil || !reflect.DeepEqual(legacy, expected) {
		t.Fatalf("reconstructed YAML changed the configuration: %v\n%s", err, legacyYAML)
	}
}
