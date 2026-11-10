// Package agent coordinates inference, independent approval contexts and tools.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/gitforge"
	"github.com/lehlud/zentrale/modelcatalog"
	"github.com/lehlud/zentrale/providerlogin"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

// NewID creates an opaque resource identifier with at least 128 bits of entropy.
func NewID() string {
	return rand.Text()
}

// JSON serializes internal domain values. Passing a channel, function or cyclic
// object is a programming error; external input is decoded before reaching here.
func JSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Errorf("encode internal domain value: %w", err))
	}

	return string(encoded)
}

type grant struct {
	Path      string
	Hash      string
	MachineID string

	watchID string
	cancel  context.CancelFunc
}

type runningJob struct {
	cancel context.CancelFunc
	done   chan struct{}

	mu                sync.Mutex
	job               domain.Job
	terminationReason string
	input             *jobInput
	output            *remote.OutputBuffer
	changed           chan struct{}

	execution  executionRequest
	sandbox    string
	yielded    bool
	connection *remote.Connection
	project    domain.Project

	inputHistory        []string
	inputHistoryOmitted bool
}

// Engine owns application-wide work. configMu serializes configuration and
// lifecycle transitions; mu protects only the short-lived in-memory registries.
// mu is never held while a provider request or remote command is running.
type Engine struct {
	Store        *datastore.Instance
	Remote       *remote.Manager
	Editors      *remote.Editors
	Provider     providers.Client
	Catalog      *modelcatalog.Client
	AccountLogin *providerlogin.Client
	GitForge     *gitforge.Service

	accountMu        sync.Mutex
	catalogMu        sync.Mutex
	catalogRevisions map[string]uint64

	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup

	mu                   sync.Mutex
	running              map[string]context.CancelFunc
	wake                 map[string]chan struct{}
	inputRevisions       map[string]uint64
	instructionRevisions map[string]uint64
	stopRequested        map[string]bool

	jobs       map[string]*runningJob
	mcpServers map[string]*mcpServer
	grants     map[string]map[string]grant

	subscribers   map[chan domain.Event]bool
	liveResponses map[string]domain.LiveResponse

	configMu  sync.Mutex
	sandboxMu sync.Mutex

	sourcePreparations sync.Map // Workspace directory -> checkout semaphore.
	vmOperations       sync.Map // Main agent and machine -> environment lifecycle.

	connections     map[string]*agentConnection
	preparing       map[string]int
	editorPreparing map[string]int
}

func New(store *datastore.Instance, providerClient providers.Client) (*Engine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{
		Store:                store,
		connections:          make(map[string]*agentConnection),
		preparing:            make(map[string]int),
		editorPreparing:      make(map[string]int),
		catalogRevisions:     make(map[string]uint64),
		Provider:             providerClient,
		Catalog:              modelcatalog.New(),
		AccountLogin:         providerlogin.New(),
		GitForge:             gitforge.New(store),
		Remote:               remote.NewManager(&remote.Connector{Store: store}),
		ctx:                  ctx,
		cancel:               cancel,
		running:              make(map[string]context.CancelFunc),
		wake:                 make(map[string]chan struct{}),
		inputRevisions:       make(map[string]uint64),
		instructionRevisions: make(map[string]uint64),
		stopRequested:        make(map[string]bool),
		jobs:                 make(map[string]*runningJob),
		mcpServers:           make(map[string]*mcpServer),
		grants:               make(map[string]map[string]grant),
		subscribers:          make(map[chan domain.Event]bool),
		liveResponses:        make(map[string]domain.LiveResponse),
	}
	if err := engine.recoverInterruptedWork(); err != nil {
		cancel()
		return nil, err
	}

	engine.Editors = remote.NewEditors(engine.Remote, func(session domain.EditorSession) {
		engine.scheduleVMIdleCheck(remote.EditorScopeID(session.ProjectID, session.AgentID))
	})

	if err := engine.recoverInputs(); err != nil {
		engine.Close()
		return nil, err
	}
	engine.workers.Go(engine.superviseVMs)

	return engine, nil
}

// Work from a previous process cannot still be supervised by this process. Keep
// its history and mark interruption before recoverInputs restarts agent turns.
// Jobs are never replayed: their external side effects may already have occurred.
func (engine *Engine) recoverInterruptedWork() error {
	for _, kind := range []string{"agent", "job"} {
		resources, err := engine.listAll(kind, nil)
		if err != nil {
			return fmt.Errorf("recover %s resources: %w", kind, err)
		}

		for _, resource := range resources {
			status := stringArg(resource, "status")
			if status != "running" && status != "waiting" {
				continue
			}

			resource["status"] = "interrupted"
			resource["error"] = "service restarted"
			parentID := stringArg(resource, "projectId")

			if kind == "job" {
				parentID = stringArg(resource, "agentId")
			}

			if err := engine.Store.Save(kind, stringArg(resource, "id"), parentID, resource); err != nil {
				return fmt.Errorf("recover %s state: %w", kind, err)
			}
		}
	}

	return nil
}

func (engine *Engine) Close() {
	// Serialize cancellation with worker registration. Lifecycle entry points
	// hold configMu while checking ctx and adding to workers; after this barrier
	// none can add an agent worker while shutdown is waiting for them.
	engine.configMu.Lock()
	engine.cancel()
	engine.configMu.Unlock()
	engine.mu.Lock()

	for _, cancel := range engine.running {
		cancel()
	}

	for _, job := range engine.jobs {
		job.cancel()
	}

	engine.mu.Unlock()
	engine.closeMCPServers("")
	engine.Editors.Close()
	engine.workers.Wait()
	engine.mu.Lock()
	ids := make([]string, 0, len(engine.connections))

	for id := range engine.connections {
		ids = append(ids, id)
	}

	engine.mu.Unlock()

	for _, id := range ids {
		engine.releaseConnection(id)
	}
	engine.stopUnusedVMsOnShutdown()
}
