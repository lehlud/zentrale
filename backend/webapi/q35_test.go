package webapi

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestQ35GPUConfigurationRoundTripsThroughAPI(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"valid test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	key := requireData(t, fixture.query(t, `mutation{generateSSHKey{id}}`, nil))["generateSSHKey"].(map[string]any)
	input := map[string]any{"name": "GPU", "host": "gpu.example", "user": "user", "keyId": key["id"], "sandbox": "auto", "vmType": "q35", "gpuDevices": []string{"0000:01:00.0", "0000:01:00.1"}}
	machine := requireData(t, fixture.query(t, `mutation($input:MachineInput!){saveMachine(input:$input){id vmType gpuDevices}}`, map[string]any{"input": input}))["saveMachine"].(map[string]any)
	if machine["vmType"] != "q35" || len(machine["gpuDevices"].([]any)) != 2 {
		t.Fatal(machine)
	}
	input["id"], input["vmType"] = machine["id"], "microvm"
	if fixture.query(t, `mutation($input:MachineInput!){saveMachine(input:$input){id}}`, map[string]any{"input": input})["errors"] == nil {
		t.Fatal("API accepted GPU assignment with incompatible machine type")
	}
}

func TestStopAgentVMMutationRetainsTheConversationAndRejectsBusyAgents(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"valid test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	record := domain.Agent{ID: "vm-owner", ProjectID: "project", Role: "main", Status: "idle"}
	if err := fixture.engine.Store.Save("agent", record.ID, record.ProjectID, record); err != nil {
		t.Fatal(err)
	}
	data := requireData(t, fixture.query(t, `mutation{stopAgentVM(agentId:"vm-owner")}`, nil))
	if data["stopAgentVM"] != true {
		t.Fatal(data)
	}
	if err := fixture.engine.Store.Load("agent", record.ID, &record); err != nil || record.Status != "idle" {
		t.Fatalf("stop changed conversation: %+v %v", record, err)
	}
	record.Status = "running"
	if err := fixture.engine.Store.Save("agent", record.ID, record.ProjectID, record); err != nil {
		t.Fatal(err)
	}
	if fixture.query(t, `mutation{stopAgentVM(agentId:"vm-owner")}`, nil)["errors"] == nil {
		t.Fatal("stopped an active agent's VM")
	}
}
