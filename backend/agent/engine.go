// Package agent coordinates inference, independent approval contexts and tools.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/gitforge"
	"github.com/lehlud/zentrale/hosted"
	"github.com/lehlud/zentrale/huggingface"
	"github.com/lehlud/zentrale/internal/keylock"
	"github.com/lehlud/zentrale/modelcatalog"
	"github.com/lehlud/zentrale/providerlogin"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

// NewID creates a compact resource reference, not an authentication token.
func NewID() string {
	return domain.NewID()
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

	Revision uint64
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

	execution       executionRequest
	sandbox         string
	yielded         bool
	connection      *remote.Connection
	launchDirectory string
	daemonReceipt   *daemonJobReceipt
	daemonInput     func(context.Context, []byte, bool) error
	inputReady      chan struct{}

	inputHistory        []string
	inputHistoryOmitted bool
}

// Engine owns application-wide work. configMu serializes configuration and
// lifecycle transitions; mu protects only the short-lived in-memory registries.
// mu is never held while a provider request or remote command is running.
type Engine struct {
	installationID    string
	projectReadiness  sync.Map
	terminalGates     keylock.Group
	resettingProjects map[string]bool
	movingProjects    map[string]bool
	Store             *datastore.Instance
	Remote            *remote.Manager
	Editors           *remote.Editors
	Provider          providers.Client
	Catalog           *modelcatalog.Client
	AccountLogin      *providerlogin.Client
	GitForge          *gitforge.Service
	HuggingFace       *huggingface.Service
	hosted            hostedInference
	deployModal       func(context.Context, string, string, string) (string, error)
	modalDeployments  map[string]*modalDeployment

	accountGates     keylock.Group
	loginPollMu      sync.Mutex
	loginPollers     map[string]*loginPoller
	catalogGates     keylock.Group
	catalogRevisions map[string]uint64

	ctx      context.Context
	cancel   context.CancelFunc
	workers  sync.WaitGroup
	runOwner string

	mu                   sync.Mutex
	running              map[string]context.CancelFunc
	wake                 map[string]chan struct{}
	inputRevisions       map[string]uint64
	instructionRevisions map[string]uint64
	stopRequested        map[string]bool
	agentsRemoving       sync.Map // Agent ID -> pending confirmed removal.

	jobs       map[string]*runningJob
	mcpServers map[string]*mcpServer
	grants     map[string]map[string]grant

	subscribers   map[chan domain.Event]bool
	liveResponses map[string]domain.LiveResponse

	configMu          sync.Mutex
	projectGates      keylock.Group
	projectOperations map[string]domain.Project // Protected by configMu; remote Git operations release the global mutex.
	attachmentGate    chan struct{}
	sideSlots         chan struct{}
	questionMu        sync.Mutex
	teamworkMu        sync.Mutex // Short atomic board transitions, never inference or remote work.
	reviewGates       sync.Map   // Shared approver ID -> cancellable review semaphore.
	inferenceMu       sync.Mutex
	inferencePrefixes map[string]inferencePrefix
	clarificationMu   sync.Mutex
	sandboxMu         sync.Mutex

	sourcePreparations sync.Map // Workspace directory -> checkout semaphore.
	vmOperations       sync.Map // Main agent and machine -> environment lifecycle.

	connections       map[string]*agentConnection
	connectionCleanup map[string]<-chan struct{} // Protected by mu; scoped runtime teardown barriers.
	preparing         map[string]int
	editorPreparing   map[string]int
}

func New(store *datastore.Instance, providerClient providers.Client) (*Engine, error) {
	installationID, _, err := store.InstallationIdentity()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	hostedClient := hosted.NewWithPredictionStore(store)
	engine := &Engine{
		installationID:       installationID,
		Store:                store,
		connections:          make(map[string]*agentConnection),
		preparing:            make(map[string]int),
		editorPreparing:      make(map[string]int),
		catalogRevisions:     make(map[string]uint64),
		Provider:             providerClient,
		Catalog:              modelcatalog.New(),
		AccountLogin:         providerlogin.New(),
		GitForge:             gitforge.New(store),
		HuggingFace:          huggingface.New(store),
		hosted:               hostedClient,
		deployModal:          hosted.DeployModalHumanizer,
		modalDeployments:     make(map[string]*modalDeployment),
		Remote:               remote.NewManager(&remote.Connector{Store: store}),
		ctx:                  ctx,
		runOwner:             NewID(),
		attachmentGate:       make(chan struct{}, 1),
		sideSlots:            make(chan struct{}, 8),
		cancel:               cancel,
		running:              make(map[string]context.CancelFunc),
		wake:                 make(map[string]chan struct{}),
		inputRevisions:       make(map[string]uint64),
		instructionRevisions: make(map[string]uint64),
		stopRequested:        make(map[string]bool),
		jobs:                 make(map[string]*runningJob),
		mcpServers:           make(map[string]*mcpServer),
		grants:               make(map[string]map[string]grant),
		movingProjects:       make(map[string]bool),
		subscribers:          make(map[chan domain.Event]bool),
		liveResponses:        make(map[string]domain.LiveResponse),
	}
	engine.Editors = remote.NewEditors(engine.Remote, func(session domain.EditorSession) {
		engine.scheduleVMIdleCheck(remote.EditorScopeID(session.ProjectID, session.AgentID))
	})
	// Restore ownership of jobs and editors before agent turns can resume.
	recovery := []struct {
		name string
		run  func() error
	}{
		{"shared_approvers", engine.migrateSharedApprovers},
		{"interrupted_work", engine.recoverInterruptedWork},
		{"editors", engine.Editors.Restore},
		{"daemon_jobs", engine.recoverDaemonJobs},
		{"logins", engine.recoverLoginProtocols},
		{"project_moves", engine.recoverProjectMoves},
		{"agent_inputs", engine.recoverInputs},
		{"modal_deployments", engine.recoverModalDeployments},
	}
	timings := make(map[string]int64, len(recovery))
	for _, phase := range recovery {
		started := time.Now()
		if err := phase.run(); err != nil {
			engine.Close()
			return nil, fmt.Errorf("recover %s: %w", phase.name, err)
		}
		timings[phase.name] = time.Since(started).Milliseconds()
	}
	slog.Info("Zentrale recovery complete", "phases_ms", timings)
	engine.workers.Go(engine.superviseVMs)
	engine.workers.Go(engine.cleanupDeletedVMs)
	engine.workers.Go(engine.superviseDaemonStops)
	engine.workers.Go(func() { hostedClient.RecoverPredictions(ctx) })

	return engine, nil
}

// Work from a previous process cannot still be supervised by this process. Keep
// its history and mark interruption before recoverInputs restarts agent turns.
// Jobs are never replayed: their external side effects may already have occurred.
func (engine *Engine) recoverInterruptedWork() error {
	for _, kind := range []string{"agent", "job"} {
		resources, err := engine.Store.UnfinishedWork(kind)
		if err != nil {
			return fmt.Errorf("recover %s resources: %w", kind, err)
		}

		for _, resource := range resources {
			status := stringArg(resource, "status")
			id := stringArg(resource, "id")
			if kind == "agent" {
				state := domain.AgentRunState{AgentID: id, Phase: "interrupted"}
				if _, err := engine.Store.WriteAgentRun("", "interrupted", "", state, false); err != nil {
					return fmt.Errorf("recover agent state: %w", err)
				}
				continue
			}
			if status == "running" {
				if _, err := engine.Store.Credential(daemonReceiptName(id)); err == nil {
					continue
				}
			}

			if err := engine.Store.InterruptJob(id); err != nil {
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
	engine.HuggingFace.Close()
	engine.hosted.Close()

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
	if engine.Remote.Connector.NativeSessionsForTests {
		engine.stopUnusedVMsOnShutdown()
	}
	engine.Remote.Connector.Close()
}
