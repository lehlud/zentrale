package oauthflow

import (
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"testing"
	"time"
)

func TestPendingIsBoundToStateSessionAndExpiry(t *testing.T) {
	now := time.Now()
	callback := Callback{Owner: "session-hash", RedirectURI: "https://app.example/oauth/callback"}
	pending, err := New("git:connection:login:", callback, now)
	if err != nil {
		t.Fatal(err)
	}

	if err := pending.Validate(pending.State, callback.Owner, now); err != nil {
		t.Fatal(err)
	}

	for _, attempt := range []struct {
		state string
		owner string
		at    time.Time
	}{
		{pending.State + "x", callback.Owner, now},
		{pending.State, "another-session", now},
		{pending.State, callback.Owner, pending.ExpiresAt},
	} {
		if err := pending.Validate(attempt.state, attempt.owner, attempt.at); err == nil {
			t.Fatal("unbound or expired callback accepted", attempt)
		}
	}

	address, err := url.Parse(pending.AuthorizationURL("https://git.example/login/oauth/authorize", "app", []string{"read:user"}, nil))
	if err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256([]byte(pending.Verifier))
	parameters := address.Query()
	if len(pending.Verifier) != 43 || parameters.Get("code_challenge_method") != "S256" ||
		parameters.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) ||
		parameters.Get("state") != pending.State || parameters.Get("redirect_uri") != callback.RedirectURI {
		t.Fatal("invalid PKCE parameters", parameters)
	}

	pending.Verifier = ""
	if err := pending.Validate(pending.State, callback.Owner, now); err == nil {
		t.Fatal("consumed verifier accepted")
	}
}

func TestPendingNeedsAuthenticatedCallbackAndFreshRandomness(t *testing.T) {
	if _, err := New("git:", Callback{}, time.Now()); err == nil {
		t.Fatal("missing callback accepted")
	}

	callback := Callback{Owner: "session", RedirectURI: "https://app.example/oauth/callback"}
	first, _ := New("git:", callback, time.Now())
	second, _ := New("git:", callback, time.Now())
	if first.State == second.State || first.Verifier == second.Verifier || first.State == first.Verifier {
		t.Fatal("state or verifier was reused")
	}
}
