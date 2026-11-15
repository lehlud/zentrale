package webapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/domain"
)

type oauthCatalogTransport func(*http.Request) (*http.Response, error)

func (transport oauthCatalogTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestBrowserCallbackContextRejectsOtherOriginsAndMalformedPaths(t *testing.T) {
	ctx := context.WithValue(context.Background(), requestKey{}, &requestState{
		authenticated: true, sessionHash: "owner", origin: "https://zentrale.example",
	})
	for _, raw := range []string{
		"https://other.example/oauth/callback",
		"https://zentrale.example.evil/oauth/callback",
		"https://user@zentrale.example/oauth/callback",
		"https://zentrale.example/oauth/callback?code=secret",
		"https://zentrale.example/oauth/callback#fragment",
		"https://zentrale.example/a/../oauth/callback",
		"https://zentrale.example/a/%2e%2e/oauth/callback",
	} {
		if _, err := browserLoginContext(ctx, raw); err == nil {
			t.Error("unsafe callback accepted", raw)
		}
	}

	if _, err := browserLoginContext(ctx, "https://zentrale.example/preview/project/oauth/callback"); err != nil {
		t.Fatal("preview callback prefix rejected", err)
	}
}

func TestGoogleGraphQLCallbackPersistsNoSecretsAndRejectsReplayOrLogout(t *testing.T) {
	for _, revokeSession := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "logout-during-exchange"}[revokeSession], func(t *testing.T) {
			fixture := newAPIFixture(t)
			requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
			applicationURL, _ := url.Parse(fixture.server.URL)
			owner := ""
			for _, cookie := range fixture.client.Jar.Cookies(applicationURL) {
				if cookie.Name == "zentrale_session" {
					owner = auth.TokenHash(cookie.Value)
				}
			}

			var exchanges atomic.Int32
			tokens := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				exchanges.Add(1)
				_ = request.ParseForm()
				if request.Form.Get("client_secret") != "private-app-secret" || request.Form.Get("code_verifier") == "" {
					t.Error("protected app credentials or PKCE missing")
				}

				if revokeSession {
					if err := fixture.engine.Store.DeleteSession(owner); err != nil {
						t.Error(err)
					}
				}

				_, _ = response.Write([]byte(`{"access_token":"private-access","refresh_token":"private-refresh","token_type":"Bearer","expires_in":3600}`))
			}))
			t.Cleanup(tokens.Close)
			fixture.engine.AccountLogin.GoogleTokenURL = tokens.URL
			catalogCalled := make(chan struct{}, 1)
			fixture.engine.Catalog.HTTP = &http.Client{Transport: oauthCatalogTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "Bearer private-access" || request.Header.Get("x-goog-user-project") != "api-project" || request.Header.Get("x-goog-api-key") != "" {
					t.Error("Google model catalog used API-key rather than OAuth/quota-project authentication")
				}

				select {
				case catalogCalled <- struct{}{}:
				default:
				}

				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[]}`))}, nil
			})}

			saved := requireData(t, fixture.query(t, `mutation($input:ProviderInput!){saveProvider(input:$input){id authMode}}`, map[string]any{
				"input": map[string]any{
					"id": "google-fixture", "name": "Google", "kind": "gemini", "authMode": "oauth",
					"oauthClientId": "own-web-app", "oauthProjectId": "api-project", "oauthClientSecret": "private-app-secret",
				},
			}))
			if saved["saveProvider"] == nil {
				t.Fatal("Google config was not saved")
			}

			begin := requireData(t, fixture.query(t, `mutation($uri:String!){beginProviderLogin(providerId:"google-fixture",redirectUri:$uri){id method verificationUrl}}`, map[string]any{"uri": fixture.server.URL + "/oauth/callback"}))
			login := begin["beginProviderLogin"].(map[string]any)
			verification, _ := url.Parse(login["verificationUrl"].(string))
			variables := map[string]any{"state": verification.Query().Get("state"), "code": "fixture-code"}
			mutation := `mutation($state:String!,$code:String!){completeBrowserLogin(state:$state,code:$code)}`
			result := fixture.query(t, mutation, variables)
			if revokeSession {
				if result["errors"] == nil {
					t.Fatal("logout during exchange accepted")
				}
			} else {
				if requireData(t, result)["completeBrowserLogin"] != true {
					t.Fatal("callback did not complete")
				}

				select {
				case <-catalogCalled:
				case <-time.After(5 * time.Second):
					t.Fatal("connected account did not discover models")
				}

				if fixture.query(t, mutation, variables)["errors"] == nil || exchanges.Load() != 1 {
					t.Fatal("authorization code replayed")
				}
			}

			var public domain.Provider
			if err := fixture.engine.Store.Load("provider", "google-fixture", &public); err != nil || public.OAuth != nil || public.OAuthClientSecret != "" || public.Connected == revokeSession {
				t.Fatal("unsafe or stale provider snapshot", err)
			}

			rows, err := fixture.engine.Store.List("provider_login", nil, 10, 0)
			encoded, marshalErr := json.Marshal(rows)
			if err != nil || marshalErr != nil || strings.Contains(string(encoded), "private-") || strings.Contains(string(encoded), "code_verifier") {
				t.Fatal("public login exposes credentials", err, marshalErr)
			}

			if revokeSession {
				return
			}

			// A name-only edit preserves the matching app/account but supersedes
			// an unfinished grant. Disconnect drops tokens; deletion also removes
			// the retained app secret and every pending private flow.
			pending := requireData(t, fixture.query(t, `mutation($uri:String!){beginProviderLogin(providerId:"google-fixture",redirectUri:$uri){id}}`, map[string]any{"uri": fixture.server.URL + "/oauth/callback"}))
			pendingID := pending["beginProviderLogin"].(map[string]any)["id"].(string)
			if _, err := fixture.engine.Store.Credential("google_login/" + pendingID); err != nil {
				t.Fatal("pending private flow missing", err)
			}

			edited := requireData(t, fixture.query(t, `mutation($input:ProviderInput!){saveProvider(input:$input){id connected}}`, map[string]any{
				"input": map[string]any{
					"id": "google-fixture", "name": "Renamed Google", "kind": "gemini", "authMode": "oauth",
					"oauthClientId": "own-web-app", "oauthProjectId": "api-project",
				},
			}))
			if edited["saveProvider"].(map[string]any)["connected"] != true {
				t.Fatal("name-only edit discarded the account")
			}

			if _, err := fixture.engine.Store.Credential("google_login/" + pendingID); err == nil {
				t.Fatal("configuration edit retained a superseded private flow")
			}

			requireData(t, fixture.query(t, `mutation{disconnectProvider(providerId:"google-fixture")}`, nil))
			private, err := fixture.engine.Store.Credential("google_provider/google-fixture")
			if err != nil || strings.Contains(string(private), "private-access") || strings.Contains(string(private), "private-refresh") || !strings.Contains(string(private), "private-app-secret") {
				t.Fatal("disconnect did not preserve only the app configuration", err)
			}

			requireData(t, fixture.query(t, `mutation{deleteResource(kind:"provider",id:"google-fixture")}`, nil))
			if _, err := fixture.engine.Store.Credential("google_provider/google-fixture"); err == nil {
				t.Fatal("deleted provider retained its private app secret")
			}
		})
	}
}
