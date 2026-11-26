package webapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/auth/oidc"
	"github.com/lehlud/zentrale/datastore"
)

func TestOIDCInstallationRedirectsDirectlyAndDoesNotOfferPasswordLogin(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}

		issuer := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
			"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	}))
	t.Cleanup(idp.Close)

	store, err := datastore.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	authentication, err := auth.NewWithIssuer(store, "", idp.URL)
	if err != nil {
		t.Fatal(err)
	}
	const redirect = "http://127.0.0.1" + OIDCCallbackPath
	login, err := oidc.New(context.Background(), oidc.Config{
		Issuer: idp.URL, ClientID: "test-client", ClientSecret: "test-secret", RedirectURL: redirect,
	})
	if err != nil {
		t.Fatal(err)
	}

	frontend := t.TempDir()
	const index = "<!doctype html><title>Zentrale</title>"
	if err := os.WriteFile(filepath.Join(frontend, "index.html"), []byte(index), 0600); err != nil {
		t.Fatal(err)
	}
	api := &Server{Store: store, Auth: authentication, OIDC: login, Frontend: frontend}
	handler, err := api.Handler()
	if err != nil {
		t.Fatal(err)
	}

	guest := httptest.NewRecorder()
	handler.ServeHTTP(guest, httptest.NewRequest(http.MethodGet, "/", nil))
	location, err := url.Parse(guest.Header().Get("Location"))
	if err != nil || guest.Code != http.StatusFound || location == nil ||
		!strings.HasPrefix(location.String(), idp.URL+"/authorize?") {
		t.Fatalf("first request did not redirect directly to the provider: %d %v (%v)", guest.Code, location, err)
	}
	query := location.Query()
	if query.Get("state") == "" || query.Get("nonce") == "" || query.Get("redirect_uri") != redirect ||
		query.Get("code_challenge_method") != "S256" || strings.Contains(guest.Body.String(), index) {
		t.Fatalf("unexpected initial login redirect: %s", location)
	}

	token, err := authentication.LoginOIDC(idp.URL, "first", "alice", "Alice")
	if err != nil {
		t.Fatal(err)
	}
	appRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	appRequest.AddCookie(&http.Cookie{Name: "zentrale_session", Value: token})
	signedIn := httptest.NewRecorder()
	handler.ServeHTTP(signedIn, appRequest)
	if signedIn.Code != http.StatusOK || signedIn.Body.String() != index {
		t.Fatalf("OIDC session did not open the app: %d %q", signedIn.Code, signedIn.Body.String())
	}

	for _, mutation := range []string{
		`mutation{login(password:"local password")}`,
		`mutation{setup(password:"local password",commitIdentity:{name:"Alice",email:"alice@example.org"})}`,
	} {
		body, err := json.Marshal(map[string]string{"query": mutation})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, req)
		if !strings.Contains(result.Body.String(), "disabled") {
			t.Fatalf("local authentication route was not disabled: %s", result.Body.String())
		}
	}
}
