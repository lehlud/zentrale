package webapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitConfigurationSecretsAndKeyUsages(t *testing.T) {
	fixture := newAPIFixture(t)
	for _, query := range []string{
		`query{gitConnections{id}}`,
		`query{gitRepositories(connectionId:"github"){items{id}}}`,
		`mutation{beginGitLogin(connectionId:"github"){id}}`,
		`mutation{configureGitConnection(input:{kind:"github",clientId:"app"}){id}}`,
		`mutation{revokeRepositoryKey(id:1)}`,
	} {
		if fixture.query(t, query, nil)["errors"] == nil {
			t.Fatalf("Git API allowed unauthenticated request: %s", query)
		}
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Agent",email:"agent@example.test"})}`, nil))
	value := requireData(t, fixture.query(t, `mutation{configureGitConnection(input:{kind:"gitlab",clientId:"own-app",clientSecret:"private-client-secret"}){id configured connected}}`, nil))
	if value["configureGitConnection"].(map[string]any)["configured"] != true {
		t.Fatal(value)
	}
	public := requireData(t, fixture.query(t, `query{gitConnections{id kind baseUrl clientId configured connected userName}}`, nil))
	encoded, _ := json.Marshal(public)
	if strings.Contains(string(encoded), "private-client-secret") {
		t.Fatal("secret in public account descriptor")
	}
	if fixture.query(t, `query{gitConnections{accessToken clientSecret}}`, nil)["errors"] == nil {
		t.Fatal("secret fields exist in output schema")
	}
	key := requireData(t, fixture.query(t, `mutation{generateSSHKey{id}}`, nil))["generateSSHKey"].(map[string]any)
	keyID := key["id"]
	uses := requireData(t, fixture.query(t, `query{sshKeys{id canDelete canRevoke usages{kind id name projectId}}}`, nil))["sshKeys"].([]any)
	if uses[0].(map[string]any)["canDelete"] != true {
		t.Fatal("unused key cannot be deleted")
	}
	requireData(t, fixture.query(t, `mutation($key:Int!){saveMachine(input:{id:"work",name:"Work",host:"localhost",user:"developer",keyId:$key}){id}}`, map[string]any{"key": keyID}))
	uses = requireData(t, fixture.query(t, `query{sshKeys{id purpose purposeId canDelete canRevoke usages{kind id name projectId}}}`, nil))["sshKeys"].([]any)
	used := uses[0].(map[string]any)
	if used["canDelete"] != false || used["canRevoke"] != false || used["purpose"] != "machine" || len(used["usages"].([]any)) != 1 {
		t.Fatal("key usage not reflected", used)
	}
	if fixture.query(t, `mutation($id:Int!){deleteSSHKey(id:$id)}`, map[string]any{"id": keyID})["errors"] == nil {
		t.Fatal("API deleted a used key")
	}
}
