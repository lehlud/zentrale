package webapi

import (
	"fmt"
	"testing"
)

func TestJumpOnlyProjectValidationAndSSHKeyRenameAPI(t *testing.T) {
	fixture := newAPIFixture(t)
	if fixture.query(t, `mutation{renameSSHKey(id:1,name:"unauthorized")}`, nil)["errors"] == nil {
		t.Fatal("anonymous key rename accepted")
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	key := requireData(t, fixture.query(t, `mutation{generateSSHKey{id publicKey}}`, nil))["generateSSHKey"].(map[string]any)
	id := int(key["id"].(float64))
	machine := requireData(t, fixture.query(t, `mutation($input:MachineInput!){saveMachine(input:$input){id jumpHostOnly}}`, map[string]any{"input": map[string]any{"id": "bastion", "name": "Login", "host": "127.0.0.1", "user": "developer", "keyId": id, "jumpHostOnly": true}}))["saveMachine"].(map[string]any)
	if machine["jumpHostOnly"] != true {
		t.Fatal("jump-only role was not saved")
	}
	if fixture.query(t, `mutation{saveProject(input:{id:"invalid-target",name:"Invalid",machineIds:["bastion"]}){id}}`, nil)["errors"] == nil {
		t.Fatal("jump-only machine assigned as project worker")
	}
	requireData(t, fixture.query(t, `mutation($id:Int!){renameSSHKey(id:$id,name:"Login key")}`, map[string]any{"id": id}))
	keys := requireData(t, fixture.query(t, `query{sshKeys{id name publicKey purpose purposeId canDelete}}`, nil))["sshKeys"].([]any)
	for _, item := range keys {
		value := item.(map[string]any)
		if int(value["id"].(float64)) == id {
			if value["name"] != "Login key" || value["publicKey"] != key["publicKey"] || value["purposeId"] != "bastion" || value["canDelete"] != false {
				t.Fatal("rename changed key material or ownership", fmt.Sprint(value["name"], value["purposeId"], value["canDelete"]))
			}
			return
		}
	}
	t.Fatal("renamed key disappeared")
}
