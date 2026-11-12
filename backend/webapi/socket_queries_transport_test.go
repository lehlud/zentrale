package webapi

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func openReadOnlyPushProtocolV1(t *testing.T, fixture *apiFixture) *websocket.Conn {
	t.Helper()
	request, _ := http.NewRequest("GET", fixture.server.URL, nil)
	headers := http.Header{}
	for _, cookie := range fixture.client.Jar.Cookies(request.URL) {
		headers.Add("Cookie", cookie.String())
	}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/events?after=latest", headers)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func readReadOnlyPushProtocolV1(t *testing.T, conn *websocket.Conn, id string) map[string]any {
	t.Helper()
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			t.Fatal(err)
		}
		if frame["type"] != "query_result" {
			continue
		}
		if frame["requestId"] != id {
			t.Fatalf("unexpected snapshot: %v", frame)
		}
		return frame["result"].(map[string]any)
	}
}

func TestReadOnlyPushProtocolV1TransportSecurityAndUpdates(t *testing.T) {
	fixture := newAPIFixture(t)
	endpoint := "ws" + strings.TrimPrefix(fixture.server.URL, "http") + "/events?after=latest"
	if conn, response, err := websocket.DefaultDialer.Dial(endpoint, nil); err == nil {
		conn.Close()
		t.Fatal("unauthenticated socket accepted")
	} else if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unexpected auth response: %v %v", response, err)
	}
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))
	request, _ := http.NewRequest("GET", fixture.server.URL, nil)
	headers := http.Header{"Origin": []string{"https://foreign.invalid"}}
	for _, cookie := range fixture.client.Jar.Cookies(request.URL) {
		headers.Add("Cookie", cookie.String())
	}
	if conn, response, err := websocket.DefaultDialer.Dial(endpoint, headers); err == nil {
		conn.Close()
		t.Fatal("foreign origin accepted")
	} else if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected origin response: %v %v", response, err)
	}
	if err := fixture.engine.Store.Save("project", "push", "", map[string]any{"id": "push", "name": "Before"}); err != nil {
		t.Fatal(err)
	}
	conn := openReadOnlyPushProtocolV1(t, fixture)
	if err := conn.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "write", Query: `mutation{logout}`}); err != nil {
		t.Fatal(err)
	}
	if readReadOnlyPushProtocolV1(t, conn, "write")["errors"] == nil {
		t.Fatal("mutation executed")
	}
	if err := conn.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "project", Query: `query($id:String!){project(id:$id){id name}}`, Variables: map[string]any{"id": "push"}}); err != nil {
		t.Fatal(err)
	}
	if got := requireData(t, readReadOnlyPushProtocolV1(t, conn, "project"))["project"].(map[string]any)["name"]; got != "Before" {
		t.Fatal(got)
	}
	if err := fixture.engine.Store.Save("project", "push", "", map[string]any{"id": "push", "name": "After"}); err != nil {
		t.Fatal(err)
	}
	fixture.engine.Emit("project", "", nil)
	// No further client query: this update must be pushed by the server.
	if got := requireData(t, readReadOnlyPushProtocolV1(t, conn, "project"))["project"].(map[string]any)["name"]; got != "After" {
		t.Fatal(got)
	}
	if err := conn.WriteJSON(socketQueryRequest{Type: "unsubscribe", RequestID: "project"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "barrier", Query: `{status{authenticated}}`}); err != nil {
		t.Fatal(err)
	}
	requireData(t, readReadOnlyPushProtocolV1(t, conn, "barrier"))
	oversize := openReadOnlyPushProtocolV1(t, fixture)
	_ = oversize.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "oversize", Query: strings.Repeat(" ", socketQueryReadLimit)})
	for {
		var frame map[string]any
		if err := oversize.ReadJSON(&frame); err != nil {
			break
		}
		if frame["type"] == "query_result" {
			t.Fatal("oversize frame executed")
		}
	}
	for range 3 {
		cancelled := openReadOnlyPushProtocolV1(t, fixture)
		_ = cancelled.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "cancel", Query: `{status{authenticated}}`})
		_ = cancelled.Close()
	}
	reconnected := openReadOnlyPushProtocolV1(t, fixture)
	if err := reconnected.WriteJSON(socketQueryRequest{Type: "subscribe", RequestID: "reconnected", Query: `{status{authenticated}}`}); err != nil {
		t.Fatal(err)
	}
	requireData(t, readReadOnlyPushProtocolV1(t, reconnected, "reconnected"))
	requireData(t, fixture.query(t, `mutation{logout}`, nil))
	fixture.engine.Emit("session-revoked", "", nil)
	for {
		var frame map[string]any
		if err := conn.ReadJSON(&frame); err != nil {
			break
		}
		if frame["type"] == "query_result" || frame["type"] == "session-revoked" {
			t.Fatal("data pushed after session revocation")
		}
	}
}
