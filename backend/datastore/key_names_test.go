package datastore

import (
	"bytes"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestSSHKeyRenamePersistsWithoutChangingUsedKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "bastion", KeyID: key.ID}
	if err := store.SaveWithSSHKeys("machine", machine.ID, "", machine, SSHKeyClaim{KeyID: key.ID, Purpose: "machine", PurposeID: machine.ID}); err != nil {
		t.Fatal(err)
	}
	if err := store.RenameSSHKey(key.ID, "  University login  "); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	names, err := store.SSHKeyNames()
	if err != nil || names[key.ID] != "University login" {
		t.Fatalf("label was not retained: %v %v", names, err)
	}
	data, err := store.PrivateKey(key.ID)
	if err != nil || !bytes.Equal(data, key.X509Data) {
		t.Fatal("renaming changed private key material")
	}
	usages, err := store.SSHKeyUsages()
	if err != nil || len(usages[key.ID]) != 1 || usages[key.ID][0].ID != machine.ID {
		t.Fatal("renaming changed key references")
	}
	if err := store.DropPrivSSHKey(key.ID); err == nil {
		t.Fatal("rename made an in-use key deletable")
	}
	for _, invalid := range []string{strings.Repeat("x", 201), "first\nsecond", "nul\x00name"} {
		if err := store.RenameSSHKey(key.ID, invalid); err == nil {
			t.Fatal("invalid label accepted")
		}
	}
	if err := store.RenameSSHKey(key.ID+1000, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown key accepted: %v", err)
	}
	if err := store.Remove("machine", machine.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DropPrivSSHKey(key.ID); err != nil {
		t.Fatal(err)
	}
	names, err = store.SSHKeyNames()
	if err != nil || len(names) != 0 {
		t.Fatal("deleted key retained a name")
	}
}
