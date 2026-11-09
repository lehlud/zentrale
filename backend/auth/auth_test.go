package auth

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/datastore"
)

func testStore(t *testing.T) *datastore.Instance {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func TestPasswordAndRevocableSessions(t *testing.T) {
	service, err := New(testStore(t), "")
	if err != nil {
		t.Fatal(err)
	}

	needsSetup, err := service.NeedsSetup()
	if err != nil || !needsSetup {
		t.Fatalf("fresh service: setup=%v error=%v", needsSetup, err)
	}

	if err := service.Setup("correct horse battery staple"); err != nil {
		t.Fatal(err)
	}

	if err := service.Setup("another password"); err == nil {
		t.Fatal("setup replaced the existing password")
	}

	if _, err := service.Login("wrong password", "first-client"); err == nil {
		t.Fatal("invalid password accepted")
	}

	token, err := service.Login("correct horse battery staple", "first-client")
	if err != nil || !service.Valid(token) {
		t.Fatalf("valid login: %v", err)
	}

	if service.Valid(token + "x") {
		t.Fatal("invalid session token accepted")
	}

	if err := service.Logout(token); err != nil {
		t.Fatal(err)
	}

	if service.Valid(token) {
		t.Fatal("revoked session remains valid")
	}
}

func TestEnvironmentHashSkipsSetupWithoutOverwritingExistingPassword(t *testing.T) {
	store := testStore(t)
	firstHash, err := Hash("first secret password")
	if err != nil {
		t.Fatal(err)
	}

	service, err := New(store, firstHash)
	if err != nil {
		t.Fatal(err)
	}

	if needsSetup, _ := service.NeedsSetup(); needsSetup {
		t.Fatal("environment hash did not initialize password")
	}

	replacement, err := Hash("replacement password")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := New(store, replacement); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Login("first secret password", "client"); err != nil {
		t.Fatal("environment overwrote established password")
	}

	if _, err := New(store, "$argon2id$v=19$m=4294967295,t=1,p=1$c2FsdA$YWJj"); err == nil {
		t.Fatal("unbounded Argon2 parameters accepted")
	}
}

func TestConcurrentSetupHasOneWinner(t *testing.T) {
	service, err := New(testStore(t), "")
	if err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	var successes atomic.Int32

	for range 4 {
		workers.Go(func() {
			if service.Setup("concurrent password") == nil {
				successes.Add(1)
			}
		})
	}

	workers.Wait()

	if successes.Load() != 1 {
		t.Fatalf("setup winners: %d", successes.Load())
	}
}

func TestHashValidationAndLoginLimit(t *testing.T) {
	for _, password := range []string{"short", strings.Repeat("x", 1025)} {
		if _, err := Hash(password); err == nil {
			t.Fatal("invalid password length accepted")
		}
	}

	for _, hash := range []string{
		"",
		"argon2id",
		"$argon2id$v=19$m=8,t=1,p=100$YWJjZGVmZ2g$YWJjZGVmZ2hpamtsbW5vcA",
		"$argon2id$v=19$m=8,t=1,t=2$YWJjZGVmZ2g$YWJjZGVmZ2hpamtsbW5vcA",
	} {
		if Verify(hash, "password") {
			t.Fatalf("invalid PHC accepted: %q", hash)
		}
	}

	service, err := New(testStore(t), "")
	if err != nil {
		t.Fatal(err)
	}

	for range 5 {
		_, _ = service.Login("wrong", "client")
	}

	if _, err := service.Login("wrong", "client"); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Fatalf("login limit: %v", err)
	}
}
