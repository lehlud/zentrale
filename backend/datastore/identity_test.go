package datastore

import (
	"path/filepath"
	"sync"
	"testing"
)

func TestInstallationIdentityIsConcurrentPersistentAndPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.sqlite")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 12
	type identity struct{ id, secret string }
	results := make(chan identity, callers)
	var workers sync.WaitGroup
	for range callers {
		workers.Go(func() {
			id, secret, err := store.InstallationIdentity()
			if err != nil {
				t.Error(err)
				return
			}
			results <- identity{id, secret}
		})
	}
	workers.Wait()
	close(results)
	first := <-results
	if len(first.id) != 32 || len(first.secret) != 64 {
		t.Fatalf("invalid identity lengths")
	}
	for value := range results {
		if value != first {
			t.Fatal("concurrent callers obtained different daemon identities")
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	id, secret, err := reopened.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id != first.id || secret != first.secret {
		t.Fatal("daemon identity changed after restart")
	}
	other, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	id, secret, err = other.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id == first.id || secret == first.secret {
		t.Fatal("different installation reused daemon identity")
	}
}

func TestEvaluationCopyCannotResumeTheOriginalInstallation(t *testing.T) {
	store, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetSetting("installation-id", "original-installation"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCredential("installation/daemon-token", []byte("original-token")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetCredential("provider/account", []byte("retained-account")); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"agent", "job", "microvm", "job_stop", "vm_cleanup", "editor_attachment", "terminal_attachment", "provider_login"} {
		if err := store.Save(kind, kind, "project", map[string]string{"id": kind, "status": "running"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save("machine", "host", "", map[string]string{"id": "host"}); err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareEvaluationCopy(); err != nil {
		t.Fatal(err)
	}
	id, token, err := store.InstallationIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if id == "original-installation" || token == "original-token" {
		t.Fatal("evaluation can attach the original daemon")
	}
	account, err := store.Credential("provider/account")
	if err != nil || string(account) != "retained-account" {
		t.Fatal("provider account was lost", err)
	}
	for _, kind := range []string{"agent", "job", "microvm", "job_stop", "vm_cleanup", "editor_attachment", "terminal_attachment", "provider_login"} {
		rows, err := store.List(kind, nil, 10, 0)
		if err != nil || len(rows) != 0 {
			t.Fatalf("evaluation retained resumable %s: %v %v", kind, rows, err)
		}
	}
	if err := store.PrepareEvaluationCopy(); err == nil {
		t.Fatal("an already-opened installation could be reset")
	}
}
