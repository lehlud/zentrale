// Package oauthflow supplies the session-bound PKCE state shared by native
// browser logins. Provider-specific consent and token requests stay with their
// respective adapters.
package oauthflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"time"
)

type callbackKey struct{}

// Callback is supplied by the authenticated web boundary, never by a model or
// an OAuth provider. Owner is the hash of the initiating application session.
type Callback struct {
	RedirectURI string
	Owner       string
}

func WithCallback(ctx context.Context, callback Callback) context.Context {
	return context.WithValue(ctx, callbackKey{}, callback)
}

func CallbackFrom(ctx context.Context) Callback {
	callback, _ := ctx.Value(callbackKey{}).(Callback)
	return callback
}

// Pending contains credentials and must only be persisted in the encrypted
// credential store. Its authorization URL is the only public projection.
type Pending struct {
	State       string    `json:"state"`
	Verifier    string    `json:"verifier"`
	RedirectURI string    `json:"redirectUri"`
	Owner       string    `json:"owner"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func New(prefix string, callback Callback, now time.Time) (Pending, error) {
	if callback.Owner == "" || callback.RedirectURI == "" {
		return Pending{}, errors.New("browser login requires an authenticated callback")
	}

	return Pending{
		State:       prefix + randomValue(),
		Verifier:    randomValue(),
		RedirectURI: callback.RedirectURI,
		Owner:       callback.Owner,
		ExpiresAt:   now.Add(10 * time.Minute),
	}, nil
}

func (pending Pending) AuthorizationURL(endpoint, clientID string, scopes []string, extra url.Values) string {
	parameters := url.Values{}
	for name, values := range extra {
		parameters[name] = append([]string(nil), values...)
	}

	digest := sha256.Sum256([]byte(pending.Verifier))
	parameters.Set("client_id", clientID)
	parameters.Set("response_type", "code")
	parameters.Set("redirect_uri", pending.RedirectURI)
	parameters.Set("state", pending.State)
	parameters.Set("code_challenge", base64.RawURLEncoding.EncodeToString(digest[:]))
	parameters.Set("code_challenge_method", "S256")

	if len(scopes) > 0 {
		parameters.Set("scope", strings.Join(scopes, " "))
	}

	return endpoint + "?" + parameters.Encode()
}

func (pending Pending) Validate(state, owner string, now time.Time) error {
	if pending.State == "" || pending.Owner == "" || pending.Verifier == "" {
		return errors.New("browser login is no longer current; start again")
	}

	if subtle.ConstantTimeCompare([]byte(state), []byte(pending.State)) != 1 ||
		subtle.ConstantTimeCompare([]byte(owner), []byte(pending.Owner)) != 1 {
		return errors.New("browser login does not belong to this session")
	}

	if !now.Before(pending.ExpiresAt) {
		return errors.New("browser login expired; start again")
	}

	return nil
}

func randomValue() string {
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}

	return base64.RawURLEncoding.EncodeToString(bytes[:])
}
