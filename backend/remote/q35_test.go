package remote

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
)

func TestQ35GPUConfigurationAndPoolInheritance(t *testing.T) {
	base := domain.Machine{ID: "gpu", Name: "GPU", Host: "gpu.example", User: "user", Port: 22, KeyID: 1, Sandbox: SandboxAutomatic}
	base.GPUDevices = []string{"0000:0A:00.0", "0000:0A:00.1"}
	machine := WithMachineDefaults(base)
	if machine.VMType != VMTypeQ35 || machine.GPUDevices[0] != "0000:0a:00.0" || base.GPUDevices[0] != "0000:0A:00.0" {
		t.Fatal("GPU defaults mutated their input or failed to select q35", machine, base)
	}
	if err := ValidateMachine(machine); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*domain.Machine)
	}{
		{"unknown type", func(m *domain.Machine) { m.VMType = "arbitrary-qemu-option" }},
		{"mmio GPU", func(m *domain.Machine) { m.VMType = VMTypeMicroVM }},
		{"disabled sandbox", func(m *domain.Machine) { m.Sandbox = SandboxDisabled }},
		{"container fallback", func(m *domain.Machine) { m.Sandbox = SandboxBubblewrap }},
		{"custom runner", func(m *domain.Machine) { m.MicroVMCommand = "/opt/custom" }},
		{"injected option", func(m *domain.Machine) { m.GPUDevices = []string{"0000:01:00.0,romfile=evil"} }},
		{"bad slot", func(m *domain.Machine) { m.GPUDevices = []string{"0000:01:20.0"} }},
		{"duplicate function", func(m *domain.Machine) { m.GPUDevices = []string{"0000:01:00.0", "0000:01:00.0"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := machine
			test.edit(&invalid)
			if ValidateMachine(invalid) == nil {
				t.Fatal("invalid GPU configuration accepted", invalid)
			}
		})
	}
	configuration := strings.Replace(validPool, "sandbox: bwrap", "sandbox: auto\nvm_type: q35", 1)
	configuration += "    gpu_devices: [\"0000:01:00.0\", \"0000:01:00.1\"]\n"
	pool, err := ParsePool(configuration)
	if err != nil || pool.Machines[0].VMType != VMTypeQ35 || len(pool.Machines[0].GPUDevices) != 2 {
		t.Fatal(pool, err)
	}
	if _, err := ParsePool(strings.Replace(configuration, "vm_type: q35", "vm_type: microvm", 1)); err == nil {
		t.Fatal("pool inherited incompatible machine type")
	}
}

func TestExplicitQ35NeverFallsBackToHostExecution(t *testing.T) {
	var attempts atomic.Int32
	peer, err := testssh.StartWithCommandHandler(func(command string, _ io.Reader, _, stderr io.Writer) int {
		attempts.Add(1)
		if strings.Contains(command, "GPU_HOST_FALLBACK_SENTINEL") || strings.Contains(command, "systemd-run --user") {
			t.Error("GPU requirement weakened isolation")
		}
		_, _ = io.WriteString(stderr, "MicroVM unavailable: q35 missing")
		return 78
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, _ := DecodePublicKey(peer.PublicKey)
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := WithMachineDefaults(domain.Machine{ID: "gpu", Name: "GPU", Host: peer.Host, Port: peer.Port, User: "user", KeyID: key.ID, Sandbox: SandboxAutomatic, VMType: VMTypeQ35})
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	connector := &Connector{Store: store}
	connection, err := connector.Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	command, level, err := Sandbox(context.Background(), connection, machine, Execution{Workspace: "/project", Command: "GPU_HOST_FALLBACK_SENTINEL"})
	if err == nil || command != "" || level != "" || attempts.Load() != 1 || !strings.Contains(err.Error(), "q35 missing") {
		t.Fatal(command, level, err, attempts.Load())
	}
}

func TestVMConfigurationIdentityIncludesBackendAndDeviceAssignment(t *testing.T) {
	var original, q35, gpu map[string]any
	for _, test := range []struct {
		machine domain.Machine
		value   *map[string]any
	}{
		{domain.Machine{}, &original},
		{domain.Machine{VMType: VMTypeQ35}, &q35},
		{domain.Machine{VMType: VMTypeQ35, GPUDevices: []string{"0000:01:00.0"}}, &gpu},
	} {
		if err := json.Unmarshal([]byte(microVMConfiguration(test.machine)), test.value); err != nil {
			t.Fatal(err)
		}
	}
	if original["vmType"] != VMTypeMicroVM || q35["vmType"] != VMTypeQ35 || original["runnerHash"] == q35["runnerHash"] || q35["runnerHash"] == gpu["runnerHash"] {
		t.Fatal("different assignments could reuse the same live VM")
	}
	if len(gpu["gpuDevices"].([]any)) != 1 || gpu["vfio"] == "" {
		t.Fatal("GPU configuration or preflight implementation was dropped")
	}
}
