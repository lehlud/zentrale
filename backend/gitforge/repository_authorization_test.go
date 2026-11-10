package gitforge

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestGitHubWorkflowConsentIsRequiredAndOldGrantsAreVisible(t *testing.T) {
	fixture := newForgeFixture(t, "github")
	fixture.connect(t)
	account, err := fixture.service.account("github")
	if err != nil {
		t.Fatal(err)
	}
	if account.Public.ReauthorizationRequired || !slices.Contains(account.Scopes, "workflow") {
		t.Fatal("workflow grant was not recorded", account.Public)
	}

	account.Scopes = nil // A connection created before workflow consent was requested.
	if err := fixture.service.save(account); err != nil {
		t.Fatal(err)
	}
	connections, err := fixture.service.Connections()
	if err != nil || !connections[0].Connected || !connections[0].ReauthorizationRequired {
		t.Fatal("existing Git access must stay available with a visible reauthorization action", connections, err)
	}

	for _, scope := range []string{"repo,read:org", ""} {
		fixture.tokenScopes = &scope
		login, err := fixture.service.Begin(context.Background(), "github")
		if err != nil {
			t.Fatal(err)
		}
		fixture.now = fixture.now.Add(5 * time.Second)
		login, err = fixture.service.Poll(context.Background(), "github", login.ID)
		if err != nil || login.Status != "failed" || login.Error == "" {
			t.Fatal("login reported success without workflow consent", login, err)
		}
	}

	fixture.tokenScopes = nil
	fixture.connect(t)
	connections, err = fixture.service.Connections()
	if err != nil || connections[0].ReauthorizationRequired {
		t.Fatal("successful consent did not clear the reconnect action", connections, err)
	}
	fixture.now = fixture.now.Add(70 * time.Second)
	if _, err := fixture.service.Token(context.Background(), "github"); err != nil {
		t.Fatal(err)
	}
	connections, err = fixture.service.Connections()
	if err != nil || connections[0].ReauthorizationRequired {
		t.Fatal("refresh response without scopes discarded the existing grant", connections, err)
	}
}

func TestGitHubKeyReauthorizationKeepsIdentityAndRecoversLostResponse(t *testing.T) {
	for _, loseResponse := range []bool{false, true} {
		name := "successful registration"
		if loseResponse {
			name = "lost registration response"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newForgeFixture(t, "github")
			fixture.connect(t)
			project, err := fixture.service.SaveProject(context.Background(), domain.Project{
				ID: "project", Directory: "/srv/project", GitConnectionID: "github", RepositoryID: "team/demo",
			})
			if err != nil {
				t.Fatal(err)
			}
			var source domain.Source
			if err := fixture.service.Store.Load("source", rootSourceID(project.ID), &source); err != nil {
				t.Fatal(err)
			}
			var binding repositoryBinding
			if err := fixture.service.Store.Load("repository_binding", source.BindingID, &binding); err != nil {
				t.Fatal(err)
			}
			originalKey, err := fixture.service.Store.PrivateKey(source.KeyID)
			if err != nil {
				t.Fatal(err)
			}
			originalRegistration := binding.DeployKeyID
			binding.Scopes = nil
			if err := fixture.service.Store.Save("repository_binding", binding.ID, "", binding); err != nil {
				t.Fatal(err)
			}
			account, err := fixture.service.account("github")
			if err != nil {
				t.Fatal(err)
			}
			account.Scopes = nil
			if err := fixture.service.save(account); err != nil {
				t.Fatal(err)
			}
			fixture.keys = append(fixture.keys, map[string]any{"id": 99, "key": "ssh-ed25519 UNRELATED", "read_only": false})
			if err := fixture.service.RefreshRepositoryAuthorization(context.Background(), binding.ID, source.URL); err != nil {
				t.Fatal(err)
			}
			if fixture.keyPosts != 1 || len(fixture.keys) != 2 {
				t.Fatal("keys changed before workflow consent")
			}

			fixture.connect(t)
			fixture.loseKeyResponse = loseResponse
			err = fixture.service.RefreshRepositoryAuthorization(context.Background(), binding.ID, source.URL)
			if (err != nil) != loseResponse {
				t.Fatal("unexpected registration result", err)
			}
			if err := fixture.service.RefreshRepositoryAuthorization(context.Background(), binding.ID, source.URL); err != nil {
				t.Fatal(err)
			}
			if fixture.keyPosts != 2 || len(fixture.keys) != 2 {
				t.Fatal("registration retry replaced unrelated keys or created duplicate keys", fixture.keyPosts, len(fixture.keys))
			}
			if err := fixture.service.Store.Load("repository_binding", binding.ID, &binding); err != nil {
				t.Fatal(err)
			}
			if binding.State != "active" || binding.DeployKeyID == originalRegistration ||
				binding.KeyID != source.KeyID || !slices.Contains(binding.Scopes, "workflow") {
				t.Fatal("new authorization was not recorded", binding)
			}
			currentKey, err := fixture.service.Store.PrivateKey(source.KeyID)
			if err != nil || !bytes.Equal(originalKey, currentKey) {
				t.Fatal("reauthorization changed the checkout's private identity", err)
			}
			if err := fixture.service.RefreshRepositoryAuthorization(context.Background(), binding.ID, "git@github.com:team/unrelated.git"); err == nil {
				t.Fatal("another repository was allowed to refresh this binding")
			}
		})
	}
}
