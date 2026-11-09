package webapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func previewTestTransport(upstream string) http.RoundTripper {
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.URL.Host = strings.TrimPrefix(upstream, "http://")
		return http.DefaultTransport.RoundTrip(request)
	})
}

func TestPreviewProxyPreservesRequestsAndScopesCredentialsBehindTLS(t *testing.T) {
	root := "/preview/project/session/"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		if request.Method != http.MethodPost || request.URL.EscapedPath() != "/api/a%2Fb" ||
			request.URL.RawQuery != "q=a%2Fb" || string(body) != `{"value":42}` {
			t.Error("proxy changed the API request", request.Method, request.URL, string(body))
		}
		if request.Host != "localhost:1235" || request.Header.Get("Origin") != "http://localhost:1234" ||
			request.Header.Get("Referer") != "http://localhost:1234/dashboard" ||
			request.Header.Get("X-Forwarded-Proto") != "https" {
			t.Error("local API origin or public TLS origin was lost", request.Header, request.Host)
		}
		if request.Header.Get("Authorization") != "Bearer app-token" {
			t.Error("application authentication was removed")
		}
		for _, cookie := range request.Cookies() {
			if cookie.Name != "app_session" {
				t.Error("Zentrale credentials leaked", cookie.Name)
			}
		}
		if request.Header.Get("X-Zentrale-Preview") != "" || request.Header.Get("X-Zentrale-Preview-Origin-Port") != "" {
			t.Error("internal preview headers leaked")
		}
		http.SetCookie(writer, &http.Cookie{Name: "app_session", Value: "application", Path: "/api", Domain: "localhost", HttpOnly: true})
		http.SetCookie(writer, &http.Cookie{Name: "zentrale_session", Value: "must-not-set", Path: "/"})
		writer.Header().Set("Clear-Site-Data", `"cookies"`)
		writer.Header().Set("Service-Worker-Allowed", "/")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"ok":true}`)
	}))
	defer upstream.Close()
	proxy := previewProxy(previewTestTransport(upstream.URL), root, 1235, true)
	request := httptest.NewRequest(http.MethodPost, "https://zentrale.example"+root+"1235/api/a%2Fb?q=a%2Fb", strings.NewReader(`{"value":42}`))
	request.Header.Set("Origin", "https://zentrale.example")
	request.Header.Set("Referer", "https://zentrale.example"+root+"1234/dashboard")
	request.Header.Set("Authorization", "Bearer app-token")
	request.Header.Set("X-Zentrale-Preview", "1")
	request.Header.Set("X-Zentrale-Preview-Origin-Port", "1234")
	for _, cookie := range []*http.Cookie{
		{Name: previewCookieName(root, "app_session", "/api"), Value: "application"},
		{Name: "zentrale_session", Value: "private"},
		{Name: "vscode-cli-secret-half", Value: "editor-secret"},
	} {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, request)
	if response.Code != 200 || response.Body.String() != `{"ok":true}` {
		t.Fatal(response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != previewCookieName(root, "app_session", "/api") ||
		cookies[0].Path != root || cookies[0].Domain != "" || !cookies[0].Secure || !cookies[0].HttpOnly ||
		cookies[1].Name != previewCookieName(root, "zentrale_session", "/") {
		t.Fatal("unsafe application cookies", cookies)
	}
	if response.Header().Get("Clear-Site-Data") != "" || response.Header().Get("Service-Worker-Allowed") != "" {
		t.Fatal("application changed the containing origin's state")
	}
}

func TestPreviewProxyRewritesCompressedHTMLAndPreservesApplicationScripts(t *testing.T) {
	root := "/preview/project/session/"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Content-Encoding", "gzip")
		writer.Header().Set("ETag", `"original"`)
		writer.Header().Add("Content-Security-Policy", "default-src 'self'; script-src 'nonce-app' 'strict-dynamic'; object-src 'none'")
		writer.Header().Add("Content-Security-Policy", "script-src-elem 'nonce-app'; base-uri 'self'")
		compressed := gzip.NewWriter(writer)
		defer compressed.Close()
		_, _ = io.WriteString(compressed, `<!doctype html><html><head><script nonce="app">window.value = "/keep-this-string";</script><script type="module" src="/src/main.js"></script></head><body><a href="http://127.0.0.1:1235/login?x=1#done">API</a><form action="/submit"></form></body></html>`)
	}))
	defer upstream.Close()
	proxy := previewProxy(previewTestTransport(upstream.URL), root, 1234, true)
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest("GET", "https://zentrale.example"+root+"1234/", nil))
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, root+"_zentrale/runtime.js") ||
		!strings.Contains(body, `src="`+root+`1234/src/main.js"`) ||
		!strings.Contains(body, `href="`+root+`1235/login?x=1#done"`) ||
		!strings.Contains(body, `action="`+root+`1234/submit"`) ||
		!strings.Contains(body, `window.value = "/keep-this-string";`) {
		t.Fatal(response.Code, body)
	}
	if strings.Index(body, "_zentrale/runtime.js") > strings.Index(body, "window.value") ||
		response.Header().Get("Content-Encoding") != "" || response.Header().Get("ETag") != "" {
		t.Fatal("bootstrap order or rewritten body metadata is wrong", response.Header(), body)
	}
	if len(response.Header().Values("Content-Security-Policy")) != 2 ||
		!strings.Contains(response.Header().Get("Content-Security-Policy"), "'strict-dynamic'") ||
		!strings.Contains(response.Header().Get("Content-Security-Policy"), "object-src 'none'") {
		t.Fatal("application CSP was discarded", response.Header())
	}
}

func TestPreviewProxyForwardsWebSocketsAndKeepsTheFrontendOrigin(t *testing.T) {
	root := "/preview/project/session/"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/hmr" || request.URL.Query().Get("token") != "app-token" ||
			request.URL.Query().Has("__zentrale_preview_origin") || request.Header.Get("Origin") != "http://localhost:1234" {
			t.Error("WebSocket path, token or origin was changed", request.URL, request.Header)
		}
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
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
	server := httptest.NewServer(previewProxy(previewTestTransport(upstream.URL), root, 1235, true))
	defer server.Close()
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+root+"1235/hmr?token=app-token&__zentrale_preview_origin=1234", http.Header{"Origin": []string{"https://zentrale.example"}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_ = connection.WriteMessage(websocket.TextMessage, []byte("reload"))
	_, message, err := connection.ReadMessage()
	if err != nil || string(message) != "reload" {
		t.Fatal(string(message), err)
	}
}

func TestPreviewURLRewritesOnlyLoopbackAndApplicationPaths(t *testing.T) {
	root := "/preview/project/session/"
	for _, test := range []struct{ input, expected string }{
		{"/assets/main.js", root + "1234/assets/main.js"},
		{"http://localhost:1235/api?q=1#part", root + "1235/api?q=1#part"},
		{"http://127.0.0.1:1235/api", root + "1235/api"},
		{"http://[::1]:1235/api", root + "1235/api"},
		{"https://external.example/api", "https://external.example/api"},
		{"https://localhost.evil.example/api", "https://localhost.evil.example/api"},
		{"data:image/svg+xml,123", "data:image/svg+xml,123"},
		{"relative.js", "relative.js"},
		{"http://localhost:1235", root + "1235/"},
		{"/files/a%2Fb", root + "1234/files/a%2Fb"},
		{root + "1234/already", root + "1234/already"},
	} {
		t.Run(test.input, func(t *testing.T) {
			if actual := previewURL(test.input, root, 1234); actual != test.expected {
				t.Fatal(actual, test.expected)
			}
		})
	}
	if policy := previewScriptPolicy("default-src 'none'; style-src 'self'", "test"); !strings.Contains(policy, "script-src  'nonce-test'") || !strings.Contains(policy, "default-src 'none'") {
		t.Fatal("CSP fallback was broadened", policy)
	}
}
