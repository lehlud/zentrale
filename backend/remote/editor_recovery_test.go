package remote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestDaemonEditorRestoresSameProcessAndWorkbench(t *testing.T) {
	connector, store := daemonTestConnector(t)
	project := domain.Project{ID: "editor-project", Name: "Editor", Directory: t.TempDir(), MachineIDs: []string{"daemon-host"}}
	if err := store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	connection, err := connector.Dial(ctx, "daemon-host")
	if err != nil {
		t.Fatal(err)
	}
	starts := filepath.Join(t.TempDir(), "starts")
	editors := NewEditors(NewManager(connector))
	session := &editorSession{project: project, target: EditorTarget{MachineID: "daemon-host"}, info: domain.EditorSession{ID: domain.NewID(), ProjectID: project.ID, MachineID: "daemon-host", URL: "/ide/stable-workbench"}, runtime: editorRuntime{command: "printf x >> " + Quote(starts) + "; printf 'ZENTRALE_EDITOR_READY {\"socketPath\":\"/tmp/ze-editor-fixture/vscode.sock\"}\\n'; cat", sandbox: SandboxDisabled}}
	started := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runEditorProcess(editors.durableEditorContext(editors.ctx, session, connection, false), connection, session.runtime.command, func(output string) {
			if strings.Contains(output, "ZENTRALE_EDITOR_READY") {
				select {
				case started <- struct{}{}:
				default:
				}
			}
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	editors.Close()
	<-done
	connector.Close()
	restarted := NewEditors(NewManager(connector))
	defer restarted.Close()
	if err := restarted.Restore(); err != nil {
		t.Fatal(err)
	}
	for {
		current := restarted.Current(project)
		if current.ID != "" {
			if current.ID != session.info.ID || current.URL != session.info.URL {
				t.Fatalf("workbench identity changed: %+v", current)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	value, err := os.ReadFile(starts)
	if err != nil || string(value) != "x" {
		t.Fatalf("editor was started again: %q %v", value, err)
	}
}
