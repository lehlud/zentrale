package webapi

import (
	"strings"
	"testing"
)

func TestPoolKeyIsGeneratedAndExposedOnlyAsPublicSetupData(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	yaml := "id: pool\nname: Pool\nmax_machines: 1\nshared_home: true\nmachines:\n  - id: first\n    name: First\n    host: first.example.org\n    user: developer\n  - id: second\n    name: Second\n    host: second.example.org\n    user: developer\n"
	mutation := `mutation($yaml:String!){savePool(yaml:$yaml){id keyId machines{id keyId}}}`
	pool := requireData(t, fixture.query(t, mutation, map[string]any{"yaml": yaml}))["savePool"].(map[string]any)
	keyID := pool["keyId"]
	for _, value := range pool["machines"].([]any) {
		if value.(map[string]any)["keyId"] != keyID {
			t.Fatal("pool members did not share the pool identity")
		}
	}
	keys := requireData(t, fixture.query(t, `query{sshKeys{id publicKey fingerprint purpose purposeId canDelete usages{kind id name}}}`, nil))["sshKeys"].([]any)
	if len(keys) != 1 {
		t.Fatal("pool creation did not generate exactly one key")
	}
	key := keys[0].(map[string]any)
	if key["id"] != keyID || key["purpose"] != "pool" || key["purposeId"] != "pool" || key["canDelete"] != false || !strings.HasPrefix(key["publicKey"].(string), "ecdsa-sha2-nistp521 ") {
		t.Fatal("public setup data or ownership was incorrect")
	}
	updated := requireData(t, fixture.query(t, mutation, map[string]any{"yaml": strings.Replace(yaml, "name: Pool", "name: Renamed pool", 1)}))["savePool"].(map[string]any)
	if updated["keyId"] != keyID {
		t.Fatal("HTTP reimport changed pool identity")
	}
	knownHosts := "@cert-authority *.example.org " + key["publicKey"].(string) + "\n"
	requireData(t, fixture.query(t, mutation, map[string]any{"yaml": yaml + "known_hosts: |\n  " + knownHosts}))
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
}
