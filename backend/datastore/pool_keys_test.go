package datastore

import (
	"bytes"
	"sync"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func poolKeyFixture(id string) domain.Pool {
	return domain.Pool{ID: id, Name: id, Machines: []domain.Machine{{ID: "a"}, {ID: "b"}}}
}

func TestManagedPoolAllocatesOneStableExclusiveKey(t *testing.T) {
	store := newKeyTestInstance(t)
	request := poolKeyFixture("pool")
	pool, err := store.SaveManagedPool(request)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := store.PrivateKey(pool.KeyID)
	if pool.KeyID <= 0 || len(key) == 0 || pool.Machines[0].KeyID != pool.KeyID || pool.Machines[1].KeyID != pool.KeyID || request.Machines[0].KeyID != 0 {
		t.Fatal("pool did not acquire a dedicated shared identity")
	}
	request.Machines = []domain.Machine{{ID: "b"}, {ID: "c"}, {ID: "a"}}
	updated, err := store.SaveManagedPool(request)
	if err != nil || updated.KeyID != pool.KeyID {
		t.Fatal("update rotated the key", err)
	}
	for _, machine := range updated.Machines {
		if machine.KeyID != pool.KeyID {
			t.Fatal("new or reordered member lost the pool identity")
		}
	}
	updatedKey, _ := store.PrivateKey(updated.KeyID)
	if !bytes.Equal(key, updatedKey) {
		t.Fatal("update changed key material")
	}
	other, err := store.SaveManagedPool(poolKeyFixture("other"))
	if err != nil || other.KeyID == pool.KeyID {
		t.Fatal("different pools shared an identity", err)
	}
	usages, _ := store.SSHKeyUsages()
	if len(usages[pool.KeyID]) != 1 || usages[pool.KeyID][0].Kind != "pool" || usages[pool.KeyID][0].ID != "pool" {
		t.Fatal("pool identity was recorded as unrelated per-host purposes", usages)
	}
	if err := store.DropPrivSSHKey(pool.KeyID); err == nil {
		t.Fatal("live pool key was deletable")
	}
	for _, kind := range []string{"machine", "source"} {
		if err := store.SaveWithSSHKeys(kind, "thief", "", map[string]any{"keyId": pool.KeyID}, SSHKeyClaim{KeyID: pool.KeyID, Purpose: kind, PurposeID: "thief"}); err == nil {
			t.Fatal("pool key was reused outside its pool", kind)
		}
	}
	request.KeyID = other.KeyID
	if _, err := store.SaveManagedPool(request); err == nil {
		t.Fatal("caller selected a pool key")
	}
	request.KeyID = 0
	request.Machines[0].KeyID = other.KeyID
	if _, err := store.SaveManagedPool(request); err == nil {
		t.Fatal("caller selected a member key")
	}
	keys, _ := store.ListPrivSSHKeys()
	if len(keys) != 2 {
		t.Fatal("update or rejected request allocated extra keys")
	}
	if err := store.Remove("pool", "pool"); err != nil {
		t.Fatal(err)
	}
	if err := store.DropPrivSSHKey(pool.KeyID); err != nil {
		t.Fatal("removed pool retained a key reference", err)
	}
}

func TestUnmanagedDevelopmentPoolDoesNotSilentlyAdoptOrRotateKeys(t *testing.T) {
	store := newKeyTestInstance(t)
	key, _ := store.GenerateSSHKey()
	old := poolKeyFixture("unmanaged")
	old.Machines[0].KeyID = key.ID
	if err := store.Save("pool", old.ID, "", old); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveManagedPool(poolKeyFixture("unmanaged")); err == nil {
		t.Fatal("unmanaged pool was silently converted")
	}
	var retained domain.Pool
	if err := store.Load("pool", old.ID, &retained); err != nil || retained.Machines[0].KeyID != key.ID {
		t.Fatal("unsupported configuration was changed", retained, err)
	}
	keys, err := store.ListPrivSSHKeys()
	if err != nil || len(keys) != 1 {
		t.Fatal("unsupported configuration allocated another key", keys, err)
	}
}

func TestMixedDevelopmentPoolDoesNotSilentlyReplaceMemberKeys(t *testing.T) {
	store := newKeyTestInstance(t)
	pool, err := store.SaveManagedPool(poolKeyFixture("mixed"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	pool.Machines[1].KeyID = other.ID
	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveManagedPool(poolKeyFixture(pool.ID)); err == nil {
		t.Fatal("old member access was silently replaced by the pool identity")
	}
	var retained domain.Pool
	if err := store.Load("pool", pool.ID, &retained); err != nil || retained.KeyID != pool.KeyID || retained.Machines[1].KeyID != other.ID {
		t.Fatal("unsupported mixed identities changed", err)
	}
	keys, err := store.ListPrivSSHKeys()
	if err != nil || len(keys) != 2 {
		t.Fatal("rejected update changed keys", err)
	}
}

func TestFailedPoolWriteRollsBackGeneratedKey(t *testing.T) {
	store := newKeyTestInstance(t)
	// Reads still succeed, but the final resource write fails after allocation.
	if _, err := store.db.Exec(`DROP TABLE resources CASCADE; CREATE VIEW resources AS SELECT ''::VARCHAR AS kind, ''::VARCHAR AS id, '{}'::JSON AS data WHERE false`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveManagedPool(poolKeyFixture("failed")); err == nil {
		t.Fatal("read-only resource accepted a pool write")
	}
	keys, err := store.ListPrivSSHKeys()
	if err != nil || len(keys) != 0 {
		t.Fatal("failed pool write orphaned a generated key", err)
	}
}

func TestConcurrentPoolImportsAllocateOneIdentity(t *testing.T) {
	store := newKeyTestInstance(t)
	request := poolKeyFixture("pool")
	var workers sync.WaitGroup
	keyIDs := make(chan int, 8)
	errors := make(chan error, 8)
	for range 8 {
		workers.Go(func() {
			pool, err := store.SaveManagedPool(request)
			keyIDs <- pool.KeyID
			errors <- err
		})
	}
	workers.Wait()
	close(keyIDs)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	keys, err := store.ListPrivSSHKeys()
	if err != nil || len(keys) != 1 {
		t.Fatal("concurrent imports allocated more than one key", err)
	}
	for id := range keyIDs {
		if id != keys[0].ID {
			t.Fatal("concurrent import selected another identity")
		}
	}
}
