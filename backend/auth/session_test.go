package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestSessionJWTExpiryAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name, issuer string
		lifetime     time.Duration
	}{
		{name: "password", lifetime: 7 * 24 * time.Hour},
		{name: "oidc", issuer: "https://login.example.org", lifetime: 4 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "zentrale.db")
			open := func() *Service {
				t.Helper()
				store, err := datastore.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = store.Close() })
				s, err := NewWithIssuer(store, "", tc.issuer)
				if err != nil {
					t.Fatal(err)
				}
				return s
			}

			initial := open()
			var token string
			var err error
			if tc.issuer == "" {
				const password = "session lifetime fixture"
				if err := initial.Setup(password); err != nil {
					t.Fatal(err)
				}
				token, err = initial.Login(password, "client")
			} else {
				token, err = initial.LoginOIDC(tc.issuer, "fixture-subject", "alice", "Alice")
			}
			if err != nil || !initial.Valid(token) {
				t.Fatalf("sign-in did not create a valid session: %v", err)
			}

			var claims jwt.RegisteredClaims
			_, err = sessionParser.ParseWithClaims(token, &claims, func(*jwt.Token) (any, error) {
				return initial.sessionKey, nil
			})
			if err != nil || claims.IssuedAt == nil || claims.ExpiresAt == nil ||
				claims.ExpiresAt.Sub(claims.IssuedAt.Time) != tc.lifetime {
				t.Fatalf("the signed session must expire after %s: %v", tc.lifetime, err)
			}

			if err := initial.Store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := open()
			if !reopened.Valid(token) {
				t.Fatal("reopening the database invalidated a live session")
			}

			// The JWT expiry must be honored even if a database receipt exists.
			claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute))
			expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(reopened.sessionKey)
			if err != nil {
				t.Fatal(err)
			}
			if err := reopened.Store.CreateUserSession(TokenHash(expired), domain.InitialUserID, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if reopened.Valid(expired) {
				t.Fatal("expired JWT was honored")
			}

			if err := reopened.Logout(token); err != nil {
				t.Fatal(err)
			}
			if reopened.Valid(token) {
				t.Fatal("logged-out JWT was honored")
			}
		})
	}
}

func TestFirstSessionKeyInvalidatesLegacyOpaqueReceipts(t *testing.T) {
	store := testStore(t)
	if created, err := store.BootstrapAccount(initialAccount(DefaultUsername, "legacy-hash"), ""); err != nil || !created {
		t.Fatalf("could not seed an earlier installation: %v", err)
	}
	legacy := TokenHash("a session from the previous version")
	if err := store.CreateUserSession(legacy, domain.InitialUserID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if _, err := New(store, ""); err != nil {
		t.Fatal(err)
	}
	if valid, err := store.UserSessionValid(legacy); err != nil || valid {
		t.Fatalf("legacy session survived initial key creation: %v, %v", valid, err)
	}
}
