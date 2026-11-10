package webapi

import (
	"testing"
)

func TestQ35GPUConfigurationRoundTripsThroughAPI(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"valid test password",commitIdentity:{name:"Test",email:"test@example.test"})}`,
			nil,
		),
	)
	key := requireData(t, fixture.query(t, `mutation{generateSSHKey{id}}`, nil))["generateSSHKey"].(map[string]any)
	input := map[string]any{
		"name":    "GPU",
		"host":    "gpu.example",
		"user":    "user",
		"keyId":   key["id"],
		"sandbox": "auto",
		"vmType":  "q35",
		"gpuDevices": []string{
			"0000:01:00.0",
			"0000:01:00.1",
		},
	}

	machine := requireData(
		t,
		fixture.query(
			t,
			`mutation($input:MachineInput!){saveMachine(input:$input){id vmType gpuDevices}}`,
			map[string]any{
				"input": input,
			},
		),
	)["saveMachine"].(map[string]any)
	if machine["vmType"] != "q35" || len(machine["gpuDevices"].([]any)) != 2 {
		t.Fatal(machine)
	}

	input["id"], input["vmType"] = machine["id"], "microvm"

	if fixture.query(
		t,
		`mutation($input:MachineInput!){saveMachine(input:$input){id}}`,
		map[string]any{
			"input": input,
		},
	)["errors"] == nil {
		t.Fatal("API accepted GPU assignment with incompatible machine type")
	}
}

func TestVMLifecycleHasNoManualStopMutation(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"valid test password",commitIdentity:{name:"Test",email:"test@example.test"})}`, nil))
	if fixture.query(t, `mutation{stopAgentVM(agentId:"vm-owner")}`, nil)["errors"] == nil {
		t.Fatal("VM lifecycle still requires manual stop controls")
	}
}
