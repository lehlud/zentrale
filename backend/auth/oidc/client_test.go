package oidc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"golang.org/x/oauth2"
)

const (
	testClientID     = "zentrale-client"
	testClientSecret = "zentrale-secret"
	testRedirect     = "https://zentrale.test/auth/oidc/callback"
)

type mockProvider struct {
	config        Config
	ctx           context.Context
	grants        sync.Map
	exchangeCount atomic.Int32
}

type mockGrant struct {
	challenge string
	claims    map[string]any
	wrongKey  bool
}

func newMockProvider(t *testing.T, metadataOverride map[string]any) *mockProvider {
	t.Helper()
	trusted := newMockSigningKey(t)
	untrusted := newMockSigningKey(t)
	m := &mockProvider{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tenant/token" {
			m.token(w, r, trusted, untrusted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tenant/.well-known/openid-configuration":
			issuer := "https://" + r.Host + "/tenant"
			metadata := map[string]any{
				"issuer":                                issuer,
				"authorization_endpoint":                issuer + "/authorize",
				"token_endpoint":                        issuer + "/token",
				"jwks_uri":                              issuer + "/jwks",
				"id_token_signing_alg_values_supported": []string{"ES256"},
			}
			for name, value := range metadataOverride {
				metadata[name] = value
			}
			_ = json.NewEncoder(w).Encode(metadata)
		case "/tenant/jwks":
			public := jose.JSONWebKey{Key: &trusted.PublicKey, KeyID: "test-key", Algorithm: "ES256", Use: "sig"}
			_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{public}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	m.ctx = context.WithValue(t.Context(), oauth2.HTTPClient, server.Client())
	m.config = Config{Issuer: server.URL + "/tenant", ClientID: testClientID, ClientSecret: testClientSecret, RedirectURL: testRedirect}
	return m
}

func newMockSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func (m *mockProvider) token(w http.ResponseWriter, r *http.Request, trusted, untrusted *ecdsa.PrivateKey) {
	m.exchangeCount.Add(1)
	clientID, secret, _ := r.BasicAuth()
	if clientID == "" {
		clientID, secret = r.FormValue("client_id"), r.FormValue("client_secret")
	}
	if r.Method != http.MethodPost || clientID != testClientID || secret != testClientSecret ||
		r.FormValue("grant_type") != "authorization_code" || r.FormValue("redirect_uri") != testRedirect {
		http.Error(w, "invalid code exchange", http.StatusBadRequest)
		return
	}

	value, ok := m.grants.LoadAndDelete(r.FormValue("code"))
	if !ok {
		http.Error(w, "provider-only diagnostics", http.StatusBadRequest)
		return
	}
	grant := value.(mockGrant)
	verifier := r.FormValue("code_verifier")
	digest := sha256.Sum256([]byte(verifier))
	if len(verifier) < 43 || base64.RawURLEncoding.EncodeToString(digest[:]) != grant.challenge {
		http.Error(w, "invalid verifier", http.StatusBadRequest)
		return
	}

	response := map[string]any{"access_token": "not used", "token_type": "Bearer"}
	if grant.claims != nil {
		key := trusted
		if grant.wrongKey {
			key = untrusted
		}
		idToken, err := signMockToken(key, grant.claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		response["id_token"] = idToken
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func signMockToken(key *ecdsa.PrivateKey, claims map[string]any) (string, error) {
	signer, err := jose.NewSigner(jose.SigningKey{
		Algorithm: jose.ES256,
		Key:       jose.JSONWebKey{Key: key, KeyID: "test-key"},
	}, nil)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return signed.CompactSerialize()
}

func startTestFlow(t *testing.T, client *Client) (url.Values, *http.Cookie) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "https://zentrale.test/auth/oidc/start", nil)
	if err := client.Start(response, request); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusFound || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected start response: %d %v", response.Code, response.Header())
	}

	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := location.Query()
	if location.Scheme+"://"+location.Host+location.Path != client.issuer+"/authorize" ||
		query.Get("client_id") != testClientID || query.Get("response_type") != "code" ||
		query.Get("redirect_uri") != testRedirect || query.Get("scope") != "openid profile" ||
		query.Get("client_secret") != "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected authorization redirect: %v", location)
	}
	for _, name := range []string{"state", "nonce", "code_challenge"} {
		if len(query.Get(name)) != flowTokenLen {
			t.Errorf("authorization redirect has invalid %s", name)
		}
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one flow cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
		!strings.HasPrefix(cookie.Name, "__Host-") || cookie.Path != "/" || cookie.Domain != "" ||
		cookie.MaxAge != int(flowTTL/time.Second) || strings.Contains(location.RawQuery, cookie.Value) {
		t.Fatalf("unexpected flow cookie attributes: %v", cookie)
	}
	return query, cookie
}

func callTestCallback(client *Client, query url.Values, cookies ...*http.Cookie) (Identity, error, *httptest.ResponseRecorder) {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, testRedirect+"?"+query.Encode(), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	identity, err := client.Callback(response, request)
	return identity, err, response
}

func (m *mockProvider) issueGrant(authQuery url.Values, change map[string]any, omitIDToken, wrongKey bool) url.Values {
	var claims map[string]any
	if !omitIDToken {
		claims = map[string]any{
			"iss": m.config.Issuer, "sub": "stable-user-id", "aud": testClientID,
			"exp": time.Now().Add(time.Minute).Unix(), "iat": time.Now().Unix(), "nonce": authQuery.Get("nonce"),
			"name": "Ada Example", "preferred_username": "example",
		}
		for name, value := range change {
			if value == nil {
				delete(claims, name)
			} else {
				claims[name] = value
			}
		}
	}
	code := randomToken()
	m.grants.Store(code, mockGrant{challenge: authQuery.Get("code_challenge"), claims: claims, wrongKey: wrongKey})
	return url.Values{"state": {authQuery.Get("state")}, "code": {code}, "iss": {m.config.Issuer}}
}

func TestAuthorizationCodeFlow(t *testing.T) {
	m := newMockProvider(t, nil)
	client, err := New(m.ctx, m.config)
	if err != nil {
		t.Fatal(err)
	}
	query, cookie := startTestFlow(t, client)
	_, otherBrowser := startTestFlow(t, client)
	callback := m.issueGrant(query, nil, false, false)

	wrongState := url.Values{"state": {randomToken()}, "code": {callback.Get("code")}}
	if _, err, _ := callTestCallback(client, wrongState, cookie); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("mismatched state returned %v", err)
	}

	for _, cookies := range [][]*http.Cookie{nil, {otherBrowser}, {cookie, cookie}} {
		_, err, response := callTestCallback(client, callback, cookies...)
		if !errors.Is(err, ErrInvalidState) || len(response.Result().Cookies()) != 0 {
			t.Fatalf("callback with invalid browser binding: %v", err)
		}
	}
	if m.exchangeCount.Load() != 0 {
		t.Fatal("invalid browser binding triggered a token exchange")
	}

	identity, err, response := callTestCallback(client, callback, cookie)
	want := Identity{Issuer: m.config.Issuer, Subject: "stable-user-id", PreferredUsername: "example", Name: "Ada Example"}
	if err != nil || identity != want {
		t.Fatalf("callback returned %#v, %v; want %#v", identity, err, want)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookie.Name || cookies[0].MaxAge != -1 || !cookies[0].Secure {
		t.Fatalf("callback did not clear the flow cookie: %v", cookies)
	}
	_, err, _ = callTestCallback(client, callback, cookie)
	if !errors.Is(err, ErrInvalidState) || m.exchangeCount.Load() != 1 {
		t.Fatalf("consumed flow could be replayed: %v", err)
	}
}

func TestCodeFlowWithoutDisplayClaims(t *testing.T) {
	m := newMockProvider(t, map[string]any{"token_endpoint_auth_methods_supported": []string{"client_secret_post"}})
	client, err := New(m.ctx, m.config)
	if err != nil {
		t.Fatal(err)
	}
	if client.oauth.Endpoint.AuthStyle != oauth2.AuthStyleInParams {
		t.Fatal("token exchange does not use the advertised client_secret_post method")
	}
	query, cookie := startTestFlow(t, client)
	callback := m.issueGrant(query, map[string]any{"name": nil, "preferred_username": nil}, false, false)
	callback.Del("iss") // The response issuer parameter is optional, unlike the ID-token claim.

	identity, err, _ := callTestCallback(client, callback, cookie)
	want := Identity{Issuer: m.config.Issuer, Subject: "stable-user-id"}
	if err != nil || identity != want {
		t.Fatalf("callback without display claims returned %#v, %v", identity, err)
	}
}

func TestInvalidIDToken(t *testing.T) {
	m := newMockProvider(t, nil)
	client, err := New(m.ctx, m.config)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name        string
		changes     map[string]any
		omitIDToken bool
		wrongKey    bool
	}{
		{name: "omitted token", omitIDToken: true},
		{name: "untrusted signature", wrongKey: true},
		{name: "issuer", changes: map[string]any{"iss": m.config.Issuer + "/"}},
		{name: "audience", changes: map[string]any{"aud": "different-client"}},
		{name: "untrusted additional audience", changes: map[string]any{"aud": []string{testClientID, "other-client"}, "azp": testClientID}},
		{name: "authorized party", changes: map[string]any{"azp": "different-client"}},
		{name: "missing nonce", changes: map[string]any{"nonce": nil}},
		{name: "incorrect nonce", changes: map[string]any{"nonce": randomToken()}},
		{name: "expired token", changes: map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}},
		{name: "future issue time", changes: map[string]any{"iat": time.Now().Add(time.Hour).Unix()}},
		{name: "missing subject", changes: map[string]any{"sub": nil}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, cookie := startTestFlow(t, client)
			callback := m.issueGrant(query, test.changes, test.omitIDToken, test.wrongKey)
			identity, err, _ := callTestCallback(client, callback, cookie)
			if !errors.Is(err, ErrInvalidIDToken) || identity != (Identity{}) {
				t.Fatalf("invalid token returned %#v, %v", identity, err)
			}
		})
	}
}

func TestInvalidProviderResponseConsumesFlow(t *testing.T) {
	m := newMockProvider(t, nil)
	client, err := New(m.ctx, m.config)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		change   func(url.Values)
		want     error
		exchange int32
	}{
		{"provider denied", func(q url.Values) { q.Set("error", "access_denied") }, ErrProviderDenied, 0},
		{"issuer mismatch", func(q url.Values) { q.Set("iss", "https://other.test") }, ErrInvalidCallback, 0},
		{"multiple codes", func(q url.Values) { q.Add("code", "second-code") }, ErrInvalidCallback, 0},
		{"exchange failure", func(q url.Values) { q.Set("code", "unknown-code") }, ErrTokenExchange, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query, cookie := startTestFlow(t, client)
			callback := m.issueGrant(query, nil, false, false)
			test.change(callback)

			previous := m.exchangeCount.Load()
			_, err, _ := callTestCallback(client, callback, cookie)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			_, err, _ = callTestCallback(client, callback, cookie)
			if !errors.Is(err, ErrInvalidState) || m.exchangeCount.Load() != previous+test.exchange {
				t.Fatalf("rejected flow was not consumed or unexpectedly attempted exchange: %v", err)
			}
		})
	}
}

func TestRejectsInvalidDiscovery(t *testing.T) {
	for _, metadata := range []map[string]any{
		{"issuer": "https://other.test"},
		{"token_endpoint": "http://127.0.0.1:8080/token"},
		{"jwks_uri": ""},
		{"token_endpoint_auth_methods_supported": []string{"private_key_jwt"}},
	} {
		m := newMockProvider(t, metadata)
		if _, err := New(m.ctx, m.config); err == nil {
			t.Fatalf("accepted invalid discovery metadata override: %v", metadata)
		}
	}
}
