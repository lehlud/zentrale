package agent

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type concurrencyTransport func(*http.Request) (*http.Response, error)

func (transport concurrencyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestSlowCatalogDoesNotBlockOtherProvidersOrConfiguration(t *testing.T) {
	engine, _ := testEngine(t, nil)
	for _, id := range []string{"slow", "fast"} {
		if err := engine.Store.Save("provider", id, "", domain.Provider{ID: id, Kind: "openai", AuthMode: "api", BaseURL: "https://" + id + ".invalid", Credential: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	engine.Catalog.HTTP = &http.Client{Transport: concurrencyTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == "slow.invalid" {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-5","object":"model"}]}`)), Header: http.Header{}}, nil
	})}
	slow := make(chan error, 1)
	go func() { slow <- engine.RefreshProviderModels(context.Background(), "slow") }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow catalog did not start")
	}
	fast := make(chan error, 1)
	go func() { fast <- engine.RefreshProviderModels(context.Background(), "fast") }()
	select {
	case err := <-fast:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("another provider waited for slow discovery")
	}
	if _, err := engine.SandboxStatuses(); err != nil {
		t.Fatal(err)
	}
	once.Do(func() { close(release) })
	if err := <-slow; err != nil {
		t.Fatal(err)
	}
}

func TestDeletionCommitsWithoutWaitingForRemoteTeardown(t *testing.T) {
	engine, main := testEngine(t, nil)
	entry := engine.connectionEntry(main.ID)
	entry.mutex.Lock()
	var once sync.Once
	defer once.Do(entry.mutex.Unlock)
	if err := engine.Store.Save("microvm", main.ID+":missing", main.ID, microVMRecord{AgentID: main.ID, ProjectID: main.ProjectID, MachineID: "missing", Workspace: "/unchanged"}); err != nil {
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- engine.Delete("agent", main.ID) }()
	select {
	case err := <-deleted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("deletion waited for remote connection teardown")
	}
	var record domain.Agent
	if err := engine.Store.Load("agent", main.ID, &record); err != sql.ErrNoRows {
		t.Fatal("logical deletion failed", err)
	}
	checks := make(chan error, 1)
	go func() { _, err := engine.SandboxStatuses(); checks <- err }()
	select {
	case err := <-checks:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read-only query blocked behind deletion")
	}
	tasks, err := engine.listAll("vm_cleanup", nil)
	if err != nil || len(tasks) != 1 || tasks[0]["ownerId"] != main.ID {
		t.Fatal("cleanup not durable", tasks, err)
	}
}

func TestMachineDeletionCascadesConnectionsAndPreservesConversations(t *testing.T) {
	engine, main := testEngine(t, nil)
	for _, machine := range []domain.Machine{{ID: "gateway"}, {ID: "jump-child", Jumps: []string{"gateway"}}, {ID: "jump-grandchild", Jumps: []string{"jump-child"}}, {ID: "survivor"}} {
		if err := engine.Store.Save("machine", machine.ID, "", machine); err != nil {
			t.Fatal(err)
		}
	}
	pool := domain.Pool{ID: "pool", Machines: []domain.Machine{{ID: "dependent", Jumps: []string{"jump-child"}}, {ID: "independent"}}}
	if err := engine.Store.Save("pool", pool.ID, "", pool); err != nil {
		t.Fatal(err)
	}
	var project domain.Project
	if err := engine.Store.Load("project", main.ProjectID, &project); err != nil {
		t.Fatal(err)
	}
	project.MachineIDs = []string{"jump-grandchild", "survivor"}
	project.PoolIDs = []string{"pool"}
	if err := engine.Store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	main.MachineID = "jump-grandchild"
	if err := engine.saveAgent(main); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	engine.preparing[main.ID] = 1
	engine.mu.Unlock()
	err := engine.Delete("machine", "gateway")
	engine.mu.Lock()
	delete(engine.preparing, main.ID)
	engine.mu.Unlock()
	if err == nil {
		t.Fatal("removed machine under active preparation")
	}
	if err := engine.Store.Load("machine", "gateway", &domain.Machine{}); err != nil {
		t.Fatal("partial deletion", err)
	}
	if err := engine.Delete("machine", "gateway"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gateway", "jump-child", "jump-grandchild"} {
		if err := engine.Store.Load("machine", id, &domain.Machine{}); err != sql.ErrNoRows {
			t.Fatal("dependent connection retained", id, err)
		}
	}
	if err := engine.Store.Load("pool", pool.ID, &pool); err != nil || len(pool.Machines) != 1 || pool.Machines[0].ID != "independent" {
		t.Fatal(pool, err)
	}
	if err := engine.Store.Load("project", project.ID, &project); err != nil || len(project.MachineIDs) != 1 || project.MachineIDs[0] != "survivor" || len(project.PoolIDs) != 1 {
		t.Fatal(project, err)
	}
	if err := engine.Store.Load("agent", main.ID, &main); err != nil || main.MachineID != "" {
		t.Fatal("conversation lost or stale machine retained", main, err)
	}
	if err := engine.Delete("pool", pool.ID); err != nil {
		t.Fatal(err)
	}
	if err := engine.Store.Load("project", project.ID, &project); err != nil || len(project.PoolIDs) != 0 || len(project.MachineIDs) != 1 {
		t.Fatal(project, err)
	}
}
