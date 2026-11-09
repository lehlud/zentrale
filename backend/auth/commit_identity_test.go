package auth

import (
	"encoding/json"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestSetupStoresPasswordAndCommitIdentityTogether(t *testing.T) {
	store := testStore(t)
	service, err := New(store, "")
	if err != nil {
		t.Fatal(err)
	}

	invalid := domain.CommitIdentity{Name: "Team", Email: "not an email"}
	if service.SetupWithIdentity("correct setup password", invalid) == nil {
		t.Fatal("invalid identity completed setup")
	}

	if needs, _ := service.NeedsSetup(); !needs {
		t.Fatal("invalid identity consumed initial setup")
	}

	if err := service.SetupWithIdentity(
		"correct setup password",
		domain.CommitIdentity{
			Name:  "  Test Team  ",
			Email: "  team@example.org  ",
		},
	); err != nil {
		t.Fatal(err)
	}

	if _, err := service.Login("correct setup password", "client"); err != nil {
		t.Fatal(err)
	}

	if err := service.SetupWithIdentity(
		"replacement password",
		domain.CommitIdentity{
			Name:  "Replacement",
			Email: "replacement@example.org",
		},
	); err == nil {
		t.Fatal("second setup replaced the identity")
	}

	encoded, err := store.Setting("commit_identity")
	if err != nil {
		t.Fatal(err)
	}

	var identity domain.CommitIdentity
	if err := json.Unmarshal([]byte(encoded), &identity); err != nil {
		t.Fatal(err)
	}

	if identity.Name != "Test Team" || identity.Email != "team@example.org" {
		t.Fatalf("saved identity: %+v", identity)
	}
}
