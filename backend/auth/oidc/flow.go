package oidc

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"
)

const (
	flowTTL      = 5 * time.Minute
	maxFlows     = 1024
	flowTokenLen = 43 // Unpadded base64url encoding of 32 random bytes.
)

type authFlow struct {
	browserToken string
	nonce        string
	pkceVerifier string
	expiresAt    time.Time
}

// flowStore retains only pending flows. Callbacks consume a matching flow
// atomically, before making any request to the provider.
type flowStore struct {
	mu      sync.Mutex
	pending map[string]authFlow
}

func (s *flowStore) add(state string, flow authFlow, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for key, pending := range s.pending {
		if !now.Before(pending.expiresAt) {
			delete(s.pending, key)
		}
	}
	if _, exists := s.pending[state]; exists || len(s.pending) >= maxFlows {
		return false
	}

	s.pending[state] = flow
	return true
}

func (s *flowStore) consume(state, browserToken string, now time.Time) (authFlow, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	flow, ok := s.pending[state]
	if !ok {
		return authFlow{}, false
	}
	if !now.Before(flow.expiresAt) {
		delete(s.pending, state)
		return authFlow{}, false
	}
	if subtle.ConstantTimeCompare([]byte(browserToken), []byte(flow.browserToken)) != 1 {
		return authFlow{}, false
	}

	delete(s.pending, state)
	return flow, true
}

func randomToken() string {
	var bytes [32]byte
	rand.Read(bytes[:])
	return base64.RawURLEncoding.EncodeToString(bytes[:])
}

func (c *Client) newFlowCookie(value string, expiresAt time.Time, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     c.cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   c.secureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func flowCookieValue(r *http.Request, name string) string {
	var value string
	found := false
	for _, cookie := range r.Cookies() {
		if cookie.Name != name {
			continue
		}
		if found {
			return ""
		}
		value, found = cookie.Value, true
	}
	return value
}
