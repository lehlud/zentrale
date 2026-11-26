package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/graphql-go/graphql"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/auth"
)

func TestSessionCookiesUseTheAuthenticationModeLifetime(t *testing.T) {
	for _, tc := range []struct {
		name, issuer string
		want         int
	}{
		{name: "password", want: 7 * 24 * 60 * 60},
		{name: "oidc", issuer: "https://login.example.org", want: 4 * 60 * 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{Auth: &auth.Service{OIDCIssuer: tc.issuer}, SecureCookie: true}
			e := echo.New()
			response := httptest.NewRecorder()
			request := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), response)

			server.setSessionCookie(request, "signed-session-fixture")
			server.setSessionCookie(request, "")
			cookies := response.Result().Cookies()
			if len(cookies) != 2 || cookies[0].Name != "zentrale_session" || cookies[0].MaxAge != tc.want || !cookies[0].Secure || !cookies[0].HttpOnly {
				t.Fatalf("incorrect lifetime or flags: %+v", cookies)
			}
			if cookies[1].Value != "" || cookies[1].MaxAge != -1 {
				t.Fatal("logout did not expire the session cookie")
			}
		})
	}
}

func TestAuthenticationErrorsHaveOneProtocolCode(t *testing.T) {
	fixture := newAPIFixture(t)
	response, err := fixture.client.Post(fixture.server.URL+"/graphql", "application/json",
		strings.NewReader(`{"query":"{projects{id}}"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated query returned HTTP %d", response.StatusCode)
	}
	var result graphql.Result
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	assertAuthenticationError := func(t *testing.T, result graphql.Result) {
		t.Helper()
		if len(result.Errors) != 1 || result.Errors[0].Message != "authentication required" || result.Errors[0].Extensions["code"] != "UNAUTHENTICATED" {
			t.Fatalf("missing structured authentication error: %+v", result)
		}
	}
	assertAuthenticationError(t, result)

	// Resolver guards and socket-query execution must agree with HTTP validation.
	authentication, err := auth.New(fixture.engine.Store, "")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Store: fixture.engine.Store, Engine: fixture.engine, Auth: authentication}
	schema, err := server.Schema()
	if err != nil {
		t.Fatal(err)
	}
	assertAuthenticationError(t, *graphql.Do(graphql.Params{
		Schema: schema, RequestString: `{projects{id}}`, Context: context.Background(),
	}))
	assertAuthenticationError(t, server.executeSocketQuery(context.Background(), schema, "invalid", "test", socketQueryRequest{Query: `{projects{id}}`}).(graphql.Result))
}

func TestRevokedEventSessionClosesWithAuthenticationCode(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	conn := openReadOnlyPushProtocolV1(t, fixture)
	if err := conn.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "session", Query: `{status{authenticated}}`}); err != nil {
		t.Fatal(err)
	}
	requireData(t, readReadOnlyPushProtocolV1(t, conn, "session"))
	requireData(t, fixture.query(t, `mutation{logout}`, nil))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		var closed *websocket.CloseError
		if !errors.As(err, &closed) || closed.Code != sessionExpired {
			t.Fatalf("revocation looked like a network outage: %v", err)
		}
		break
	}
}
