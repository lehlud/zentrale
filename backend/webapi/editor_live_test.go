package webapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/internal/testssh"
	"github.com/lehlud/zentrale/remote"
	"github.com/lehlud/zentrale/vscode"
)

// This opt-in test uses the official workbench and headless server, an isolated
// SSH peer and a real Chromium. No production database or machine is contacted.
func TestPublishedVSCodeWorkbenchOverSSH(t *testing.T) {
	webDirectory := os.Getenv("ZENTRALE_VSCODE_SMOKE_WEB")
	serverDirectory := os.Getenv("ZENTRALE_VSCODE_SMOKE_SERVER")
	if webDirectory == "" || serverDirectory == "" {
		t.Skip("set ZENTRALE_VSCODE_SMOKE_WEB and ZENTRALE_VSCODE_SMOKE_SERVER to matching extracted official distributions")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	root := t.TempDir()
	webCache := filepath.Join(root, "web")
	agentCache := filepath.Join(root, "remote-cache")
	commit := vscode.CurrentRelease().Commit
	platform := "server-linux-" + map[string]string{"arm64": "arm64", "amd64": "x64"}[runtime.GOARCH]
	if err := os.MkdirAll(filepath.Join(agentCache, commit, platform), 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(serverDirectory, filepath.Join(agentCache, commit, platform, "server")); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(webCache, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(webDirectory, filepath.Join(webCache, commit)); err != nil {
		t.Fatal(err)
	}

	workspace := filepath.Join(root, "remote-project")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(workspace, "remote.py"), []byte("print('remote workspace')\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(workspace, "remote.ts"), []byte("const answer: number = 'wrong';\n"), 0600); err != nil {
		t.Fatal(err)
	}

	loginShell := os.Getenv("ZENTRALE_VSCODE_SMOKE_SHELL")
	if loginShell == "" {
		loginShell = "/bin/sh"
	}

	peer, err := testssh.StartEditorPeer(func(command string, stdin io.Reader, stdout, stderr io.Writer) int {
		arguments := []string{"-c", command}

		if filepath.Base(loginShell) == "fish" {
			arguments = append([]string{"--no-config"}, arguments...)
		}

		process := exec.CommandContext(ctx, loginShell, arguments...)
		process.Env = append(
			os.Environ(),
			"ZENTRALE_VSCODE_SERVER_CACHE="+agentCache,
			"ZENTRALE_VSCODE_SERVER_DATA="+filepath.Join(root, "remote-data"),
			"SHELL=/bin/sh",
		)

		process.Stdin, process.Stdout, process.Stderr = stdin, stdout, stderr

		if err := process.Run(); err != nil {
			return 1
		}

		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()

	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	engine, err := agent.New(store, echoProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()

	authentication, err := auth.New(store, "")
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

	machine, err := engine.SaveMachine(domain.Machine{
		ID:      "editor-peer",
		Name:    "Remote SSH test",
		Host:    peer.Host,
		Port:    peer.Port,
		User:    "developer",
		KeyID:   key.ID,
		Sandbox: "none",
	})
	if err != nil {
		t.Fatal(err)
	}

	project, err := engine.SaveProject(domain.Project{
		ID:        "editor-smoke",
		Name:      "Remote workspace",
		Directory: workspace,
		MachineIDs: []string{
			machine.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	frontend, _ := filepath.Abs("../../frontend/dist")
	api := &Server{
		Store:     store,
		Auth:      authentication,
		Engine:    engine,
		Frontend:  frontend,
		Workbench: vscode.NewWorkbench(webCache),
	}

	handler, err := api.Handler()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/_test/disconnect" {
			peer.Close()
			writer.WriteHeader(http.StatusNoContent)

			return
		}

		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()

	command := exec.CommandContext(ctx, "node", "../../frontend/scripts/editor-smoke.mjs", server.URL, project.ID, workspace)
	command.Env = os.Environ()

	if output, err := command.CombinedOutput(); err != nil {
		_ = filepath.Walk(filepath.Join(root, "remote-data"), func(name string, info os.FileInfo, _ error) error {
			if info != nil &&
				!info.IsDir() &&
				strings.Contains(name, "exthost") &&
				strings.HasSuffix(name, ".log") {
				data, _ := os.ReadFile(name)
				if len(data) > 8000 {
					data = data[len(data)-8000:]
				}

				t.Logf("Remote extension host %s:\n%s", filepath.Base(name), data)
			}

			return nil
		})

		t.Fatalf("VS Code browser smoke: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
