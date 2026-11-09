package gitforge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type forgeFixture struct {
	service         *Service
	kind            string
	now             time.Time
	keys            []map[string]any
	keyPosts        int
	tokenPosts      int
	refreshes       int
	profileFailures int
	loseKeyResponse bool
	oauthError      string
	createdOwner    string
}

func newForgeFixture(t *testing.T, kind string) *forgeFixture {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	fixture := &forgeFixture{service: New(store), kind: kind, now: time.Now().UTC(), keys: []map[string]any{}}
	fixture.service.Now = func() time.Time { return fixture.now }
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	fixture.service.Client.GitHubAuthURL, fixture.service.Client.GitHubAPIURL = server.URL, server.URL
	base := "https://github.com"

	if kind == "gitlab" {
		base = server.URL
	}

	_, err = fixture.service.Configure(domain.GitConnectionConfig{
		Kind:         kind,
		BaseURL:      base,
		ClientID:     "own-app",
		ClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	return fixture
}

func (fixture *forgeFixture) repository() map[string]any {
	if fixture.kind == "github" {
		return map[string]any{
			"id":        42,
			"name":      "demo",
			"full_name": "team/demo",
			"ssh_url":   "git@github.com:team/demo.git",
			"html_url":  "https://github.com/team/demo",
			"private":   true,
			"permissions": map[string]any{
				"admin": true,
			},
		}
	}

	return map[string]any{
		"id":                  42,
		"name":                "demo",
		"path_with_namespace": "team/demo",
		"ssh_url_to_repo":     "git@gitlab.com:team/demo.git",
		"web_url":             "https://gitlab.com/team/demo",
		"visibility":          "private",
		"permissions": map[string]any{
			"group_access": map[string]any{
				"access_level": 40,
			},
		},
	}
}

func (fixture *forgeFixture) serve(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	write := func(value any) { _ = json.NewEncoder(response).Encode(value) }
	path := strings.TrimPrefix(request.URL.Path, "/api/v4")

	switch {
	case path == "/login/device/code" || path == "/oauth/authorize_device":
		_ = request.ParseForm()
		expectedScope := "repo read:org"

		if fixture.kind == "gitlab" {
			expectedScope = "api"
		}

		if request.Form.Get("client_id") != "own-app" || request.Form.Get("scope") != expectedScope {
			http.Error(response, "wrong app/scopes", 400)
			return
		}

		write(map[string]any{
			"device_code":      "private-device",
			"user_code":        "ABCD-EFGH",
			"verification_uri": "http://" + request.Host + "/device",
			"expires_in":       600,
			"interval":         5,
		})
	case path == "/login/oauth/access_token" || path == "/oauth/token":
		_ = request.ParseForm()
		fixture.tokenPosts++

		if request.Form.Get("grant_type") == "refresh_token" {
			fixture.refreshes++

			if request.Form.Get("refresh_token") != "refresh-one" {
				http.Error(response, "wrong refresh token", 400)
				return
			}

			write(map[string]any{
				"access_token":  "access-two",
				"refresh_token": "refresh-two",
				"expires_in":    120,
			})

			return
		}

		if fixture.oauthError != "" {
			if fixture.kind == "gitlab" {
				response.WriteHeader(400)
			}

			write(map[string]any{"error": fixture.oauthError})

			return
		}

		write(map[string]any{"access_token": "access-one", "refresh_token": "refresh-one", "expires_in": 120})
	case path == "/user":
		if fixture.profileFailures > 0 {
			fixture.profileFailures--
			response.WriteHeader(503)
			write(map[string]any{"message": "try again"})

			return
		}

		write(map[string]any{"login": "gunter", "username": "gunter"})
	case path == "/user/orgs":
		write([]map[string]any{{"login": "team"}})
	case path == "/namespaces":
		write([]map[string]any{{"id": 7, "full_path": "team", "kind": "group"}})
	case request.Method == "POST" &&
		(path == "/orgs/team/repos" ||
			path == "/user/repos" ||
			path == "/projects"):
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)

		if body["name"] != "demo" {
			http.Error(response, "wrong name", 400)
			return
		}

		if fixture.kind == "github" && body["private"] != true {
			http.Error(response, "must stay private", 400)
			return
		}

		if fixture.kind == "gitlab" &&
			(body["visibility"] != "private" ||
				body["namespace_id"] != float64(7)) {
			http.Error(response, "wrong namespace/privacy", 400)
			return
		}

		fixture.createdOwner = path
		response.WriteHeader(201)
		write(fixture.repository())
	case path == "/user/repos" || path == "/projects":
		write([]map[string]any{fixture.repository()})
	case strings.HasSuffix(path, "/keys") || strings.HasSuffix(path, "/deploy_keys"):
		if request.Method == "GET" {
			write(fixture.keys)
			return
		}

		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		writable := fixture.kind == "github" &&
			body["read_only"] == false ||
			fixture.kind == "gitlab" &&
				body["can_push"] == true
		if !writable {
			http.Error(response, "write permission missing", 400)
			return
		}

		fixture.keyPosts++
		key := map[string]any{
			"id":        fixture.keyPosts,
			"key":       body["key"],
			"read_only": false,
			"can_push":  true,
		}

		fixture.keys = append(fixture.keys, key)

		if fixture.loseKeyResponse {
			fixture.loseKeyResponse = false
			response.WriteHeader(503)
			write(map[string]any{"message": "response lost"})

			return
		}

		response.WriteHeader(201)
		write(key)
	case request.Method == "DELETE" &&
		(strings.Contains(path, "/keys/") ||
			strings.Contains(path, "/deploy_keys/")):
		fixture.keys = []map[string]any{}
		response.WriteHeader(204)
	case path == "/repos/team/demo" || path == "/projects/42":
		write(fixture.repository())
	default:
		response.WriteHeader(404)
		write(map[string]any{"message": fmt.Sprintf("unexpected %s %s", request.Method, path)})
	}
}

func (fixture *forgeFixture) connect(t *testing.T) {
	t.Helper()
	login, err := fixture.service.Begin(context.Background(), fixture.kind)
	if err != nil {
		t.Fatal(err)
	}

	fixture.now = fixture.now.Add(5 * time.Second)
	result, err := fixture.service.Poll(context.Background(), fixture.kind, login.ID)
	if err != nil || result.Status != "connected" {
		t.Fatalf("connect: %+v, %v", result, err)
	}
}

func TestDeviceLoginPendingSlowDownRecoveryAndRefresh(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newForgeFixture(t, kind)
			fixture.oauthError = "authorization_pending"
			login, err := fixture.service.Begin(context.Background(), kind)
			if err != nil || login.UserCode == "" {
				t.Fatal(login, err)
			}

			_, _ = fixture.service.Poll(context.Background(), kind, login.ID)

			if fixture.tokenPosts != 0 {
				t.Fatal("polled before interval")
			}

			fixture.now = fixture.now.Add(5 * time.Second)
			pending, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || pending.Status != "pending" {
				t.Fatal(pending, err)
			}

			fixture.oauthError = "slow_down"
			fixture.now = fixture.now.Add(5 * time.Second)
			slow, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || slow.Interval != 10 {
				t.Fatal(slow, err)
			}

			fixture.oauthError, fixture.profileFailures = "", 1
			fixture.now = fixture.now.Add(10 * time.Second)

			if _, err := fixture.service.Poll(context.Background(), kind, login.ID); err == nil {
				t.Fatal("expected profile failure")
			}

			posts := fixture.tokenPosts
			fixture.now = fixture.now.Add(10 * time.Second)
			connected, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || connected.Status != "connected" || fixture.tokenPosts != posts {
				t.Fatal("consumed device code was retried", connected, err)
			}

			public, _ := fixture.service.Connections()
			data, _ := json.Marshal(public)
			if strings.Contains(string(data), "access-one") ||
				strings.Contains(string(data), "private-device") ||
				strings.Contains(string(data), "secret") {
				t.Fatal("credentials exposed")
			}

			fixture.now = fixture.now.Add(70 * time.Second)
			token, err := fixture.service.Token(context.Background(), kind)
			if err != nil || token != "access-two" || fixture.refreshes != 1 {
				t.Fatal(token, err)
			}

			if err := fixture.service.Disconnect(kind); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Token(context.Background(), kind); err == nil {
				t.Fatal("disconnected account still works")
			}
		})
	}
}

func TestRepositoryCreationAndSharedDeployKeyRecovery(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newForgeFixture(t, kind)
			fixture.connect(t)
			namespace := "team"

			if kind == "gitlab" {
				namespace = "7"
			}

			repository, err := fixture.service.CreateRepository(
				context.Background(),
				domain.GitRepositoryInput{
					ConnectionID: kind,
					NamespaceID:  namespace,
					Name:         "demo",
					Private:      true,
				},
			)
			if err != nil || fixture.createdOwner == "" {
				t.Fatal(repository, err)
			}

			project := domain.Project{
				ID:              "one",
				Name:            "One",
				Directory:       "/srv/one",
				GitConnectionID: kind,
				RepositoryID:    repository.ID,
			}

			fixture.loseKeyResponse = true

			if _, err := fixture.service.SaveProject(context.Background(), project); err == nil {
				t.Fatal("expected interrupted key registration")
			}

			keys, _ := fixture.service.Store.ListPrivSSHKeys()
			if len(keys) != 1 || fixture.keyPosts != 1 {
				t.Fatal("pending key not retained")
			}

			if err := fixture.service.Store.DropPrivSSHKey(keys[0].ID); err == nil {
				t.Fatal("pending remote key was deletable")
			}

			if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
				t.Fatal(err)
			}

			project.ID, project.Name = "two", "Two"

			if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
				t.Fatal(err)
			}

			if fixture.keyPosts != 1 {
				t.Fatal("shared repo created duplicate deploy key")
			}

			usages, _ := fixture.service.Store.SSHKeyUsages()
			if len(usages[keys[0].ID]) != 3 {
				t.Fatalf("expected remote binding and both project uses: %+v", usages)
			}

			if err := fixture.service.RevokeKey(context.Background(), keys[0].ID); err == nil {
				t.Fatal("in-use repo key was revoked")
			}

			for _, id := range []string{"one", "two"} {
				project.ID, project.GitConnectionID, project.RepositoryID = id, "", ""

				if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
					t.Fatal(err)
				}
			}

			if err := fixture.service.RevokeKey(context.Background(), keys[0].ID); err != nil {
				t.Fatal(err)
			}

			keys, _ = fixture.service.Store.ListPrivSSHKeys()
			if len(keys) != 0 || len(fixture.keys) != 0 {
				t.Fatal("key revocation did not remove local and remote key")
			}
		})
	}
}

func TestRepositoryDoesNotReassignAnUnmanagedSourceKey(t *testing.T) {
	fixture := newForgeFixture(t, "github")
	fixture.connect(t)
	key, _ := fixture.service.Store.GenerateSSHKey()
	public, _ := remote.PublicKey(key.X509Data)
	fixture.keys = []map[string]any{{"id": 99, "key": public, "read_only": false}}
	source := domain.Source{
		ID:        "manual",
		ProjectID: "old",
		Name:      "demo",
		KeyID:     key.ID,
		URL:       "ssh://git@github.com/team/demo.git",
	}
	if err := fixture.service.Store.SaveWithSSHKeys(
		"source",
		source.ID,
		source.ProjectID,
		source,
		datastore.SSHKeyClaim{
			KeyID:     key.ID,
			Purpose:   "source",
			PurposeID: source.ID,
		},
	); err != nil {
		t.Fatal(err)
	}

	project := domain.Project{
		ID:              "new",
		Name:            "New",
		Directory:       "/srv/new",
		GitConnectionID: "github",
		RepositoryID:    "team/demo",
	}
	if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}

	keys, _ := fixture.service.Store.ListPrivSSHKeys()
	if len(keys) != 2 || fixture.keyPosts != 1 {
		t.Fatal("repository did not allocate its own purpose-bound key")
	}

	var adopted domain.Source
	_ = fixture.service.Store.Load("source", "manual", &adopted)

	if adopted.BindingID != "" {
		t.Fatal("unmanaged source reference was silently converted")
	}
}

func TestProjectSettingsCanBeEditedAfterGitDisconnect(t *testing.T) {
	fixture := newForgeFixture(t, "github")
	fixture.connect(t)
	project := domain.Project{
		ID:              "one",
		Name:            "One",
		Directory:       "/srv/one",
		GitConnectionID: "github",
		RepositoryID:    "team/demo",
	}
	if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
		t.Fatal(err)
	}

	if err := fixture.service.Disconnect("github"); err != nil {
		t.Fatal(err)
	}

	project.Name = "Renamed"

	if _, err := fixture.service.SaveProject(context.Background(), project); err != nil {
		t.Fatal("unrelated setting requires Git login", err)
	}
}

func TestDeviceLoginDenialExpiryAndSupersededLogin(t *testing.T) {
	for _, kind := range []string{"github", "gitlab"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newForgeFixture(t, kind)
			fixture.oauthError = "access_denied"
			login, err := fixture.service.Begin(context.Background(), kind)
			if err != nil {
				t.Fatal(err)
			}

			fixture.now = fixture.now.Add(5 * time.Second)
			denied, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || denied.Status != "failed" {
				t.Fatal(denied, err)
			}

			if _, err := fixture.service.Token(context.Background(), kind); err == nil {
				t.Fatal("denied login produced a usable token")
			}

			login, err = fixture.service.Begin(context.Background(), kind)
			if err != nil {
				t.Fatal(err)
			}

			fixture.now = fixture.now.Add(601 * time.Second)
			calls := fixture.tokenPosts
			expired, err := fixture.service.Poll(context.Background(), kind, login.ID)
			if err != nil || expired.Status != "expired" || fixture.tokenPosts != calls {
				t.Fatal("expired code was polled", expired, err)
			}

			if _, err := fixture.service.Begin(context.Background(), kind); err != nil {
				t.Fatal(err)
			}

			if _, err := fixture.service.Poll(context.Background(), kind, login.ID); err == nil {
				t.Fatal("old login was accepted")
			}
		})
	}
}
