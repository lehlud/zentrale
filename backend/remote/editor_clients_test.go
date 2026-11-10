package remote

import (
	"context"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func editorClientFixture(t *testing.T) (*Editors, *editorSession, context.Context) {
	t.Helper()
	editors := NewEditors(nil)
	editors.reconnectGrace = 20 * time.Millisecond
	t.Cleanup(editors.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	session := &editorSession{
		project: domain.Project{ID: "project"}, target: EditorTarget{AgentID: "main", MachineID: "worker"},
		info:  domain.EditorSession{ID: "editor", ProjectID: "project", AgentID: "main"},
		ready: make(chan struct{}), done: make(chan struct{}), cancel: cancel,
	}
	editors.mutex.Lock()
	editors.sessions["project:main"] = session
	editors.mutex.Unlock()
	return editors, session, ctx
}

func TestEditorLastClientDisconnectReleasesEnvironment(t *testing.T) {
	editors, _, ctx := editorClientFixture(t)
	first, err := editors.Client("project", "editor")
	if err != nil {
		t.Fatal(err)
	}
	second, err := editors.Client("project", "editor")
	if err != nil {
		t.Fatal(err)
	}
	first()
	first() // Closing an HTTP request twice must not undercount other clients.
	select {
	case <-ctx.Done():
		t.Fatal("closing one workbench stopped another")
	case <-time.After(2 * editors.reconnectGrace):
	}
	if !editors.HasActive(map[string]bool{"main": true}, "worker") {
		t.Fatal("editor did not hold the VM")
	}
	second()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("last workbench did not release its environment")
	}
	if editors.HasActive(map[string]bool{"main": true}, "worker") {
		t.Fatal("expired editor still holds the VM")
	}
}

func TestEditorReconnectAndActiveTransfersDelayRelease(t *testing.T) {
	editors, session, ctx := editorClientFixture(t)
	release, err := editors.Client("project", "editor")
	if err != nil {
		t.Fatal(err)
	}
	release()
	reconnected, err := editors.Client("project", "editor")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("reload stopped the editor")
	case <-time.After(2 * editors.reconnectGrace):
	}
	editors.mutex.Lock()
	session.activeRequests++
	editors.mutex.Unlock()
	reconnected()
	select {
	case <-ctx.Done():
		t.Fatal("active transfer was interrupted")
	case <-time.After(2 * editors.reconnectGrace):
	}
	editors.mutex.Lock()
	session.activeRequests--
	editors.mutex.Unlock()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("finished transfer did not release the editor")
	}
}

func TestOldEditorDisconnectCannotCloseReplacement(t *testing.T) {
	editors, old, oldContext := editorClientFixture(t)
	release, err := editors.Client("project", "editor")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	editors.mutex.Lock()
	editors.sessions["project:main"] = &editorSession{
		project: old.project, target: old.target,
		info: domain.EditorSession{ID: "replacement"}, done: make(chan struct{}), cancel: cancel,
	}
	editors.mutex.Unlock()
	release()
	select {
	case <-ctx.Done():
		t.Fatal("old disconnect closed replacement")
	case <-oldContext.Done():
		t.Fatal("old disconnect touched removed session")
	case <-time.After(2 * editors.reconnectGrace):
	}
}
