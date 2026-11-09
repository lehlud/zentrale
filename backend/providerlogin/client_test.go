package providerlogin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestDeviceLoginPendingExchangeAndRefresh(t *testing.T) {
	polls := 0
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"account@example.test","https://api.openai.com/auth":{"chatgpt_account_id":"account-one"}}`))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			var body map[string]string
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["client_id"] != openAIClientID {
				t.Error("missing public OAuth client")
			}
			_, _ = writer.Write([]byte(`{"device_auth_id":"device-one","user_code":"ABCD-1234","interval":"5"}`))
		case "/api/accounts/deviceauth/token":
			polls++
			if polls == 1 {
				writer.WriteHeader(http.StatusForbidden)
			} else {
				_, _ = writer.Write([]byte(`{"authorization_code":"code-one","code_verifier":"verifier-one"}`))
			}
		case "/oauth/token":
			_ = request.ParseForm()
			switch request.Form.Get("grant_type") {
			case "authorization_code":
				if request.Form.Get("code_verifier") != "verifier-one" || request.Form.Get("code") != "code-one" {
					t.Error("authorization code/PKCE lost")
				}
				_ = json.NewEncoder(writer).Encode(map[string]any{"access_token": "access-one", "refresh_token": "refresh-one", "expires_in": 3600, "id_token": "header." + claims + ".signature"})
			case "refresh_token":
				if request.Form.Get("refresh_token") != "refresh-one" {
					t.Error("refresh token lost")
				}
				_, _ = writer.Write([]byte(`{"access_token":"access-two","expires_in":7200}`))
			default:
				t.Error("unexpected OAuth grant")
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := New()
	client.OpenAIAuthURL = server.URL
	transaction, err := client.Begin(context.Background(), domain.Provider{ID: "account", Kind: "openai", AuthMode: "device"})
	if err != nil {
		t.Fatal(err)
	}
	if transaction.Session.UserCode != "ABCD-1234" || transaction.Session.Interval != 5 || transaction.Session.VerificationURL != server.URL+"/codex/device" {
		t.Fatalf("device login: %+v", transaction.Session)
	}
	if tokens, pending, err := client.Poll(context.Background(), transaction); err != nil || !pending || tokens != nil {
		t.Fatalf("pending state: %v %v %v", tokens, pending, err)
	}
	tokens, pending, err := client.Poll(context.Background(), transaction)
	if err != nil || pending || tokens == nil {
		t.Fatalf("exchange: %v %v %v", tokens, pending, err)
	}
	if tokens.AccountID != "account-one" || tokens.AccountLabel != "account@example.test" {
		t.Fatalf("account metadata: %+v", tokens)
	}
	refreshed, err := client.Refresh(context.Background(), *tokens)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "access-two" || refreshed.RefreshToken != "refresh-one" || refreshed.AccountID != "account-one" {
		t.Fatalf("refresh: %+v", refreshed)
	}
	transaction.Session.ExpiresAt = time.Now().Add(-time.Minute)
	if _, _, err := client.Poll(context.Background(), transaction); err == nil {
		t.Fatal("expired device grant accepted")
	}
}

func TestBrowserLoginPKCEAndUnsupportedMethods(t *testing.T) {
	var verifier string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/auth/keys" {
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		var body map[string]string
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["code"] != "browser-code" || body["code_verifier"] != verifier || body["code_challenge_method"] != "S256" {
			t.Error("invalid PKCE exchange")
		}
		_, _ = writer.Write([]byte(`{"key":"account-key"}`))
	}))
	defer server.Close()
	client := New()
	client.OpenRouterURL = server.URL
	transaction, err := client.Begin(context.Background(), domain.Provider{ID: "account", Kind: "openrouter", AuthMode: "browser"})
	if err != nil {
		t.Fatal(err)
	}
	verifier = transaction.CodeVerifier
	address, _ := url.Parse(transaction.Session.VerificationURL)
	if address.Query().Get("code_challenge") != challenge(verifier) || len(verifier) < 43 {
		t.Fatal("authorization URL lost its PKCE challenge")
	}
	key, err := client.CompleteBrowser(context.Background(), transaction, "browser-code")
	if err != nil || key != "account-key" {
		t.Fatalf("browser exchange: %s %v", key, err)
	}
	if _, err := client.Begin(context.Background(), domain.Provider{Kind: "anthropic", AuthMode: "device"}); err == nil {
		t.Fatal("invented a provider login method")
	}
}
