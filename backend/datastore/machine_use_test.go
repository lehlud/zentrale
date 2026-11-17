package datastore

import (
	"github.com/lehlud/zentrale/domain"
	"testing"
	"time"
)

func TestMachineUseIgnoresFinishedWorkAndExpiredTerminals(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		kind string
		data map[string]any
		want string
	}{
		{"project", map[string]any{"machineIds": []string{"host"}}, "project"},
		{"project", map[string]any{"poolIds": []string{"pool"}}, ""},
		{"job", map[string]any{"machineId": "host", "status": "running"}, "job"},
		{"job", map[string]any{"machineId": "host", "status": "stopping"}, "job"},
		{"job", map[string]any{"machineId": "host", "status": "completed"}, ""},
		{"editor_attachment", map[string]any{"machineId": "host"}, "editor"},
		{"terminal_attachment", map[string]any{"machineId": "host", "expiresAt": time.Now().Add(time.Minute)}, "terminal"},
		{"terminal_attachment", map[string]any{"machineId": "host", "expiresAt": time.Now().Add(-time.Minute)}, ""},
	} {
		if err := store.Save(test.kind, "use", "", test.data); err != nil {
			t.Fatal(err)
		}
		use, err := store.MachineUse("host")
		if err != nil || use != test.want {
			t.Fatalf("%s: got %q, want %q (%v)", test.kind, use, test.want, err)
		}
		roleErr := store.SaveWithSSHKeys("machine", "host", "", domain.Machine{ID: "host", KeyID: key.ID, JumpHostOnly: true}, SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: "host"})
		if (roleErr == nil) != (test.want == "") {
			t.Fatalf("role change bypassed %q: %v", test.want, roleErr)
		}
		other, err := store.MachineUse("unrelated")
		if err != nil || other != "" {
			t.Fatal("unrelated host was blocked")
		}
		if err := store.Remove(test.kind, "use"); err != nil {
			t.Fatal(err)
		}
	}
}
