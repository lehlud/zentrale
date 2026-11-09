package webapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestEditorProxyPreservesBasePathAndScopesOnlyVSCodeCookies(t *testing.T) {
	prefix := "/ide/project/session"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != prefix+"/static/workbench.js" || request.URL.Query().Get("version") != "1" {
			t.Errorf("lost editor path: %s", request.URL)
		}

		if request.Header.Get("Authorization") != "" {
			t.Error("authorization leaked to the editor")
		}

		if _, err := request.Cookie("zentrale_session"); err == nil {
			t.Error("Zentrale session leaked to the editor")
		}

		if cookie, err := request.Cookie("vscode-cli-secret-half"); err != nil ||
			cookie.Value != "client-secret" {
			t.Error("VS Code authentication cookie was dropped")
		}

		if request.Header.Get("X-Forwarded-Prefix") != prefix ||
			request.Header.Get("X-Forwarded-Proto") != "https" {
			t.Error("forwarded editor location is missing")
		}

		http.SetCookie(
			writer,
			&http.Cookie{
				Name:     "vscode-cli-secret-half",
				Value:    "new-secret",
				Path:     "/",
				HttpOnly: true,
			},
		)
		http.SetCookie(writer, &http.Cookie{Name: "zentrale_session", Value: "must-not-set", Path: "/"})
		_, _ = io.WriteString(writer, "asset")
	}))
	defer upstream.Close()

	transport := &http.Transport{DialContext: upstream.Client().Transport.(*http.Transport).DialContext}
	// Direct the proxy's test transport to the fixture; production uses SSH's
	// stream-local transport, which is covered by the remote editor tests.
	proxy := editorProxy(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Host = strings.TrimPrefix(upstream.URL, "http://")
		return transport.RoundTrip(request)
	}), prefix, true)
	defer transport.CloseIdleConnections()

	request := httptest.NewRequest(http.MethodGet, "https://zentrale.example"+prefix+"/static/workbench.js?version=1", nil)
	request.Header.Set("Authorization", "Bearer private")
	request.AddCookie(&http.Cookie{Name: "zentrale_session", Value: "private-session"})
	request.AddCookie(&http.Cookie{Name: "vscode-cli-secret-half", Value: "client-secret"})
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)

	if response.Code != http.StatusOK || response.Body.String() != "asset" {
		t.Fatal(response.Code, response.Body.String())
	}

	cookies := response.Result().Cookies()
	if len(cookies) != 1 ||
		cookies[0].Name != "vscode-cli-secret-half" ||
		cookies[0].Path != prefix+"/" ||
		!cookies[0].Secure ||
		!cookies[0].HttpOnly {
		t.Fatalf("unsafe editor cookies: %+v", cookies)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (run roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return run(request)
}

func TestEditorProxyForwardsWebSockets(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()

		kind, data, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(kind, data)
		}
	}))
	defer upstream.Close()

	proxy := editorProxy(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Host = strings.TrimPrefix(upstream.URL, "http://")
		return http.DefaultTransport.RoundTrip(request)
	}), "/ide/project/session", false)

	server := httptest.NewServer(proxy)
	defer server.Close()

	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ide/project/session/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	if err := connection.WriteMessage(websocket.TextMessage, []byte("editor")); err != nil {
		t.Fatal(err)
	}

	_, data, err := connection.ReadMessage()
	if err != nil || string(data) != "editor" {
		t.Fatalf("editor websocket: %q %v", data, err)
	}
}
