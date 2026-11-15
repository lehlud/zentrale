package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
	"github.com/lehlud/zentrale/providerlogin"
)

func credentialTestEngine(t *testing.T) *Engine {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	return &Engine{Store: store}
}

func TestGoogleLoginStorageAndLegacyCleanup(t *testing.T) {
	engine := credentialTestEngine(t)
	for _, method := range []string{"oauth", "device", "browser"} {
		transaction := providerlogin.Transaction{
			Session: domain.ProviderLogin{ID: method, ProviderID: "provider-one", Method: method, Status: "pending"},
		}
		if method == "oauth" {
			transaction.BrowserFlow = &oauthflow.Pending{Verifier: "private-verifier", Owner: "private-session"}
		}

		if err := engine.saveProviderLogin(transaction); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := engine.Store.List("provider_login", nil, 10, 0)
	if err != nil || len(rows) != 3 {
		t.Fatal("missing public login snapshots", err)
	}

	for _, row := range rows {
		session, ok := row["session"].(map[string]any)
		if !ok || session["id"] == "" || row["id"] != nil {
			t.Fatalf("unexpected resource shape: %#v", row)
		}
	}

	encoded, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(encoded), "private-") {
		t.Fatal("private flow escaped to public resources", err)
	}

	var loaded providerlogin.Transaction
	if err := engine.loadProviderLogin("oauth", &loaded); err != nil || loaded.BrowserFlow == nil || loaded.BrowserFlow.Verifier != "private-verifier" {
		t.Fatal("encrypted flow unavailable", err)
	}

	if err := engine.clearProviderLogins("provider-one"); err != nil {
		t.Fatal("cleanup must also work for legacy logins without private records", err)
	}

	rows, err = engine.Store.List("provider_login", nil, 10, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("cleanup left public snapshots", err)
	}

	if _, err := engine.Store.Credential("google_login/oauth"); err == nil {
		t.Fatal("cleanup retained the private flow")
	}
}

func TestGoogleProviderStorageAndMissingRecordFailClosed(t *testing.T) {
	engine := credentialTestEngine(t)
	provider := domain.Provider{
		ID: "google-one", Kind: "gemini", AuthMode: "oauth", OAuthProjectID: "project-one",
		OAuthClientSecret: "private-app-secret",
		OAuth:             &domain.OAuthTokens{AccessToken: "private-access", RefreshToken: "private-refresh", ExpiresAt: time.Now().Add(time.Hour)},
	}
	if err := engine.saveProvider(provider); err != nil {
		t.Fatal(err)
	}

	var public domain.Provider
	if err := engine.Store.Load("provider", provider.ID, &public); err != nil {
		t.Fatal(err)
	}

	if public.OAuth != nil || public.OAuthClientSecret != "" || !public.Connected || public.AccountLabel != "project-one" {
		t.Fatal("unsafe or incorrect public provider projection")
	}

	var loaded domain.Provider
	if err := engine.loadProvider(provider.ID, &loaded); err != nil || loaded.OAuth == nil || loaded.OAuth.RefreshToken != "private-refresh" {
		t.Fatal("private provider unavailable", err)
	}

	if err := engine.Store.DeleteCredential("google_provider/" + provider.ID); err != nil {
		t.Fatal(err)
	}

	if err := engine.loadProvider(provider.ID, &loaded); err == nil {
		t.Fatal("missing private record did not fail closed")
	}
}
