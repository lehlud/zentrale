package webapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
)

func TestTerminalDisconnectDiagnosticsUseFixedCategories(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{&websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "untrusted peer text"}, "browser_closed"},
		{&websocket.CloseError{Code: websocket.CloseGoingAway}, "browser_closed"},
		{&websocket.CloseError{Code: websocket.CloseAbnormalClosure}, "websocket_read_error"},
		{&net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}, "websocket_heartbeat_timeout"},
		{io.EOF, "websocket_read_error"},
	} {
		if got := terminalWebSocketReadReason(test.err); got != test.want {
			t.Errorf("category = %q, want %q", got, test.want)
		}
	}
}

func TestTerminalOriginIsStrict(t *testing.T) {
	for _, origin := range []string{"", "null", "http://example.test", "https://evil.test", "https://example.test/", "https://example.test?x=1", "https://example.test#x", "https://user@example.test"} {
		request := httptest.NewRequest("GET", "https://example.test/terminal/project", nil)
		request.Header.Set("Origin", origin)
		if (&Server{}).terminalOrigin(request) {
			t.Errorf("accepted unsafe Origin %q", origin)
		}
	}
	request := httptest.NewRequest("GET", "https://example.test/terminal/project", nil)
	request.Header.Set("Origin", "https://example.test")
	if !(&Server{}).terminalOrigin(request) {
		t.Fatal("same origin rejected")
	}
	request.TLS = nil
	if (&Server{}).terminalOrigin(request) {
		t.Fatal("scheme mismatch accepted")
	}
	if !(&Server{SecureCookie: true}).terminalOrigin(request) {
		t.Fatal("HTTPS deployment rejected")
	}
	request.TLS = &tls.ConnectionState{}
	if (&Server{Origin: "https://other.test"}).terminalOrigin(request) {
		t.Fatal("configured origin ignored")
	}
}

func TestTerminalWebSocketRealPTYSession(t *testing.T) {
	for _, proxied := range []bool{false, true} {
		name := "direct"
		if proxied {
			name = "TLS-terminating-proxy"
		}
		t.Run(name, func(t *testing.T) { testTerminalWebSocketRealPTYSession(t, proxied) })
	}
}

func testTerminalWebSocketRealPTYSession(t *testing.T, proxied bool) {
	fixture := newAPIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	peer, err := testssh.StartWithCommandHandler(func(command string, input io.Reader, stdout, stderr io.Writer) int {
		process := exec.CommandContext(ctx, "/bin/sh", "-c", command)
		process.Stdout, process.Stderr = stdout, stderr
		stdin, err := process.StdinPipe()
		if err != nil {
			return 1
		}
		go func() { io.Copy(stdin, input); stdin.Close() }()
		if err := process.Run(); err != nil {
			if status, ok := err.(*exec.ExitError); ok {
				return status.ExitCode()
			}
			return 1
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	key, err := fixture.engine.Store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, err := remote.DecodePublicKey(peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.engine.Store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "terminal-worker", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID, Sandbox: remote.SandboxDisabled}
	project := domain.Project{ID: "terminal-project", Directory: t.TempDir(), MachineIDs: []string{machine.ID}, CommitName: "Terminal Test", CommitEmail: "terminal@example.test"}
	if err := fixture.engine.Store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	if err := fixture.engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("b", 43)
	if err := fixture.engine.Store.CreateSession(auth.TokenHash(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	endpoint := fixture.server.URL
	dialer := *websocket.DefaultDialer
	if proxied {
		authentication, err := auth.New(fixture.engine.Store, "")
		if err != nil {
			t.Fatal(err)
		}
		api := &Server{Store: fixture.engine.Store, Auth: authentication, Engine: fixture.engine}
		handler, err := api.Handler()
		if err != nil {
			t.Fatal(err)
		}
		backend := httptest.NewServer(handler)
		defer backend.Close()
		upstream, err := url.Parse(backend.URL)
		if err != nil {
			t.Fatal(err)
		}
		proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
		defer proxy.Close()
		// Trusted deployment configuration, not forwarded request headers.
		api.Origin, api.SecureCookie = proxy.URL, true
		endpoint = proxy.URL
		dialer.TLSClientConfig = proxy.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	}
	header := http.Header{"Origin": {endpoint}, "Cookie": {"zentrale_session=" + token}}
	connection, _, err := dialer.Dial("ws"+strings.TrimPrefix(endpoint, "http")+"/terminal/"+project.ID+"?cols=91&rows=33", header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetReadDeadline(time.Now().Add(15 * time.Second))
	for _, status := range []string{"starting", "selecting_environment", "connecting", "checking_environment", "preparing_environment", "preparing_sources", "waiting_runtime", "preparing_terminal", "starting_terminal", "ready"} {
		var event map[string]any
		if err := connection.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != "status" || event["status"] != status {
			t.Fatal(event)
		}
	}
	// Application liveness must be answered without entering the PTY/input
	// queue. Native WS pong frames are not observable in browser JavaScript.
	if err := connection.WriteJSON(map[string]string{"type": "ping"}); err != nil {
		t.Fatal(err)
	}
	for {
		kind, data, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			continue // The initial interactive prompt may precede the pong.
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil || event["type"] != "pong" {
			t.Fatal(string(data), err)
		}
		break
	}
	readOutput := func(marker string) string {
		t.Helper()
		var output strings.Builder
		for !strings.Contains(output.String(), marker) {
			kind, data, err := connection.ReadMessage()
			if err != nil {
				t.Fatal(err)
			}
			if kind != websocket.BinaryMessage {
				t.Fatalf("not raw output: %s", data)
			}
			output.Write(data)
		}
		return output.String()
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte("stty size; printf 'first-done\\n'\n")); err != nil {
		t.Fatal(err)
	}
	if output := readOutput("first-done\r\n"); !strings.Contains(output, "33 91") {
		t.Fatal(output)
	}
	// Cover sustained interactive traffic, not just startup and one command.
	// Each echo crosses WebSocket, SSH, the real Python relay and a real PTY;
	// the proxied variant also exercises the TLS-terminating reverse proxy.
	var slowest time.Duration
	for n := range 24 {
		started := time.Now()
		_ = connection.SetReadDeadline(started.Add(2 * time.Second))
		command := fmt.Sprintf("printf 'latency-%02d\\n'\n", n)
		if err := connection.WriteMessage(websocket.BinaryMessage, []byte(command)); err != nil {
			t.Fatal(err)
		}
		readOutput(fmt.Sprintf("latency-%02d\r\n", n))
		slowest = max(slowest, time.Since(started))
	}
	t.Logf("24 WebSocket/SSH/PTY roundtrips, slowest %s", slowest)
	_ = connection.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := connection.WriteJSON(map[string]any{"type": "resize", "cols": 72, "rows": 27}); err != nil {
		t.Fatal(err)
	}
	if err := connection.WriteJSON(map[string]any{"type": "input", "data": "stty size; printf 'second-done\\n'\n"}); err != nil {
		t.Fatal(err)
	}
	if output := readOutput("second-done\r\n"); !strings.Contains(output, "27 72") {
		t.Fatal(output)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, []byte("exit 7\n")); err != nil {
		t.Fatal(err)
	}
	for {
		kind, data, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if kind == websocket.BinaryMessage {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event["type"] != "exit" || event["code"] != float64(7) {
			t.Fatal(event)
		}
		break
	}
}

func TestTerminalGeometryBounds(t *testing.T) {
	for _, query := range []string{"cols=0", "rows=-1", "cols=1001", "rows=1.5", "cols=NaN", "cols=", "cols=80&cols=90"} {
		if _, _, err := terminalGeometry(httptest.NewRequest("GET", "/terminal/p?"+query, nil)); err == nil {
			t.Error("accepted", query)
		}
	}
	cols, rows, err := terminalGeometry(httptest.NewRequest("GET", "/terminal/p", nil))
	if err != nil || cols != 80 || rows != 24 {
		t.Fatal(cols, rows, err)
	}
}

func TestTerminalProbeGuardsWithoutProvisioning(t *testing.T) {
	fixture := newAPIFixture(t)
	token := strings.Repeat("c", 43)
	if err := fixture.engine.Store.CreateSession(auth.TokenHash(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, origin, query string
		authenticated       bool
		status              int
	}{
		{"no session", fixture.server.URL, "", false, 401},
		{"no origin", "", "", true, 403},
		{"foreign origin", "https://evil.test", "", true, 403},
		{"invalid geometry", fixture.server.URL, "?cols=0", true, 400},
		{"valid probe for nonexistent project", fixture.server.URL, "", true, 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest("POST", fixture.server.URL+"/terminal/missing-project"+test.query, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Origin", test.origin)
			if test.authenticated {
				request.AddCookie(&http.Cookie{Name: "zentrale_session", Value: token})
			}
			response, err := fixture.client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" {
				t.Fatal(response.StatusCode, response.Header)
			}
			if test.status == 200 {
				var result map[string]string
				if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result["status"] != "ok" {
					t.Fatal(result, err)
				}
			}
		})
	}
}

func TestTerminalRejectsUnauthenticatedBeforeEngine(t *testing.T) {
	server := &Server{Auth: &auth.Service{}}
	e := echo.New()
	e.GET("/terminal/:project", server.terminal)
	response := httptest.NewRecorder()
	e.ServeHTTP(response, httptest.NewRequest("GET", "/terminal/project", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatal(response.Code)
	}
}

func TestTerminalAuthenticationOriginAndGeometryBeforeProvisioning(t *testing.T) {
	fixture := newAPIFixture(t)
	token := strings.Repeat("a", 43)
	if err := fixture.engine.Store.CreateSession(auth.TokenHash(token), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		origin, query string
		status        int
	}{
		{"", "", 403}, {"http://evil.test", "", 403}, {fixture.server.URL, "?cols=0", 400},
	} {
		request, _ := http.NewRequest("GET", fixture.server.URL+"/terminal/missing-project"+test.query, nil)
		request.AddCookie(&http.Cookie{Name: "zentrale_session", Value: token})
		request.Header.Set("Origin", test.origin)
		response, err := fixture.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatal(response.StatusCode, test)
		}
	}
	header := http.Header{"Origin": {fixture.server.URL}, "Cookie": {"zentrale_session=" + token}}
	connection, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(fixture.server.URL, "http")+"/terminal/missing-project", header)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	var starting, selecting, failure map[string]any
	if err := connection.ReadJSON(&starting); err != nil {
		t.Fatal(err)
	}
	if err := connection.ReadJSON(&selecting); err != nil {
		t.Fatal(err)
	}
	if err := connection.ReadJSON(&failure); err != nil {
		t.Fatal(err)
	}
	if starting["status"] != "starting" || selecting["status"] != "selecting_environment" || failure["type"] != "error" || failure["message"] == "" {
		t.Fatal(starting, selecting, failure)
	}
}

func TestSPACSPAllowsWasmButNotScriptEval(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("app"), 0600); err != nil {
		t.Fatal(err)
	}
	handler, err := (&Server{Frontend: root}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	policy := response.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "script-src 'self' 'wasm-unsafe-eval';") || strings.Contains(policy, "'unsafe-eval'") || strings.Contains(policy, "script-src 'self' 'unsafe-inline'") {
		t.Fatal(policy)
	}
}
