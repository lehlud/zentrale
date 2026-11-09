package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
)

type previewFixture struct {
	server       *httptest.Server
	engine       *agent.Engine
	project      domain.Project
	session      domain.EditorSession
	token        string
	frontendPort int
	apiPort      int
}

func newPreviewFixture(t *testing.T) *previewFixture {
	t.Helper()
	fixture := &previewFixture{}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if cookie, err := request.Cookie("zentrale_session"); err == nil && cookie.Value == fixture.token {
			t.Error("Zentrale login cookie reached development API")
		}
		if origin := request.Header.Get("Origin"); origin != "" && origin != "http://localhost:"+strconv.Itoa(fixture.frontendPort) {
			t.Error("development API received wrong frontend origin", request.URL.Path, origin, "expected", fixture.frontendPort, "referer", request.Header.Get("Referer"))
		}
		switch request.URL.Path {
		case "/api":
			if request.URL.Query().Has("omit") && len(request.Cookies()) > 0 {
				t.Error("credentials: omit forwarded application cookies")
			}
			http.SetCookie(writer, &http.Cookie{Name: "application_session", Value: "application", Path: "/", HttpOnly: true})
			http.SetCookie(writer, &http.Cookie{Name: "zentrale_session", Value: "nested-app", Path: "/"})
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{"value": 42})
		case "/echo":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.Copy(writer, request.Body)
		case "/cookies":
			values := map[string]string{}
			for _, cookie := range request.Cookies() {
				values[cookie.Name] = cookie.Value
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(values)
		case "/events":
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: live-event\n\n")
			writer.(http.Flusher).Flush()
		case "/socket":
			connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			kind, message, err := connection.ReadMessage()
			if err == nil {
				_ = connection.WriteMessage(kind, message)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(api.Close)
	apiURL, _ := url.Parse(api.URL)
	fixture.apiPort, _ = strconv.Atoi(apiURL.Port())
	frontend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/", "/deep":
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(writer, `<!doctype html><html><head><title>Development preview</title><script type="module" src="/src/main.js"></script></head><body><h1>Starting</h1><a href="/deep">Deep route</a></body></html>`)
		case "/src/main.js":
			writer.Header().Set("Content-Type", "text/javascript")
			data, err := os.ReadFile("testdata/preview-app.js")
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = io.WriteString(writer, strings.ReplaceAll(string(data), "API_PORT", strconv.Itoa(fixture.apiPort)))
		case "/src/helper.js":
			writer.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(writer, `export const label = "Preview ready";`)
		case "/ping":
			_, _ = io.WriteString(writer, "frontend")
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(frontend.Close)
	frontendURL, _ := url.Parse(frontend.URL)
	fixture.frontendPort, _ = strconv.Atoi(frontendURL.Port())
	peer, err := testssh.StartEditorPeer(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		if strings.Contains(command, "ZENTRALE_EDITOR_READY ") {
			_, _ = fmt.Fprintln(stdout, `ZENTRALE_EDITOR_READY {"socketPath":"/tmp/ze-preview-test/vscode.sock"}`)
			_, _ = io.Copy(io.Discard, stdin)
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peer.Close)
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine, err := agent.New(store, echoProvider{})
	if err != nil {
		t.Fatal(err)
	}
	fixture.engine = engine
	t.Cleanup(engine.Close)
	authentication, err := auth.New(store, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := authentication.SetupWithIdentity("preview test password", domain.CommitIdentity{Name: "Preview", Email: "preview@example.test"}); err != nil {
		t.Fatal(err)
	}
	fixture.token, err = authentication.Login("preview test password", "preview-test")
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	public, _ := remote.DecodePublicKey(peer.PublicKey)
	if err := store.TrustSSHKey(public); err != nil {
		t.Fatal(err)
	}
	machine := domain.Machine{ID: "preview-host", Name: "Preview host", Host: peer.Host, Port: peer.Port, User: "test", KeyID: key.ID, Sandbox: "none"}
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	fixture.project = domain.Project{ID: "preview-project", Name: "Preview project", Directory: t.TempDir(), MachineIDs: []string{machine.ID}}
	if err := store.Save("project", fixture.project.ID, "", fixture.project); err != nil {
		t.Fatal(err)
	}
	fixture.session, err = engine.Editors.Start(context.Background(), fixture.project)
	if err != nil {
		t.Fatal(err)
	}
	frontendDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(frontendDirectory, "index.html"), []byte("<!doctype html><title>Zentrale</title>"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &Server{Store: store, Auth: authentication, Engine: engine, Frontend: frontendDirectory, SecureCookie: true}
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	internal := httptest.NewServer(handler)
	t.Cleanup(internal.Close)
	upstream, _ := url.Parse(internal.URL)
	fixture.server = httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(upstream))
	service.Origin = fixture.server.URL
	t.Cleanup(fixture.server.Close)
	return fixture
}

func TestPreviewPrivateLoopbackForwardingThroughSSHAndTLSTermination(t *testing.T) {
	fixture := newPreviewFixture(t)
	base := fixture.server.URL + "/preview/" + fixture.project.ID + "/" + fixture.session.ID + "/"
	client := fixture.server.Client()
	response, err := client.Get(base + strconv.Itoa(fixture.apiPort) + "/api")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("preview accessible without login", response.StatusCode)
	}
	for _, test := range []struct {
		port           int
		path, expected string
	}{
		{fixture.apiPort, "/api", `"value":42`},
		{fixture.frontendPort, "/ping", "frontend"},
	} {
		request, _ := http.NewRequest("GET", base+strconv.Itoa(test.port)+test.path, nil)
		request.AddCookie(&http.Cookie{Name: "zentrale_session", Value: fixture.token})
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || !strings.Contains(string(body), test.expected) {
			t.Fatal(response.StatusCode, string(body))
		}
	}
	request, _ := http.NewRequest("GET", base+"9842/", nil)
	request.AddCookie(&http.Cookie{Name: "zentrale_session", Value: fixture.token})
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatal("internal relay port was exposed", response.StatusCode)
	}
	request.URL.Path = strings.Replace(request.URL.Path, "9842", strconv.Itoa(fixture.apiPort), 1)
	request.Header.Set("Origin", "https://unrelated.example")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatal("cross-origin request was accepted", response.StatusCode)
	}
}

func TestPreviewBrowserBehindTLSTerminatingProxy(t *testing.T) {
	if os.Getenv("ZENTRALE_PREVIEW_BROWSER") != "1" {
		t.Skip("set ZENTRALE_PREVIEW_BROWSER=1 for the Chromium preview integration test")
	}
	fixture := newPreviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "../../frontend/scripts/preview-smoke.mjs", fixture.server.URL, fixture.project.ID, fixture.session.ID, strconv.Itoa(fixture.frontendPort))
	command.Env = append(os.Environ(), "ZENTRALE_PREVIEW_TEST_TOKEN="+fixture.token)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("browser preview: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
