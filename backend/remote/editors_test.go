package remote

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func TestConnectionCheckReturnsTrustChallengeAndVisibleSuccess(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSSHFixture(t, "developer\n")
	machine := fixture.machine("worker", key.ID)
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	connector := &Connector{Store: store}
	result := connector.Check(context.Background(), machine.ID)
	if result.Connected || result.HostKey.Fingerprint == "" || result.HostKey.PublicKey == "" {
		t.Fatalf("missing trust challenge: %+v", result)
	}
	trusted, err := store.IsTrustedSSHKey(fixture.hostKey.Marshal())
	if err != nil || trusted {
		t.Fatal("a connection check must not trust the key")
	}
	raw, err := DecodePublicKey(result.HostKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(raw); err != nil {
		t.Fatal(err)
	}
	result = connector.Check(context.Background(), machine.ID)
	if !result.Connected || result.Error != "" || result.HostKey.PublicKey != "" {
		t.Fatalf("connection result: %+v", result)
	}
}

func TestRetainedSSHHandleOutlivesItsAgent(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSSHFixture(t, "developer\n")
	machine := fixture.machine("worker", key.ID)
	if err := store.Save("machine", machine.ID, "", machine); err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(fixture.hostKey.Marshal()); err != nil {
		t.Fatal(err)
	}
	connection, err := (&Connector{Store: store}).Dial(context.Background(), machine.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := connection.Retain()
	if err != nil {
		t.Fatal(err)
	}
	connection.Close()
	connection.Close()
	if _, err := Run(context.Background(), job.Client, "stream"); err != nil {
		t.Fatalf("finishing an agent disconnected its retained job: %v", err)
	}
	job.Close()
	if _, err := connection.Retain(); err == nil {
		t.Fatal("closed SSH transport was retained")
	}
}

func TestEditorReusesOneSSHSessionAndReleasesItsPoolLease(t *testing.T) {
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSSHFixture(t, "developer\n")
	runtime, err := os.MkdirTemp("/tmp", "ze-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtime) })
	fixture.editorSocket = filepath.Join(runtime, "vscode.sock")
	listener, err := net.Listen("unix", fixture.editorSocket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, request.URL.Path)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	pool := domain.Pool{ID: "pool", Name: "Pool", MaxMachines: 1, SharedHome: true, Machines: []domain.Machine{fixture.machine("worker", key.ID)}}
	if err := store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	if err := store.TrustSSHKey(fixture.hostKey.Marshal()); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(&Connector{Store: store})
	editors := NewEditors(manager)
	defer editors.Close()
	project := domain.Project{ID: "project", Directory: "/srv/projects/forecast", PoolIDs: []string{pool.ID}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var workers sync.WaitGroup
	results := make(chan domain.EditorSession, 6)
	for range 6 {
		workers.Go(func() {
			session, err := editors.Start(ctx, project)
			if err != nil {
				t.Error(err)
				return
			}
			results <- session
		})
	}
	workers.Wait()
	close(results)
	var session domain.EditorSession
	for result := range results {
		if session.ID != "" && session.ID != result.ID {
			t.Fatal("concurrent starts created multiple editors")
		}
		session = result
	}
	if session.ID == "" || session.MachineID != "pool/worker" || session.MachineName != "worker" {
		t.Fatalf("editor session: %+v", session)
	}
	fixture.mutex.Lock()
	starts := fixture.editorStarts
	fixture.mutex.Unlock()
	if starts != 1 {
		t.Fatalf("started %d remote processes", starts)
	}
	second, err := editors.StartFor(ctx, project, EditorTarget{AgentID: "second-agent"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID == session.ID || second.AgentID != "second-agent" {
		t.Fatal("different agents shared an editor session")
	}
	if err := editors.StopAgent("second-agent"); err != nil {
		t.Fatal(err)
	}
	if current := editors.CurrentFor(project, "second-agent"); current.ID != "" {
		t.Fatal("stopped agent editor remained current")
	}
	if current := editors.Current(project); current.ID != session.ID {
		t.Fatal("stopping one agent stopped another editor")
	}
	manager.mutex.Lock()
	lease := manager.leases["pool"]["ide:"+session.ID]
	manager.mutex.Unlock()
	if lease != session.MachineID {
		t.Fatal("editor did not retain its pool lease")
	}
	transport, release, err := editors.Transport(project, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Transport: transport, Timeout: time.Second}).Get("http://vscode/workbench/asset.js")
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	transport.CloseIdleConnections()
	release()
	if err != nil || string(content) != "/workbench/asset.js" {
		t.Fatalf("SSH socket forwarding: %q, %v", content, err)
	}
	project.Name = "Renamed project"
	if reused, err := editors.Start(ctx, project); err != nil || reused.ID != session.ID {
		t.Fatal("a rename restarted the editor")
	}
	if err := editors.Stop(project.ID, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := editors.Transport(project, session.ID); err == nil {
		t.Fatal("stopped session remained accessible")
	}
	manager.mutex.Lock()
	remaining := len(manager.leases["pool"])
	manager.mutex.Unlock()
	if remaining != 0 {
		t.Fatal("stopped editor retained the pool lease")
	}
}

func TestCustomMicroVMEditorDoesNotFallBackToHost(t *testing.T) {
	_, err := prepareEditorRuntime(context.Background(), nil, domain.Machine{Sandbox: SandboxMicroVM, MicroVMCommand: "/custom/runner"}, domain.Project{ID: "project", Directory: "/work/project"}, EditorTarget{}, "/ide/project/session")
	if err == nil {
		t.Fatal("custom MicroVM editor silently selected host execution")
	}
}
