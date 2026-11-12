package webapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

func TestLoginStateQueriesAuthenticatedReadonlyAndServerPushed(t *testing.T) {
	f := newAPIFixture(t)
	providerQuery := `query{providerLogin(id:"device-one"){id providerId status error}}`
	if result := f.query(t, providerQuery, nil); result["errors"] == nil {
		t.Fatal("unauthenticated login state accepted", result)
	}
	requireData(t, f.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	if data := requireData(t, f.query(t, providerQuery, nil)); data["providerLogin"] != nil {
		t.Fatal(data)
	}
	transaction := providerlogin.Transaction{
		Session:               domain.ProviderLogin{ID: "device-one", ProviderID: "provider-one", Method: "device", Status: "pending", Interval: 5, ExpiresAt: time.Now().Add(time.Minute)},
		DeviceAuthorizationID: "SECRET-device", CodeVerifier: "SECRET-verifier",
	}
	if err := f.engine.Store.Save("provider_login", "device-one", "provider-one", transaction); err != nil {
		t.Fatal(err)
	}
	conn := openReadOnlyPushProtocolV1(t, f)
	if err := conn.WriteJSON(map[string]any{"type": "subscribe", "requestId": "login-state", "query": providerQuery}); err != nil {
		t.Fatal(err)
	}
	initial := requireData(t, readReadOnlyPushProtocolV1(t, conn, "login-state"))
	if initial["providerLogin"].(map[string]any)["status"] != "pending" {
		t.Fatal(initial)
	}
	transaction.Session.Status = "connected"
	if err := f.engine.Store.Save("provider_login", "device-one", "provider-one", transaction); err != nil {
		t.Fatal(err)
	}
	f.engine.Emit("provider_login", "", map[string]string{"id": "device-one"})
	updated := requireData(t, readReadOnlyPushProtocolV1(t, conn, "login-state"))
	if updated["providerLogin"].(map[string]any)["status"] != "connected" {
		t.Fatal(updated)
	}
	body, _ := json.Marshal(updated)
	if strings.Contains(string(body), "SECRET") {
		t.Fatal("login secret exposed")
	}
	if result := f.query(t, `query{providerLogin(id:"device-one"){deviceAuthorizationID}}`, nil); result["errors"] == nil {
		t.Fatal("private transaction field exposed")
	}

	git := domain.GitLogin{ID: "git-one", ConnectionID: "github", Status: "pending", ExpiresAt: time.Now().Add(time.Minute)}
	if err := f.engine.Store.Save("git_account", "github", "", map[string]any{
		"public": domain.GitConnection{ID: "github", Kind: "github"}, "accessToken": "SECRET-token",
		"login": map[string]any{"public": git, "deviceCode": "SECRET-code"},
	}); err != nil {
		t.Fatal(err)
	}
	gitQuery := `query{gitLogin(connectionId:"github",loginId:"git-one"){id connectionId status}}`
	data := requireData(t, f.query(t, gitQuery, nil))
	if data["gitLogin"].(map[string]any)["status"] != "pending" {
		t.Fatal(data)
	}
	if wrong := requireData(t, f.query(t, `query{gitLogin(connectionId:"gitlab",loginId:"git-one"){id}}`, nil)); wrong["gitLogin"] != nil {
		t.Fatal("cross-connection login exposed", wrong)
	}
	requireData(t, f.query(t, `mutation{cancelGitLogin(connectionId:"github",loginId:"git-one")}`, nil))
	data = requireData(t, f.query(t, gitQuery, nil))
	if data["gitLogin"].(map[string]any)["status"] != "cancelled" {
		t.Fatal(data)
	}
}
