package agent

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

type ProjectConnectionError struct {
	Cause   error
	HostKey domain.SSHHostKey
}

func (failure *ProjectConnectionError) Error() string {
	return "project connection failed: " + failure.Cause.Error()
}

func (failure *ProjectConnectionError) Unwrap() error {
	return failure.Cause
}

type agentConnection struct {
	mutex      sync.Mutex
	connection *remote.Connection
	machine    domain.Machine
	projectKey string
	project    domain.Project
	sourcesKey string
	expiry     *time.Timer
}

func (engine *Engine) connectionEntry(agentID string) *agentConnection {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	if engine.ctx.Err() != nil {
		return nil
	}

	entry := engine.connections[agentID]
	if entry == nil {
		entry = &agentConnection{}
		engine.connections[agentID] = entry
	}

	return entry
}

func (engine *Engine) connect(ctx context.Context, agentRecord domain.Agent) (*remote.Connection, domain.Machine, domain.Project, error) {
	if err := engine.Store.Load("agent", agentRecord.ID, &agentRecord); err != nil {
		return nil, domain.Machine{}, domain.Project{}, err
	}

	var project domain.Project
	if err := engine.Store.Load("project", agentRecord.ProjectID, &project); err != nil {
		return nil, domain.Machine{}, project, err
	}

	entry := engine.connectionEntry(agentRecord.ID)
	if entry == nil {
		return nil, domain.Machine{}, project, engine.ctx.Err()
	}

	entry.mutex.Lock()
	defer entry.mutex.Unlock()

	projectKey := JSON(project)

	if entry.connection != nil {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		alive := engine.canReuseConnection(probe, entry, projectKey, agentRecord.MachineID)
		cancel()

		if !alive {
			engine.closeMCPServers(agentRecord.ID)
			entry.connection.Close()
			entry.connection = nil
			entry.sourcesKey = ""
		}
	}

	if entry.connection == nil {
		connection, machine, err := engine.Remote.Connect(ctx, project, agentRecord.ID, agentRecord.MachineID, nil)
		if err != nil {
			return nil, machine, project, &ProjectConnectionError{Cause: err}
		}

		if err := remote.CheckRuntime(ctx, connection); err != nil {
			connection.Close()
			engine.Remote.Release(agentRecord.ID)

			return nil, machine, project, &ProjectConnectionError{Cause: err}
		}

		engine.setRunState(agentRecord.ID, "workspace", nil)
		resolved, err := engine.Remote.ResolveProject(ctx, connection, machine, project)
		if err == nil {
			machine.StorageMounts, err = engine.Remote.PrepareStorage(ctx, connection, machine, resolved)
		}

		if err != nil {
			connection.Close()
			engine.Remote.Release(agentRecord.ID)

			return nil, machine, project, &ProjectConnectionError{Cause: err}
		}

		entry.connection, entry.machine, entry.projectKey = connection, machine, projectKey
		entry.project = resolved
		entry.sourcesKey = ""

		if agentRecord.MachineID != machine.ID {
			previous := agentRecord.MachineID
			agentRecord.MachineID = machine.ID
			engine.invalidate(agentRecord.ID)

			if err := engine.saveAgent(agentRecord); err != nil {
				connection.Close()
				entry.connection = nil
				engine.Remote.Release(agentRecord.ID)

				return nil, machine, project, err
			}

			if previous != "" {
				engine.Emit("failover", agentRecord.ID, map[string]string{"machineId": machine.ID})
			}
		}
	}

	if project.ManagedWorkspace {
		resolved, err := engine.Remote.ResolveProject(ctx, entry.connection, entry.machine, project)
		if err != nil {
			return nil, entry.machine, project, &ProjectConnectionError{Cause: err}
		}

		entry.project = resolved
	}

	if entry.expiry != nil {
		entry.expiry.Stop()
	}

	entry.expiry = time.AfterFunc(time.Minute, func() {
		engine.mu.Lock()
		running := engine.running[agentRecord.ID] != nil
		engine.mu.Unlock()

		if !running {
			engine.releaseConnection(agentRecord.ID)
		}
	})

	retained, err := entry.connection.Retain()

	return retained, entry.machine, entry.project, err
}

// CheckAgentConnection prepares an already accepted task. Pinning the lifecycle
// prevents deletion while a handshake is in flight;
// the global configuration lock is released before any network operation.
func (engine *Engine) CheckAgentConnection(ctx context.Context, agentID string) domain.MachineConnectionCheck {
	return engine.checkAgentConnection(ctx, agentID, false)
}

func (engine *Engine) checkAgentConnection(ctx context.Context, agentID string, reuseSources bool) domain.MachineConnectionCheck {
	engine.configMu.Lock()
	var record domain.Agent
	err := engine.Store.Load("agent", agentID, &record)
	if err != nil || record.Role == "approver" || engine.ctx.Err() != nil {
		engine.configMu.Unlock()

		if err != nil {
			return domain.MachineConnectionCheck{Error: err.Error()}
		}

		if engine.ctx.Err() != nil {
			return domain.MachineConnectionCheck{Error: engine.ctx.Err().Error()}
		}

		return domain.MachineConnectionCheck{Error: "send instructions to the main agent"}
	}

	engine.mu.Lock()
	engine.preparing[agentID]++
	engine.mu.Unlock()
	engine.configMu.Unlock()
	defer func() {
		engine.mu.Lock()
		engine.preparing[agentID]--

		if engine.preparing[agentID] == 0 {
			delete(engine.preparing, agentID)
		}

		engine.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	engine.setRunState(agentID, "connecting", nil)
	connectContext, cancelConnect := context.WithTimeout(ctx, 3*time.Minute)
	connection, machine, project, err := engine.connect(connectContext, record)
	cancelConnect()
	if err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), HostKey: remote.HostKeyChallenge(err)}
	}
	defer connection.Close()

	// A cached SSH transport does not prove that its VM runtime still boots.
	// Recheck before inference, and retain failures for the machine diagnostics.
	// VM setup has its own budget, including a possible cold boot. It must not
	// consume the time allowed for the subsequent repository inspection.
	sandboxContext, cancelSandbox := context.WithTimeout(ctx, 10*time.Minute)
	engine.setRunState(agentID, "checking_sandbox", nil)
	status := remote.InspectSandbox(sandboxContext, connection, machine)
	if status.Error == "" && status.Level == remote.SandboxMicroVM && machine.MicroVMCommand == "" {
		engine.setRunState(agentID, "sandbox", nil)
		temporary := agentTemporaryDirectory(record.ID)
		if err := prepareRemoteWorkspace(sandboxContext, connection, project.Directory, temporary); err != nil {
			status.Error = err.Error()
		} else if _, err := remote.ManageMicroVM(sandboxContext, connection, machine, "prepare", remote.Execution{
			AgentID:            record.ID,
			ProjectID:          project.ID,
			Workspace:          project.Directory,
			TemporaryDirectory: temporary,
		}); err != nil {
			status.Error = err.Error()
		} else if err := engine.rememberMicroVM(record, machine, project, status.Level); err != nil {
			status.Error = err.Error()
		}
	}
	cancelSandbox()

	if err := engine.rememberSandboxStatus(machine, status); err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), Sandbox: status}
	}

	if status.Error != "" {
		return domain.MachineConnectionCheck{Error: status.Error, Sandbox: status}
	}

	engine.setRunState(agentID, "repository", nil)
	if err := engine.prepareProjectSourcesForConnection(ctx, connection, machine, project, record.ID, reuseSources); err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), Sandbox: status}
	}

	return domain.MachineConnectionCheck{Connected: true, Sandbox: status}
}

func (engine *Engine) requireConnection(ctx context.Context, agentID string) error {
	result := engine.checkAgentConnection(ctx, agentID, true)
	if !result.Connected {
		return &ProjectConnectionError{Cause: errors.New(result.Error), HostKey: result.HostKey}
	}

	return nil
}

func (engine *Engine) releaseConnection(agentID string) {
	engine.mu.Lock()
	entry := engine.connections[agentID]
	engine.mu.Unlock()

	if entry == nil {
		engine.Remote.Release(agentID)
		return
	}

	entry.mutex.Lock()
	defer entry.mutex.Unlock()

	if entry.expiry != nil {
		entry.expiry.Stop()
	}

	if entry.connection != nil {
		entry.connection.Close()
		entry.connection = nil
	}
	entry.sourcesKey = ""

	engine.Remote.Release(agentID)
}

func (engine *Engine) canReuseConnection(
	ctx context.Context,
	cached *agentConnection,
	projectKey, machineID string,
) bool {
	if cached.projectKey != projectKey || cached.machine.ID != machineID {
		return false
	}

	configured, err := engine.Remote.Connector.Machine(machineID)
	if err != nil || JSON(configured) != JSON(cached.machine) {
		return false
	}

	if err := engine.Remote.Connector.VerifyTrust(cached.connection); err != nil {
		return false
	}

	if err := cached.connection.Ping(ctx); err != nil {
		return false
	}

	return remote.CheckAvailability(ctx, cached.connection, cached.machine) == nil
}
