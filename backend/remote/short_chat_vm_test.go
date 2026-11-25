package remote

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestShortChatVMRequiresManagedRunnerAndRemovesMounts(t *testing.T) {
	for _, machine := range []domain.Machine{
		{Sandbox: SandboxDisabled},
		{Sandbox: "bwrap"},
		{Sandbox: SandboxMicroVM, MicroVMCommand: "/custom/runner"},
	} {
		if _, err := ShortChatMachine(machine); err == nil {
			t.Errorf("accepted a non-managed VM target: %+v", machine)
		}
	}

	original := domain.Machine{
		ID: "selected", Sandbox: SandboxAutomatic,
		GPUDevices: []string{"test"}, Properties: []string{"GPU"},
		Storage:       []domain.StorageVolume{{ID: "project"}},
		StorageMounts: []domain.StorageMount{{HostPath: "/private/project"}},
		QuotaCommand:  []string{"test"},
	}
	got, err := ShortChatMachine(original)
	if err != nil || got.ID != original.ID || got.Sandbox != SandboxMicroVM || GPUEnabled(got) ||
		len(got.Storage) != 0 || len(got.StorageMounts) != 0 || len(got.QuotaCommand) != 0 {
		t.Errorf("unsafe short-chat machine: %+v, error=%v", got, err)
	}
}
