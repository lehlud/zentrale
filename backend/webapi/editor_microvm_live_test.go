package webapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/agent"
	"github.com/lehlud/zentrale/auth"
	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
	"github.com/lehlud/zentrale/vscode"
)

// This opt-in test uses only a disposable database copy and newly owned remote
// files/VM scopes. It opens the official workbench and a real Fedora terminal.
func TestVSCodeWorkbenchInsideMicroVMLive(t *testing.T) {
	snapshot := os.Getenv("ZENTRALE_VSCODE_MICROVM_TEST_SNAPSHOT")
	machineID := os.Getenv("ZENTRALE_MICROVM_TEST_MACHINE")
	webDirectory := os.Getenv("ZENTRALE_VSCODE_SMOKE_WEB")
	if snapshot == "" || machineID == "" || webDirectory == "" {
		t.Skip("set ZENTRALE_VSCODE_MICROVM_TEST_SNAPSHOT, ZENTRALE_MICROVM_TEST_MACHINE and ZENTRALE_VSCODE_SMOKE_WEB for the real browser/SSH/KVM test")
	}

	store, err := datastore.Open(snapshot)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })
	hash, err := auth.Hash("isolated editor smoke password")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.SetSetting("login_argon2id", hash); err != nil {
		t.Fatal(err)
	}

	authentication, err := auth.New(store, "")
	if err != nil {
		t.Fatal(err)
	}

	engine, err := agent.New(store, echoProvider{})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(engine.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	connection, err := engine.Remote.Connector.Dial(ctx, machineID)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { connection.Close() })
	base, err := remote.Run(ctx, connection.Client, "mktemp -d /tmp/zentrale-vscode-microvm-XXXXXXXX")
	if err != nil {
		t.Fatal(err)
	}

	base = strings.TrimSpace(base)
	if !strings.HasPrefix(base, "/tmp/zentrale-vscode-microvm-") || strings.ContainsAny(base, "\r\n") {
		t.Fatal("invalid owned test directory")
	}

	projectID, agentID := "vm-editor-"+agent.NewID(), "vm-agent-"+agent.NewID()
	workspace, temporary := base+"/project", "/tmp/zentrale-"+agentID
	machine, err := engine.Remote.Connector.Machine(machineID)
	if err != nil {
		t.Fatal(err)
	}

	machine.Sandbox, machine.MicroVMCommand = remote.SandboxMicroVM, ""

	if _, err := engine.SaveMachine(machine); err != nil {
		t.Fatal(err)
	}

	scope := remote.Execution{
		AgentID:            agentID,
		ProjectID:          projectID,
		Workspace:          workspace,
		TemporaryDirectory: temporary,
	}

	t.Cleanup(func() {
		_ = engine.Editors.StopProject(projectID)
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()

		if _, err := remote.ManageMicroVM(cleanup, connection, machine, "remove", scope); err != nil {
			t.Error(err)
		}

		if _, err := remote.Run(cleanup, connection.Client, "rm -rf -- "+remote.Quote(base)+" "+remote.Quote(temporary)); err != nil {
			t.Error(err)
		}
	})
	command := "mkdir -p -- " + remote.Quote(workspace) + " && printf %s " + remote.Quote("print('remote workspace')\n") + " > " + remote.Quote(workspace+"/remote.py") + " && printf %s " + remote.Quote("const answer: number = 'wrong';\n") + " > " + remote.Quote(workspace+"/remote.ts")
	if _, err := remote.Run(ctx, connection.Client, command); err != nil {
		t.Fatal(err)
	}

	project, err := engine.SaveProject(domain.Project{
		ID:        projectID,
		Name:      "VS Code Fedora MicroVM",
		Directory: workspace,
		MachineIDs: []string{
			machineID,
		},
		CommitName:  "VM Project Author",
		CommitEmail: "vm-project@example.test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.Save(
		"agent",
		agentID,
		projectID,
		domain.Agent{
			ID:        agentID,
			ProjectID: projectID,
			Name:      "Fedora workspace",
			Role:      "main",
			Status:    "idle",
			MachineID: machineID,
		},
	); err != nil {
		t.Fatal(err)
	}

	cache := filepath.Join(t.TempDir(), "workbench")
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(webDirectory, filepath.Join(cache, vscode.CurrentRelease().Commit)); err != nil {
		t.Fatal(err)
	}

	frontend, _ := filepath.Abs("../../frontend/dist")
	api := &Server{
		Store:     store,
		Auth:      authentication,
		Engine:    engine,
		Frontend:  frontend,
		Workbench: vscode.NewWorkbench(cache),
	}

	handler, err := api.Handler()
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/_test/") {
			if !authentication.Valid(api.token(request)) {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}

			switch request.URL.Path {
			case "/_test/workspace":
				file, err := remote.Workspace(workspace, request.URL.Query().Get("file"))
				if err != nil {
					writer.WriteHeader(http.StatusBadRequest)
					return
				}

				content, err := remote.Run(request.Context(), connection.Client, "cat -- "+remote.Quote(file))
				if err != nil {
					writer.WriteHeader(http.StatusNotFound)
					return
				}

				_, _ = fmt.Fprint(writer, content)
			case "/_test/disconnect":
				if _, err := remote.ManageMicroVM(request.Context(), connection, machine, "stop", scope); err != nil {
					http.Error(writer, err.Error(), http.StatusInternalServerError)
					return
				}

				writer.WriteHeader(http.StatusNoContent)
			default:
				writer.WriteHeader(http.StatusNotFound)
			}

			return
		}

		handler.ServeHTTP(writer, request)
	}))
	defer server.Close()

	process := exec.CommandContext(
		ctx,
		"node",
		"../../frontend/scripts/editor-smoke.mjs",
		server.URL,
		project.ID,
		workspace,
		"microvm",
	)

	process.Env = os.Environ()
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("Fedora workbench browser: %v\n%s", err, output)
	}

	t.Log("Fedora workbench verified: remote file save, real terminal, project commit identity, TypeScript diagnostics, languages/themes and central assets after tunnel loss")
}
