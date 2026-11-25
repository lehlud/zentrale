package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestAccountSessionsAndCredentials(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	service, err := New(store, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetupAccount("Alice", "first password", domain.CommitIdentity{Name: "Alice", Email: "alice@example.org"}); err != nil {
		t.Fatal(err)
	}
	adminToken, err := service.LoginAccount("alice", "first password", "alice")
	if err != nil {
		t.Fatal(err)
	}
	admin, ok := service.User(adminToken)
	if !ok || admin.ID != domain.InitialUserID || !admin.IsAdmin() {
		t.Fatalf("unexpected initial principal: %+v", admin)
	}
	bob, err := service.SaveAccount(admin.ID, domain.User{Username: "Bob"}, "bob's password", "")
	if err != nil || bob.Role != domain.RoleUser || bob.CanCreateProjects {
		t.Fatalf("unexpected new account: %+v (%v)", bob, err)
	}
	bobToken, err := service.LoginAccount("bob", "bob's password", "bob")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := service.User(bobToken); !ok || got.ID != bob.ID {
		t.Fatalf("session is not bound to Bob: %+v", got)
	}
	if _, err := service.SaveAccount(bob.ID, domain.User{Username: "Mallory"}, "another password", ""); err == nil {
		t.Fatal("regular user created an account")
	}
	if _, err := service.SaveAccount(admin.ID, bob, "new bob password", ""); err != nil || service.Valid(bobToken) {
		t.Fatalf("password reset did not revoke session: %v", err)
	}
	admin.Disabled = true
	if _, err := service.SaveAccount(admin.ID, admin, "", ""); err == nil {
		t.Fatal("disabled the initial admin")
	}
}

func TestLegacyAccountMigration(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const password = "legacy password"
	hash, err := Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting("login_argon2id", hash); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithIssuer(store, "", "https://login.example.org"); err == nil {
		t.Fatal("an OIDC identity could claim legacy password data before migration")
	}

	oldToken := strings.Repeat("x", 43)
	if err := store.CreateSession(TokenHash(oldToken), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	service, err := New(store, "")
	if err != nil {
		t.Fatal(err)
	}
	if service.Valid(oldToken) {
		t.Fatal("unbound legacy session still active")
	}
	if token, err := service.Login(password, "legacy"); err != nil || !service.Valid(token) {
		t.Fatalf("legacy password did not migrate: %v", err)
	}
	if _, err := NewWithIssuer(store, "", "https://login.example.org"); err == nil {
		t.Fatal("changed authentication mode on an existing installation")
	}
}

func TestFirstOIDCIdentityOwnsInitialAccount(t *testing.T) {
	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	const issuer = "https://login.example.org"
	if _, err := NewWithIssuer(store, "present", issuer); err == nil {
		t.Fatal("allowed both login modes")
	}
	service, err := NewWithIssuer(store, "", issuer)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Setup("local password"); err == nil {
		t.Fatal("allowed password setup in OIDC mode")
	}
	if _, err := service.Login("local password", "client"); err == nil {
		t.Fatal("allowed password login in OIDC mode")
	}
	token, err := service.LoginOIDC(issuer, "stable-subject", "Alice", "Alice Admin")
	if err != nil {
		t.Fatal(err)
	}
	admin, ok := service.User(token)
	if !ok || !admin.IsAdmin() || admin.Username != "alice" {
		t.Fatalf("unexpected initial OIDC principal: %+v", admin)
	}
	if _, err := service.LoginOIDC(issuer, "different-subject", "Alice", "Alice Admin"); err == nil {
		t.Fatal("matched an account by mutable username")
	}
	if _, err := service.LoginOIDC(issuer, "stable-subject", "new-name", "Alice Admin"); err != nil {
		t.Fatal(err)
	}
}
