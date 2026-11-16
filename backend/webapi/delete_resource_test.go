package webapi

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestDeleteResourceReportsFailureAndIgnoresMissingVMForLogicalDeletion(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	failed := fixture.query(t, `mutation{deleteResource(kind:"project",id:"missing")}`, nil)
	data, _ := failed["data"].(map[string]any)
	if failed["errors"] == nil || data["deleteResource"] == true {
		t.Fatal("failure presented as success", failed)
	}
	store := fixture.engine.Store
	if err := store.Save("project", "project", "", domain.Project{ID: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("agent", "main", "project", domain.Agent{ID: "main", ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save("microvm", "main:gone", "main", map[string]any{"agentId": "main", "projectId": "project", "machineId": "gone", "workspace": "/retained"}); err != nil {
		t.Fatal(err)
	}
	success := requireData(t, fixture.query(t, `mutation{deleteResource(kind:"agent",id:"main")}`, nil))
	if success["deleteResource"] != true {
		t.Fatal(success)
	}
}
