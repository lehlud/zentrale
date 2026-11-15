package webapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type huggingFaceTransport func(*http.Request) (*http.Response, error)

func (run huggingFaceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestHuggingFaceFieldsRequireAuthenticationAndNeverReturnCredential(t *testing.T) {
	fixture := newAPIFixture(t)
	query := `query{huggingFaceConnection{connected userName checkedAt}}`
	connect := `mutation($token:String!){connectHuggingFace(token:$token){connected userName checkedAt}}`
	const token = "hf_DummyGraphQLCredential123456789"
	for _, request := range []string{
		query, connect, `mutation{disconnectHuggingFace}`,
		`mutation{configureHuggingFaceOAuth(value:{clientId:"app",publicClient:true}){oauthConfigured}}`,
		`mutation{beginHuggingFaceLogin(redirectUri:"https://example.org/oauth/callback"){id}}`,
		`mutation{endHuggingFaceLogin(id:"unknown")}`,
	} {
		if fixture.query(t, request, map[string]any{"token": token})["errors"] == nil {
			t.Fatal("unauthenticated account access accepted", request)
		}
	}

	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	fixture.engine.HuggingFace.Client.Transport = huggingFaceTransport(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatal("missing internal credential")
		}

		return &http.Response{
			StatusCode: 200, Header: make(http.Header),
			Body:    io.NopCloser(strings.NewReader(`{"type":"user","name":"hf-owner"}`)),
			Request: request,
		}, nil
	})

	data := requireData(t, fixture.query(t, connect, map[string]any{"token": token}))
	if data["connectHuggingFace"].(map[string]any)["userName"] != "hf-owner" {
		t.Fatal(data)
	}

	result := fixture.query(t, query, nil)
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), token) || !strings.Contains(string(encoded), "hf-owner") {
		t.Fatal("public connection metadata is unsafe", string(encoded))
	}

	if fixture.query(t, `query{huggingFaceConnection{token}}`, nil)["errors"] == nil {
		t.Fatal("credential became a GraphQL output field")
	}

	requireData(t, fixture.query(t, `mutation{disconnectHuggingFace}`, nil))
	data = requireData(t, fixture.query(t, query, nil))
	if data["huggingFaceConnection"].(map[string]any)["connected"] != false {
		t.Fatal("disconnect retained connection", data)
	}
}

func TestHuggingFaceOAuthNativeCallbackAndPublicSchema(t *testing.T) {
	fixture := newAPIFixture(t)
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	configured := requireData(t, fixture.query(t, `mutation{
		configureHuggingFaceOAuth(value:{clientId:"hf-app",clientSecret:"private-app-secret",publicClient:false}){
			oauthConfigured oauthConfidential oauthClientId
		}
	}`, nil))
	if configured["configureHuggingFaceOAuth"].(map[string]any)["oauthConfigured"] != true {
		t.Fatal("OAuth app was not configured")
	}

	var exchanges int
	fixture.engine.HuggingFace.Client.Transport = huggingFaceTransport(func(request *http.Request) (*http.Response, error) {
		body := `{"sub":"hf-user-id","preferred_username":"browser-owner"}`
		if request.URL.Path == "/oauth/token" {
			exchanges++
			clientID, secret, ok := request.BasicAuth()
			if !ok || clientID != "hf-app" || secret != "private-app-secret" {
				t.Error("token request lost confidential-client authentication")
			}

			body = `{"access_token":"hf_oauth_privateAccess","refresh_token":"privateRefresh","token_type":"Bearer","expires_in":3600}`
		} else if request.URL.Path != "/oauth/userinfo" || request.Header.Get("Authorization") != "Bearer hf_oauth_privateAccess" {
			t.Error("unexpected OAuth userinfo request")
		}

		return &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})

	begin := `mutation($uri:String!){beginHuggingFaceLogin(redirectUri:$uri){id authorizationUrl expiresAt}}`
	for _, callback := range []string{"https://attacker.example/oauth/callback", fixture.server.URL + "/oauth/callback?extra=yes"} {
		if fixture.query(t, begin, map[string]any{"uri": callback})["errors"] == nil {
			t.Fatal("unsafe HF callback accepted", callback)
		}
	}

	data := requireData(t, fixture.query(t, begin, map[string]any{"uri": fixture.server.URL + "/oauth/callback"}))
	login := data["beginHuggingFaceLogin"].(map[string]any)
	address, err := url.Parse(login["authorizationUrl"].(string))
	if err != nil {
		t.Fatal(err)
	}
	stateValue := address.Query().Get("state")
	if !strings.HasPrefix(stateValue, "huggingface:"+login["id"].(string)+":") {
		t.Fatal("HF state does not route to the native callback")
	}

	complete := `mutation($state:String!,$code:String!){completeBrowserLogin(state:$state,code:$code)}`
	variables := map[string]any{"state": stateValue, "code": "private-code"}
	result := requireData(t, fixture.query(t, complete, variables))
	if result["completeBrowserLogin"] != true {
		t.Fatal("native callback failed", result)
	}

	if fixture.query(t, complete, variables)["errors"] == nil || exchanges != 1 {
		t.Fatal("native callback replayed an HF authorization code")
	}

	result = requireData(t, fixture.query(t, `query{
		huggingFaceConnection{connected userName authMode oauthClientId oauthConfigured login{id}}
	}`, nil))
	encoded, _ := json.Marshal(result)
	if !strings.Contains(string(encoded), "browser-owner") || !strings.Contains(string(encoded), "oauth") {
		t.Fatal("missing OAuth account metadata", result)
	}
	for _, secret := range []string{"private-app-secret", "privateAccess", "privateRefresh", "private-code"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("GraphQL exposed an HF credential")
		}
	}

	for _, field := range []string{"clientSecret", "oauth", "token", "verifier"} {
		if fixture.query(t, `query{huggingFaceConnection{`+field+`}}`, nil)["errors"] == nil {
			t.Fatal("private field exposed in HF output", field)
		}
	}
}
