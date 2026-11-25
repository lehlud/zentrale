package oidc

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	coreoidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	providerTimeout = 10 * time.Second
	maxClockSkew    = 5 * time.Minute
)

var (
	ErrInvalidCallback = errors.New("invalid OIDC callback")
	ErrInvalidState    = errors.New("OIDC login flow could not be matched to this browser")
	ErrProviderDenied  = errors.New("OIDC provider did not authorize login")
	ErrTokenExchange   = errors.New("OIDC token exchange failed")
	ErrInvalidIDToken  = errors.New("OIDC ID token could not be verified")
	ErrTooManyFlows    = errors.New("too many pending OIDC login flows")
)

// Identity contains verified ID-token claims. Persist Issuer and Subject as
// the external account key; PreferredUsername and Name are only for display.
type Identity struct {
	Issuer            string
	Subject           string
	PreferredUsername string
	Name              string
}

// Client runs a browser authorization flow for one configured provider. Use a
// single instance for both Start and Callback; pending flows are process-local.
type Client struct {
	issuer       string
	oauth        oauth2.Config
	verifier     *coreoidc.IDTokenVerifier
	httpClient   *http.Client
	flows        flowStore
	cookieName   string
	secureCookie bool
}

// New validates configuration and discovers the configured issuer.
func New(ctx context.Context, config Config) (*Client, error) {
	issuer, redirect, err := config.validate()
	if err != nil {
		return nil, err
	}

	// A caller can provide a client with custom TLS roots. Keep requests bounded
	// and do not follow redirects, which can forward token endpoint credentials.
	httpClient := &http.Client{Timeout: providerTimeout}
	if configured, ok := ctx.Value(oauth2.HTTPClient).(*http.Client); ok && configured != nil {
		httpClient.Transport = configured.Transport
		if configured.Timeout > 0 && configured.Timeout < providerTimeout {
			httpClient.Timeout = configured.Timeout
		}
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx = coreoidc.ClientContext(ctx, httpClient)
	provider, err := coreoidc.NewProvider(ctx, config.Issuer)
	if err != nil {
		return nil, fmt.Errorf("OIDC provider discovery failed: %w", err)
	}

	endpoint, err := verifiedEndpoint(provider, issuer, config.Issuer)
	if err != nil {
		return nil, err
	}

	secureCookie := redirect.Scheme == "https"
	cookieName := "zentrale-oidc-flow"
	if secureCookie {
		cookieName = "__Host-" + cookieName
	}

	return &Client{
		issuer: config.Issuer,
		oauth: oauth2.Config{
			ClientID:     config.ClientID,
			ClientSecret: config.ClientSecret,
			RedirectURL:  config.RedirectURL,
			Endpoint:     endpoint,
			Scopes:       []string{coreoidc.ScopeOpenID, "profile"},
		},
		verifier:     provider.Verifier(&coreoidc.Config{ClientID: config.ClientID}),
		httpClient:   httpClient,
		flows:        flowStore{pending: make(map[string]authFlow)},
		cookieName:   cookieName,
		secureCookie: secureCookie,
	}, nil
}

func verifiedEndpoint(provider *coreoidc.Provider, issuer *url.URL, expectedIssuer string) (oauth2.Endpoint, error) {
	var discovery struct {
		Issuer           string   `json:"issuer"`
		JWKSURL          string   `json:"jwks_uri"`
		TokenAuthMethods []string `json:"token_endpoint_auth_methods_supported"`
	}
	if err := provider.Claims(&discovery); err != nil {
		return oauth2.Endpoint{}, errors.New("invalid OIDC provider metadata")
	}

	if discovery.Issuer != expectedIssuer {
		return oauth2.Endpoint{}, errors.New("OIDC discovery returned a different issuer")
	}

	endpoint := provider.Endpoint()
	for _, field := range []struct{ name, value string }{
		{"authorization_endpoint", endpoint.AuthURL},
		{"token_endpoint", endpoint.TokenURL},
		{"jwks_uri", discovery.JWKSURL},
	} {
		u, err := parseWebURL(field.value)
		if err != nil || (issuer.Scheme == "https" && u.Scheme != "https") {
			return oauth2.Endpoint{}, fmt.Errorf("invalid OIDC discovery URL for %s", field.name)
		}
	}

	switch methods := discovery.TokenAuthMethods; {
	case len(methods) == 0 || slices.Contains(methods, "client_secret_basic"):
		endpoint.AuthStyle = oauth2.AuthStyleInHeader
	case slices.Contains(methods, "client_secret_post"):
		endpoint.AuthStyle = oauth2.AuthStyleInParams
	default:
		return oauth2.Endpoint{}, errors.New("OIDC provider does not support client secret authentication")
	}

	return endpoint, nil
}

// Start creates a browser-bound flow and redirects to the configured provider.
// The caller should mount it on the same origin as the registered callback.
func (c *Client) Start(w http.ResponseWriter, r *http.Request) error {
	setFlowHeaders(w)
	if r.Method != http.MethodGet {
		return ErrInvalidCallback
	}

	now := time.Now()
	state := randomToken()
	flow := authFlow{
		browserToken: randomToken(),
		nonce:        randomToken(),
		pkceVerifier: oauth2.GenerateVerifier(),
		expiresAt:    now.Add(flowTTL),
	}
	if !c.flows.add(state, flow, now) {
		return ErrTooManyFlows
	}

	http.SetCookie(w, c.newFlowCookie(flow.browserToken, flow.expiresAt, int(flowTTL/time.Second)))
	authURL := c.oauth.AuthCodeURL(state, coreoidc.Nonce(flow.nonce), oauth2.S256ChallengeOption(flow.pkceVerifier))
	http.Redirect(w, r, authURL, http.StatusFound)
	return nil
}

// Callback consumes the browser-bound flow and verifies the provider identity.
// It may clear the flow cookie but does not write a status or create a session.
func (c *Client) Callback(w http.ResponseWriter, r *http.Request) (Identity, error) {
	setFlowHeaders(w)
	if r.Method != http.MethodGet {
		return Identity{}, ErrInvalidCallback
	}

	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return Identity{}, ErrInvalidCallback
	}

	states, browserToken := query["state"], flowCookieValue(r, c.cookieName)
	if len(states) != 1 || len(states[0]) != flowTokenLen || len(browserToken) != flowTokenLen {
		return Identity{}, ErrInvalidState
	}

	flow, ok := c.flows.consume(states[0], browserToken, time.Now())
	if !ok {
		return Identity{}, ErrInvalidState
	}

	http.SetCookie(w, c.newFlowCookie("", time.Unix(1, 0), -1))
	if issuers, present := query["iss"]; present && (len(issuers) != 1 || issuers[0] != c.issuer) {
		return Identity{}, ErrInvalidCallback
	}

	if query.Has("error") {
		return Identity{}, ErrProviderDenied
	}

	codes := query["code"]
	if len(codes) != 1 || codes[0] == "" {
		return Identity{}, ErrInvalidCallback
	}

	ctx := coreoidc.ClientContext(r.Context(), c.httpClient)
	token, err := c.oauth.Exchange(ctx, codes[0], oauth2.VerifierOption(flow.pkceVerifier))
	if err != nil {
		return Identity{}, ErrTokenExchange
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Identity{}, ErrInvalidIDToken
	}

	return c.verifyIdentity(ctx, rawIDToken, flow.nonce)
}

func (c *Client) verifyIdentity(ctx context.Context, raw, nonce string) (Identity, error) {
	token, err := c.verifier.Verify(ctx, raw)
	if err != nil {
		return Identity{}, ErrInvalidIDToken
	}

	// The verifier permits an issuer alias for Google and additional audiences.
	// This client trusts only the exact configured issuer and its own audience.
	if token.Issuer != c.issuer || len(token.Audience) != 1 || token.Audience[0] != c.oauth.ClientID ||
		subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) != 1 {
		return Identity{}, ErrInvalidIDToken
	}

	if !validSubject(token.Subject) || token.IssuedAt.IsZero() || token.IssuedAt.After(time.Now().Add(maxClockSkew)) {
		return Identity{}, ErrInvalidIDToken
	}

	var claims struct {
		AuthorizedParty   *string `json:"azp"`
		PreferredUsername string  `json:"preferred_username"`
		Name              string  `json:"name"`
	}
	if err := token.Claims(&claims); err != nil {
		return Identity{}, ErrInvalidIDToken
	}

	if claims.AuthorizedParty != nil && *claims.AuthorizedParty != c.oauth.ClientID {
		return Identity{}, ErrInvalidIDToken
	}

	return Identity{
		Issuer:            token.Issuer,
		Subject:           token.Subject,
		PreferredUsername: claims.PreferredUsername,
		Name:              claims.Name,
	}, nil
}

func validSubject(subject string) bool {
	return len(subject) <= 255 && strings.TrimSpace(subject) != "" &&
		strings.IndexFunc(subject, func(r rune) bool { return r < 32 || r >= 127 }) == -1
}

func setFlowHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
