package gitforge

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
	"golang.org/x/crypto/ssh"
)

type browserForgeFixture struct {
	service         *Service
	connection      domain.GitConnection
	now             time.Time
	version         atomic.Value
	challenge       atomic.Value
	tokenPosts      atomic.Int32
	profileFailures atomic.Int32
	refreshDenied   atomic.Bool
	keys            atomic.Value
	createdPrivate  atomic.Bool
}

func newBrowserForgeFixture(t *testing.T, kind string) *browserForgeFixture {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	fixture := &browserForgeFixture{service: New(store), now: time.Now().UTC()}
	fixture.version.Store("1.24.0")
	fixture.keys.Store([]any{})
	fixture.service.Now = func() time.Time { return fixture.now }
	server := httptest.NewTLSServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	fixture.service.Client.ForgeHTTP = server.Client()
	fixture.connection, err = fixture.service.Configure(domain.GitConnectionConfig{
		Kind: kind, Name: "Private forge", BaseURL: server.URL, ClientID: "own-app", PrivateNetwork: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateSession("owner-session", fixture.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	return fixture
}

func (fixture *browserForgeFixture) serve(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	var result any
	switch request.URL.Path {
	case "/api/v1/version":
		result = map[string]any{"version": fixture.version.Load()}
	case "/login/oauth/access_token":
		fixture.tokenPosts.Add(1)
		_ = request.ParseForm()
		if request.Form.Get("grant_type") == "authorization_code" {
			digest := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(digest[:]) != fixture.challenge.Load() || request.Form.Get("code") != "fixture-code" {
				http.Error(response, "invalid verifier", http.StatusBadRequest)
				return
			}
		} else if fixture.refreshDenied.Load() {
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]any{"error": "invalid_grant"})
			return
		}

		result = map[string]any{"access_token": "fixture-access", "refresh_token": "fixture-refresh", "expires_in": 3600}
	case "/api/v1/user":
		if fixture.profileFailures.Add(-1) >= 0 {
			response.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		result = map[string]any{"login": "alice"}
	case "/api/v1/user/repos":
		result = []any{}
		if request.URL.Query().Get("page") == "1" {
			result = []any{map[string]any{
				"id": 42, "name": "demo", "full_name": "alice/demo",
				"ssh_url": "git@git.example:alice/demo.git", "html_url": "https://git.example/alice/demo",
				"permissions": map[string]any{"admin": true},
			}}
		}
	case "/api/v1/user/orgs":
		result = []any{}
		if request.URL.Query().Get("page") == "1" {
			result = []any{map[string]any{"username": "team"}}
		}
	case "/api/v1/orgs/team/repos":
		var input map[string]any
		_ = json.NewDecoder(request.Body).Decode(&input)
		fixture.createdPrivate.Store(input["private"] == true && input["auto_init"] == false)
		result = map[string]any{
			"id": 43, "name": "created", "full_name": "team/created",
			"ssh_url": "git@git.example:team/created.git", "html_url": "https://git.example/team/created",
			"permissions": map[string]any{"admin": true},
		}
	case "/api/v1/repos/team/created/keys":
		result = []any{}
		if request.Method == http.MethodPost {
			var input map[string]any
			_ = json.NewDecoder(request.Body).Decode(&input)
			result = map[string]any{"id": 99, "key": input["key"], "read_only": input["read_only"]}
			fixture.keys.Store([]any{result})
		} else if request.URL.Query().Get("page") == "1" {
			result = fixture.keys.Load()
		}
	case "/api/v1/repos/team/created/keys/99":
		fixture.keys.Store([]any{})
		response.WriteHeader(http.StatusNoContent)
		return
	default:
		http.NotFound(response, request)
		return
	}

	_ = json.NewEncoder(response).Encode(result)
}

func (fixture *browserForgeFixture) begin(t *testing.T) domain.GitLogin {
	t.Helper()
	ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{
		Owner: "owner-session", RedirectURI: "https://zentrale.example/oauth/callback",
	})
	login, err := fixture.service.Begin(ctx, fixture.connection.ID)
	if err != nil {
		t.Fatal(err)
	}

	address, err := url.Parse(login.VerificationURL)
	if err != nil || address.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("missing PKCE", err)
	}

	fixture.challenge.Store(address.Query().Get("code_challenge"))
	return login
}

func TestForgeBrowserSessionBindingRetryRefreshAndReplay(t *testing.T) {
	for _, kind := range []string{"gitea", "forgejo"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newBrowserForgeFixture(t, kind)
			login := fixture.begin(t)
			address, _ := url.Parse(login.VerificationURL)
			state := address.Query().Get("state")
			if (kind == "forgejo") != (address.Query().Get("scope") == "") {
				t.Fatal("incorrect scope contract")
			}

			_, err := fixture.service.CompleteBrowser(context.Background(), fixture.connection.ID, login.ID, state, "fixture-code", "other-session")
			if err == nil || fixture.tokenPosts.Load() != 0 {
				t.Fatal("wrong session reached exchange")
			}

			fixture.profileFailures.Store(1)
			pending, err := fixture.service.CompleteBrowser(context.Background(), fixture.connection.ID, login.ID, state, "fixture-code", "owner-session")
			if err == nil || pending.Status != "pending" || fixture.tokenPosts.Load() != 1 {
				t.Fatal("transient profile failure lost accepted token", err)
			}

			fixture.now = fixture.now.Add(6 * time.Second)
			connected, err := fixture.service.Poll(context.Background(), fixture.connection.ID, login.ID)
			if err != nil || connected.Status != "connected" || fixture.tokenPosts.Load() != 1 {
				t.Fatal("profile retry re-exchanged code or failed", err)
			}

			if _, err := fixture.service.CompleteBrowser(context.Background(), fixture.connection.ID, login.ID, state, "fixture-code", "owner-session"); err == nil {
				t.Fatal("consumed code was accepted again")
			}

			page, err := fixture.service.Repositories(context.Background(), fixture.connection.ID, "", 0)
			if err != nil || len(page.Items) != 1 || page.Items[0].FullName != "alice/demo" {
				t.Fatal("forge repository adapter failed", err)
			}

			fixture.now = fixture.now.Add(time.Hour)
			if _, err := fixture.service.Token(context.Background(), fixture.connection.ID); err != nil || fixture.tokenPosts.Load() != 2 {
				t.Fatal("refresh failed", err)
			}

			fixture.now = fixture.now.Add(time.Hour)
			fixture.refreshDenied.Store(true)
			if _, err := fixture.service.Token(context.Background(), fixture.connection.ID); err == nil {
				t.Fatal("revoked refresh accepted")
			}

			connections, err := fixture.service.Connections()
			if err != nil {
				t.Fatal(err)
			}

			for _, connection := range connections {
				if connection.ID == fixture.connection.ID && connection.Connected {
					t.Fatal("revoked account still appears connected")
				}
			}
		})
	}
}

func TestGiteaRejectsOldVersionAndPreservesDistinctInstances(t *testing.T) {
	fixture := newBrowserForgeFixture(t, "gitea")
	fixture.version.Store("1.22.6")
	ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{Owner: "owner-session", RedirectURI: "https://zentrale.example/oauth/callback"})
	if _, err := fixture.service.Begin(ctx, fixture.connection.ID); err == nil || !strings.Contains(err.Error(), "1.23") {
		t.Fatal("old Gitea can silently grant unrestricted OAuth access")
	}

	other, err := fixture.service.Configure(domain.GitConnectionConfig{
		Kind: "gitea", Name: "Work", BaseURL: "https://work.example", ClientID: "second-app",
	})
	if err != nil || other.ID == fixture.connection.ID || other.BaseURL == fixture.connection.BaseURL {
		t.Fatal("instances collapsed", err)
	}

	connections, err := fixture.service.Connections()
	if err != nil || len(connections) != 4 {
		t.Fatal("builtin or custom connections lost", err)
	}
}

func TestForgeOrganizationCreationAndWritableDeployKeyLifecycle(t *testing.T) {
	for _, kind := range []string{"gitea", "forgejo"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newBrowserForgeFixture(t, kind)
			login := fixture.begin(t)
			address, _ := url.Parse(login.VerificationURL)
			_, err := fixture.service.CompleteBrowser(context.Background(), fixture.connection.ID, login.ID,
				address.Query().Get("state"), "fixture-code", "owner-session")
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			repository, err := fixture.service.CreateRepository(ctx, domain.GitRepositoryInput{
				ConnectionID: fixture.connection.ID, NamespaceID: "team", Name: "created", Private: true,
			})
			if err != nil || repository.FullName != "team/created" || !fixture.createdPrivate.Load() {
				t.Fatal("private organization repository creation failed", err)
			}

			public, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}

			sshPublic, err := ssh.NewPublicKey(public)
			if err != nil {
				t.Fatal(err)
			}

			account, err := fixture.service.account(fixture.connection.ID)
			if err != nil {
				t.Fatal(err)
			}

			key, err := fixture.service.Client.createDeployKey(ctx, account, repository, string(ssh.MarshalAuthorizedKey(sshPublic)))
			if err != nil || !key.Writable {
				t.Fatal("forge did not confirm writable deploy key", err)
			}

			keys, err := fixture.service.Client.deployKeys(ctx, account, repository)
			if err != nil || len(keys) != 1 || keys[0].ID != key.ID || !keys[0].Writable {
				t.Fatal("deploy key discovery failed", err)
			}

			if err := fixture.service.Client.deleteDeployKey(ctx, account, repository, key.ID); err != nil {
				t.Fatal("forge deploy-key revocation failed", err)
			}

			keys, err = fixture.service.Client.deployKeys(ctx, account, repository)
			if err != nil || len(keys) != 0 {
				t.Fatal("revoked deploy key remained visible", err)
			}
		})
	}
}

func TestLegacyGitAccountMigratesToProtectedCredentialAndPublicSnapshot(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	legacy := account{
		Public: domain.GitConnection{ID: "gitlab", Kind: "gitlab", BaseURL: "https://gitlab.example", ClientID: "own-app", UserName: "alice"},
		Secret: "private-app", AccessToken: "private-access", RefreshToken: "private-refresh",
	}
	if err := store.Save("git_account", "gitlab", "", legacy); err != nil {
		t.Fatal(err)
	}

	service := New(store)
	var migrated account
	if err := service.loadAccount("gitlab", &migrated); err != nil || migrated.Secret != "private-app" || migrated.AccessToken != "private-access" {
		t.Fatal("migration lost private account data", err)
	}

	rows, err := store.List("git_account", nil, 10, 0)
	encoded, marshalErr := json.Marshal(rows)
	if err != nil || marshalErr != nil || strings.Contains(string(encoded), "private-") {
		t.Fatal("legacy credentials remained in public JSON", err, marshalErr)
	}

	if _, err := store.Credential("git_account/gitlab"); err != nil {
		t.Fatal("migration did not create a protected record", err)
	}
}
