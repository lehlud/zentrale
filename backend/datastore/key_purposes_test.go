package datastore

import (
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestSSHKeyPurposeAndDeletionGuard(t *testing.T) {
	store := newKeyTestInstance(t)
	key, _ := store.GenerateSSHKey()
	machine := domain.Machine{ID: "work", Name: "Work", KeyID: key.ID}
	claim := SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: machine.ID}
	if err := store.SaveWithSSHKeys("machine", machine.ID, "", machine, claim); err != nil {
		t.Fatal(err)
	}
	if err := store.DropPrivSSHKey(key.ID); err == nil {
		t.Fatal("used machine key can be deleted")
	}
	other := domain.Source{ID: "repo", Name: "Repo", KeyID: key.ID}
	if err := store.SaveWithSSHKeys("source", other.ID, "", other, SSHKeyClaim{KeyID: key.ID, Purpose: "source", PurposeID: other.ID}); err == nil {
		t.Fatal("machine key can be reused as repo key")
	}
	if err := store.SaveWithSSHKeys("machine", machine.ID, "", machine, claim); err != nil {
		t.Fatal("own purpose rejected", err)
	}
	if err := store.Remove("machine", machine.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DropPrivSSHKey(key.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPoolCannotAssignOneKeyToTwoMachines(t *testing.T) {
	store := newKeyTestInstance(t)
	key, _ := store.GenerateSSHKey()
	pool := domain.Pool{ID: "pool", Machines: []domain.Machine{{ID: "a", KeyID: key.ID}, {ID: "b", KeyID: key.ID}}}
	err := store.SaveWithSSHKeys("pool", pool.ID, "", pool,
		SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: "pool/a"},
		SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: "pool/b"})
	if err == nil {
		t.Fatal("pool shared a key between purposes")
	}
	if err := store.DropPrivSSHKey(key.ID); err != nil {
		t.Fatal("rolled-back claim left a reference", err)
	}
}

func TestOneKeyCannotGrandfatherMultiplePurposes(t *testing.T) {
	store := newKeyTestInstance(t)
	key, _ := store.GenerateSSHKey()
	pool := domain.Pool{ID: "unmanaged", Machines: []domain.Machine{{ID: "a", KeyID: key.ID}, {ID: "b", KeyID: key.ID}}}
	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWithSSHKeys("pool", pool.ID, "", pool, SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: "unmanaged/a"}, SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: "unmanaged/b"}); err == nil {
		t.Fatal("multiple historical purposes were accepted")
	}
}
