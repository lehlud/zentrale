package providerlogin

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
)

func TestGoogleOwnAppPKCEOfflineExchangeAndRefresh(t *testing.T) {
	client := New()
	provider := domain.Provider{
		ID: "google-one", Kind: "gemini", AuthMode: "oauth",
		OAuthClientID: "own-web-app", OAuthClientSecret: "private-app-secret", OAuthProjectID: "api-project",
	}
	ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{
		Owner: "initiating-session", RedirectURI: "https://zentrale.example/oauth/callback",
	})
	transaction, err := client.Begin(ctx, provider)
	if err != nil {
		t.Fatal(err)
	}

	address, err := url.Parse(transaction.Session.VerificationURL)
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256([]byte(transaction.BrowserFlow.Verifier))
	parameters := address.Query()
	if address.Host != "accounts.google.com" || parameters.Get("client_id") != "own-web-app" ||
		parameters.Get("code_challenge_method") != "S256" || parameters.Get("access_type") != "offline" ||
		parameters.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) ||
		!strings.HasPrefix(parameters.Get("state"), "provider:"+transaction.Session.ID+":") ||
		!strings.Contains(parameters.Get("scope"), "generative-language.retriever") {
		t.Fatal("incorrect own-app Google authorization contract")
	}

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_ = request.ParseForm()
		if request.Form.Get("client_id") != provider.OAuthClientID || request.Form.Get("client_secret") != provider.OAuthClientSecret {
			t.Error("own app credentials missing")
		}

		result := map[string]any{"access_token": "private-access", "expires_in": 3600, "token_type": "Bearer"}
		if request.Form.Get("grant_type") == "authorization_code" {
			if request.Form.Get("code_verifier") != transaction.BrowserFlow.Verifier || request.Form.Get("redirect_uri") != transaction.BrowserFlow.RedirectURI {
				t.Error("PKCE exchange changed verifier or callback")
			}

			result["refresh_token"] = "private-refresh"
		} else if request.Form.Get("refresh_token") != "private-refresh" {
			t.Error("refresh did not use protected account token")
		}

		_ = json.NewEncoder(response).Encode(result)
	}))
	t.Cleanup(server.Close)
	client.GoogleTokenURL = server.URL
	provider.OAuth, err = client.ExchangeGoogle(ctx, provider, *transaction.BrowserFlow, "fixture-code")
	if err != nil {
		t.Fatal(err)
	}

	tokens, err := client.RefreshGoogle(ctx, provider)
	if err != nil || tokens.RefreshToken != "private-refresh" || tokens.AccountLabel != "api-project" || calls.Load() != 2 {
		t.Fatal("rotating/omitted refresh semantics incorrect", err)
	}
}

func TestGoogleTokenRedirectAndUpstreamErrorsNeverLeakCredentials(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
	t.Cleanup(target.Close)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/redirect" {
			http.Redirect(response, request, target.URL, http.StatusTemporaryRedirect)
			return
		}

		response.WriteHeader(http.StatusBadRequest)
		_, _ = response.Write([]byte(`{"error":"invalid_grant","error_description":"private-upstream-secret"}`))
	}))
	t.Cleanup(server.Close)
	client := New()
	provider := domain.Provider{OAuthClientSecret: "private-app", OAuth: &domain.OAuthTokens{RefreshToken: "private-refresh"}}

	client.GoogleTokenURL = server.URL + "/redirect"
	if _, err := client.RefreshGoogle(context.Background(), provider); err == nil || strings.Contains(err.Error(), "private-") || redirected.Load() != 0 {
		t.Fatal("token redirect followed or secret exposed", err)
	}

	client.GoogleTokenURL = server.URL
	if _, err := client.RefreshGoogle(context.Background(), provider); !errors.Is(err, ErrGoogleReauthorization) || strings.Contains(err.Error(), "private-") {
		t.Fatal("revocation was not safely distinguished", err)
	}
}
