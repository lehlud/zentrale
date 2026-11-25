package huggingface

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
)

const oauthOwner = "test-session-owner"

func beginTestOAuth(t *testing.T, service *Service, confidential bool) domain.HuggingFaceLogin {
	t.Helper()
	if _, err := service.Store.BootstrapAccount(datastore.Account{
		User: domain.User{ID: domain.InitialUserID, Username: "admin", Role: domain.RoleAdmin}, PasswordHash: "test-only",
	}, ""); err != nil {
		t.Fatal(err)
	}
	if err := service.Store.CreateUserSession(oauthOwner, domain.InitialUserID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	_, err := service.ConfigureOAuth(domain.HuggingFaceOAuthConfig{
		ClientID: "hf-test-app", ClientSecret: "private-app-secret", PublicClient: !confidential,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{
		Owner: oauthOwner, RedirectURI: "https://zentrale.example/preview/oauth/callback",
	})
	login, err := service.BeginOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}

	return login
}

func oauthState(t *testing.T, login domain.HuggingFaceLogin) string {
	t.Helper()
	address, err := url.Parse(login.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}

	return address.Query().Get("state")
}

func TestOAuthPublicAndConfidentialPKCEAndTokenReplacement(t *testing.T) {
	for _, confidential := range []bool{false, true} {
		name := "public"
		if confidential {
			name = "confidential"
		}

		t.Run(name, func(t *testing.T) {
			var login domain.HuggingFaceLogin
			var exchanges int
			service := testService(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Scheme != "https" || request.URL.Host != "huggingface.co" {
					t.Fatal("OAuth escaped the official origin")
				}

				switch request.URL.Path {
				case "/oauth/token":
					exchanges++
					if err := request.ParseForm(); err != nil {
						t.Fatal(err)
					}

					authorize, _ := url.Parse(login.AuthorizationURL)
					query := authorize.Query()
					digest := sha256.Sum256([]byte(request.Form.Get("code_verifier")))
					if query.Get("scope") != "openid profile read-repos gated-repos" ||
						query.Get("code_challenge_method") != "S256" ||
						query.Get("code_challenge") != base64.RawURLEncoding.EncodeToString(digest[:]) ||
						request.Form.Get("client_id") != "hf-test-app" ||
						request.Form.Get("grant_type") != "authorization_code" ||
						request.Form.Get("code") != "private-code" ||
						request.Form.Get("redirect_uri") != query.Get("redirect_uri") {
						t.Fatal("incorrect PKCE authorization/token contract")
					}

					clientID, secret, basic := request.BasicAuth()
					if basic != confidential || (confidential && (clientID != "hf-test-app" || secret != "private-app-secret")) {
						t.Fatal("incorrect public/confidential client authentication")
					}

					return testResponse(request, 200, `{"access_token":"hf_oauth_privateAccess","refresh_token":"privateRefresh","token_type":"Bearer","expires_in":3600}`), nil
				case "/oauth/userinfo":
					if request.Header.Get("Authorization") != "Bearer hf_oauth_privateAccess" {
						t.Fatal("userinfo missing OAuth bearer")
					}

					return testResponse(request, 200, `{"sub":"hf-user-id","preferred_username":"oauth-user"}`), nil
				case "/api/whoami-v2":
					return testResponse(request, 200, `{"type":"user","name":"token-user"}`), nil
				default:
					t.Fatalf("unexpected OAuth request: %s", request.URL.Path)
					return nil, errors.New("unexpected endpoint")
				}
			})
			login = beginTestOAuth(t, service, confidential)
			state := oauthState(t, login)

			for _, invalid := range []struct{ id, state, owner string }{
				{login.ID, "wrong-state", oauthOwner},
				{login.ID, state, "other-session"},
				{"superseded-login", state, oauthOwner},
			} {
				if _, err := service.CompleteOAuth(context.Background(), invalid.id, invalid.state, "private-code", invalid.owner); err == nil {
					t.Fatal("invalid browser login accepted")
				}
			}

			connected, err := service.CompleteOAuth(context.Background(), login.ID, state, "private-code", oauthOwner)
			if err != nil || !connected.Connected || connected.AuthMode != "oauth" || connected.UserName != "oauth-user" {
				t.Fatal("OAuth did not connect", connected, err)
			}

			encoded, _ := json.Marshal(connected)
			for _, secret := range []string{"private-app-secret", "privateAccess", "privateRefresh", "code_verifier"} {
				if strings.Contains(string(encoded), secret) {
					t.Fatal("public metadata exposed OAuth credentials")
				}
			}

			if _, err := service.CompleteOAuth(context.Background(), login.ID, state, "private-code", oauthOwner); err == nil || exchanges != 1 {
				t.Fatal("authorization code replayed", exchanges, err)
			}

			connected, err = service.Connect(context.Background(), testToken)
			if err != nil || connected.AuthMode != "token" || !connected.OAuthConfigured {
				t.Fatal("PAT could not replace OAuth without losing app configuration", err)
			}

			if err := service.Disconnect(); err != nil {
				t.Fatal(err)
			}
			status, err := service.Status()
			if err != nil || status.Connected || !status.OAuthConfigured || status.Login.ID != "" {
				t.Fatal("disconnect did not retain only app configuration", status, err)
			}
		})
	}
}

func TestOAuthInFlightCancellationCannotRestoreAccount(t *testing.T) {
	for _, action := range []string{"logout", "cancel", "disconnect", "app-change", "supersede", "token-replace", "close"} {
		t.Run(action, func(t *testing.T) {
			started := make(chan struct{})
			proceed := make(chan struct{})
			service := testService(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/api/whoami-v2" {
					return testResponse(request, 200, `{"type":"user","name":"replacement-user"}`), nil
				}

				if request.URL.Path == "/oauth/token" {
					close(started)
					<-proceed // A transport may return a response after cancellation.
					return testResponse(request, 200, `{"access_token":"privateAccess","token_type":"Bearer","expires_in":3600}`), nil
				}

				return testResponse(request, 200, `{"sub":"user-id","preferred_username":"stale-user"}`), nil
			})
			login := beginTestOAuth(t, service, false)
			done := make(chan error, 1)
			go func() {
				_, err := service.CompleteOAuth(context.Background(), login.ID, oauthState(t, login), "private-code", oauthOwner)
				done <- err
			}()
			<-started

			var err error
			switch action {
			case "logout":
				err = service.Store.DeleteUserSession(oauthOwner)
			case "cancel":
				err = service.EndOAuth(login.ID, oauthOwner)
			case "disconnect":
				err = service.Disconnect()
			case "app-change":
				_, err = service.ConfigureOAuth(domain.HuggingFaceOAuthConfig{ClientID: "different-app", PublicClient: true})
			case "supersede":
				ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{
					Owner: oauthOwner, RedirectURI: "https://zentrale.example/oauth/callback",
				})
				_, err = service.BeginOAuth(ctx)
			case "token-replace":
				_, err = service.Connect(context.Background(), testToken)
			case "close":
				service.Close()
			}

			close(proceed)
			if err != nil {
				t.Fatal(err)
			}

			if err := <-done; err == nil {
				t.Fatal("cancelled/revoked/superseded login was accepted")
			}

			status, err := service.Status()
			if err != nil || (status.Connected && status.UserName != "replacement-user") {
				t.Fatal("late OAuth response restored stale credentials", status, err)
			}
		})
	}
}

func TestOAuthRefreshIsSerializedAndRevocationFailsClosed(t *testing.T) {
	var refreshes atomic.Int32
	var revoked atomic.Bool
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/userinfo" {
			return testResponse(request, 200, `{"sub":"user-id","preferred_username":"refresh-user"}`), nil
		}

		if err := request.ParseForm(); err != nil {
			t.Error(err)
		}
		if request.Form.Get("grant_type") == "authorization_code" {
			return testResponse(request, 200, `{"access_token":"oldAccess","refresh_token":"keepRefresh","token_type":"Bearer","expires_in":3600}`), nil
		}

		refreshes.Add(1)
		if request.Form.Get("refresh_token") != "keepRefresh" {
			t.Error("refresh token was not retained")
		}

		if revoked.Load() {
			return testResponse(request, 400, `{"error":"invalid_grant","error_description":"private token details"}`), nil
		}

		return testResponse(request, 200, `{"access_token":"freshAccess","token_type":"Bearer","expires_in":3600}`), nil
	})
	login := beginTestOAuth(t, service, false)
	if _, err := service.CompleteOAuth(context.Background(), login.ID, oauthState(t, login), "code", oauthOwner); err != nil {
		t.Fatal(err)
	}

	expire := func() {
		t.Helper()
		value, err := service.account()
		if err != nil {
			t.Fatal(err)
		}

		value.OAuth.ExpiresAt = time.Now().Add(-time.Minute)
		if err := service.saveAccount(value); err != nil {
			t.Fatal(err)
		}
	}
	expire()

	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			_, token, release, err := service.beginRequest(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			defer release()

			if token != "freshAccess" {
				t.Error("dataset request received stale credentials")
			}
		})
	}
	workers.Wait()

	if refreshes.Load() != 1 {
		t.Fatal("concurrent requests refreshed more than once", refreshes.Load())
	}

	expire()
	revoked.Store(true)
	if err := service.refreshOAuth(context.Background()); !errors.Is(err, errReauthorize) || strings.Contains(err.Error(), "private token details") {
		t.Fatal("revoked refresh was not safely rejected", err)
	}

	status, err := service.Status()
	if err != nil || status.Connected || !status.OAuthConfigured {
		t.Fatal("revoked OAuth remained connected or lost app configuration", status, err)
	}
}

func TestOAuthRedirectExpiryAndLostResponseNeverReplay(t *testing.T) {
	var calls int
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		calls++
		response := testResponse(request, http.StatusFound, "")
		response.Header.Set("Location", "https://huggingface.co/stolen-code")
		return response, nil
	})
	login := beginTestOAuth(t, service, false)
	state := oauthState(t, login)
	if _, err := service.CompleteOAuth(context.Background(), login.ID, state, "code", oauthOwner); err == nil {
		t.Fatal("redirected token response accepted")
	}

	if _, err := service.CompleteOAuth(context.Background(), login.ID, state, "code", oauthOwner); err == nil || calls != 1 {
		t.Fatal("redirect followed or authorization code replayed", calls, err)
	}

	ctx := oauthflow.WithCallback(context.Background(), oauthflow.Callback{
		Owner: oauthOwner, RedirectURI: "https://zentrale.example/oauth/callback",
	})
	login, err := service.BeginOAuth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	value, _ := service.account()
	value.Login.Flow.ExpiresAt = time.Now().Add(-time.Minute)
	if err := service.saveAccount(value); err != nil {
		t.Fatal(err)
	}

	if _, err := service.CompleteOAuth(context.Background(), login.ID, oauthState(t, login), "code", oauthOwner); err == nil || calls != 1 {
		t.Fatal("expired code sent to token endpoint", calls, err)
	}
}

func TestOAuthLostTokenResponseCannotBeReplayed(t *testing.T) {
	var calls int
	service := testService(t, func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("transport failed with private-code and private-secret")
	})
	login := beginTestOAuth(t, service, false)
	state := oauthState(t, login)

	for range 2 {
		_, err := service.CompleteOAuth(context.Background(), login.ID, state, "code", oauthOwner)
		if err == nil || strings.Contains(err.Error(), "private-") {
			t.Fatal("lost token response was not safely rejected", err)
		}
	}

	if calls != 1 {
		t.Fatal("lost token exchange was replayed", calls)
	}
}

func TestOAuthWithoutRefreshRemainsUsableUntilActualExpiry(t *testing.T) {
	service := testService(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/oauth/token" {
			return testResponse(request, 200, `{"access_token":"shortAccess","token_type":"Bearer","expires_in":30}`), nil
		}

		return testResponse(request, 200, `{"sub":"user-id","preferred_username":"short-lived"}`), nil
	})
	login := beginTestOAuth(t, service, false)
	if _, err := service.CompleteOAuth(context.Background(), login.ID, oauthState(t, login), "code", oauthOwner); err != nil {
		t.Fatal(err)
	}

	_, token, release, err := service.beginRequest(context.Background())
	if err != nil {
		t.Fatal("unexpired access without a refresh token was rejected", err)
	}
	release()
	if token != "shortAccess" {
		t.Fatal("short-lived token was not available")
	}

	value, _ := service.account()
	value.OAuth.ExpiresAt = time.Now().Add(-time.Second)
	if err := service.saveAccount(value); err != nil {
		t.Fatal(err)
	}

	if err := service.refreshOAuth(context.Background()); !errors.Is(err, errReauthorize) {
		t.Fatal("expired non-refreshable access was not rejected", err)
	}
}
