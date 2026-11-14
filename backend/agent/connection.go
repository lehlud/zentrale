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
	mutex      connectionMutex
	connection *remote.Connection
	machine    domain.Machine
	projectKey string
	project    domain.Project
	sourcesKey string
	ready      *connectionReadiness
	expiry     *time.Timer
}

// Connection state sometimes covers remote setup. Waiters must be cancellable
// even when another operation is stuck in that setup. Lock keeps the usual
// interface for short, non-I/O state transitions elsewhere in the engine.
type connectionMutex struct {
	once sync.Once
	gate chan struct{}
}

func (mutex *connectionMutex) LockContext(ctx context.Context) error {
	mutex.once.Do(func() { mutex.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case mutex.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			mutex.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (mutex *connectionMutex) Lock()   { _ = mutex.LockContext(context.Background()) }
func (mutex *connectionMutex) Unlock() { <-mutex.gate }

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
	return engine.connectWithEditorCandidate(ctx, agentRecord, false)
}

// An editor candidate may reuse the resolved workspace only while a complete
// readiness receipt exists. withEditorEnvironment must immediately validate its
// fingerprint and live VM generation, or discard/reconnect it before use.
func (engine *Engine) connectWithEditorCandidate(ctx context.Context, agentRecord domain.Agent, editorCandidate bool) (*remote.Connection, domain.Machine, domain.Project, error) {
	if err := engine.Store.Load("agent", agentRecord.ID, &agentRecord); err != nil {
		return nil, domain.Machine{}, domain.Project{}, err
	}
	owner, err := engine.vmOwner(agentRecord.ID)
	if err != nil {
		return nil, domain.Machine{}, domain.Project{}, err
	}
	preferredMachine := agentRecord.MachineID
	if owner.ID != agentRecord.ID && owner.MachineID != "" {
		preferredMachine = owner.MachineID
	}

	var project domain.Project
	if err := engine.Store.Load("project", agentRecord.ProjectID, &project); err != nil {
		return nil, domain.Machine{}, project, err
	}

	entry := engine.connectionEntry(agentRecord.ID)
	if entry == nil {
		return nil, domain.Machine{}, project, engine.ctx.Err()
	}

	if err := entry.mutex.LockContext(ctx); err != nil {
		return nil, domain.Machine{}, project, err
	}
	locked := true
	defer func() {
		if locked {
			entry.mutex.Unlock()
		}
	}()

	projectKey := JSON(project)

	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.Machine{}, project, err
		}
		if entry.connection != nil {
			original := entry.connection
			retained, retainError := original.Retain()
			snapshot := &agentConnection{connection: retained, machine: entry.machine, projectKey: entry.projectKey}
			// State ownership must not cover SSH traffic: another agent's slow
			// availability/receipt probe must not queue an already warm terminal.
			entry.mutex.Unlock()
			locked = false
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			alive := retainError == nil && engine.canReuseConnection(probe, snapshot, projectKey, preferredMachine)
			cancel()
			if retained != nil {
				retained.Close()
			}
			if err := entry.mutex.LockContext(ctx); err != nil {
				return nil, domain.Machine{}, project, err
			}
			locked = true
			if entry.connection != original {
				continue // Invalidation/reconnect won the race; validate the new state.
			}

			if !alive {
				engine.closeMCPServers(agentRecord.ID)
				entry.connection.Close()
				entry.connection = nil
				entry.sourcesKey = ""
				entry.ready = nil
			}
		}

		reused := entry.connection != nil
		if entry.connection == nil {
			connection, machine, err := engine.Remote.Connect(ctx, project, agentRecord.ID, preferredMachine, nil)
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
			entry.ready = nil

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

		// A fresh connection already resolved the workspace above. Running the
		// remote receipt script twice adds contention to simultaneous child startup.
		if reused && project.ManagedWorkspace && !(editorCandidate && entry.ready != nil) {
			original, machine := entry.connection, entry.machine
			retained, err := original.Retain()
			if err != nil {
				return nil, machine, project, &ProjectConnectionError{Cause: err}
			}
			entry.mutex.Unlock()
			locked = false
			resolved, err := engine.Remote.ResolveProject(ctx, retained, machine, project)
			retained.Close()
			if err := entry.mutex.LockContext(ctx); err != nil {
				return nil, machine, project, err
			}
			locked = true
			if entry.connection != original {
				continue // Never publish a receipt for a replaced transport/scope.
			}
			if err != nil {
				return nil, entry.machine, project, &ProjectConnectionError{Cause: err}
			}

			entry.project = resolved
		}
		break
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
		engine.scheduleVMIdleCheck(agentID)
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	var result domain.MachineConnectionCheck
	// Individual phases retain their independent cold-boot/queue/clone budgets.
	// The retry ceiling must not collapse them back into a shared three minutes.
	err = retryConnectionPreparation(ctx, 45*time.Minute, 250*time.Millisecond, func(attempt context.Context) error {
		var failure error
		result, failure = engine.checkAgentConnectionAttempt(attempt, record, reuseSources)
		return failure
	})
	if err != nil {
		result.Connected = false
		result.Error = err.Error()
	}

	return result
}

// Only harness-owned, idempotent startup preparation is eligible for retry.
// Keep the typed failure until the retry decision; the public diagnostics use
// strings and must not be used to classify authentication or permission errors.
func (engine *Engine) checkAgentConnectionAttempt(ctx context.Context, record domain.Agent, reuseSources bool) (domain.MachineConnectionCheck, error) {
	agentID := record.ID
	if reuseSources {
		engine.setRunState(agentID, "checking_ready", nil)
	} else {
		engine.setRunState(agentID, "connecting", nil)
	}
	connectContext, cancelConnect := context.WithTimeout(ctx, 3*time.Minute)
	connection, machine, project, err := engine.connect(connectContext, record)
	cancelConnect()
	if err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), HostKey: remote.HostKeyChallenge(err)}, err
	}
	defer func() {
		if connection != nil {
			connection.Close()
		}
	}()
	if reuseSources {
		if status, ok := engine.reuseReadyConnection(ctx, record, connection, machine, project); ok {
			engine.setRunState(agentID, "ready", nil)
			return domain.MachineConnectionCheck{Connected: true, Sandbox: status}, nil
		}
		if entry := engine.connectionEntry(record.ID); entry != nil {
			entry.mutex.Lock()
			reconnect := entry.connection == nil
			entry.mutex.Unlock()
			if reconnect {
				connection.Close()
				reconnectContext, cancel := context.WithTimeout(ctx, 3*time.Minute)
				connection, machine, project, err = engine.connect(reconnectContext, record)
				cancel()
				if err != nil {
					return domain.MachineConnectionCheck{Error: err.Error(), HostKey: remote.HostKeyChallenge(err)}, err
				}
			}
		}
	} else if entry := engine.connectionEntry(record.ID); entry != nil {
		entry.mutex.Lock()
		entry.ready = nil
		entry.mutex.Unlock()
	}

	// A cached SSH transport does not prove that its VM runtime still boots.
	// Recheck before inference, and retain failures for the machine diagnostics.
	// VM setup has its own budget, including a possible cold boot. It must not
	// consume the time allowed for the subsequent repository inspection.
	sandboxContext, cancelSandbox := context.WithTimeout(ctx, remote.ManagedVMSetupTimeout)
	engine.setRunState(agentID, "checking_sandbox", nil)
	status := remote.InspectSandbox(sandboxContext, connection, machine)
	var preparationError error
	preparedBootID := ""
	if status.Error == "" && status.Level == remote.SandboxMicroVM && machine.MicroVMCommand == "" {
		engine.setRunState(agentID, "sandbox", nil)
		temporary, err := engine.temporaryDirectory(record.ID)
		if err != nil {
			status.Error = err.Error()
			preparationError = err
		} else if err := prepareRemoteWorkspace(sandboxContext, connection, project.Directory, temporary); err != nil {
			status.Error = err.Error()
			preparationError = err
		} else if err := engine.rememberMicroVM(record, machine, project, status.Level); err != nil {
			status.Error = err.Error()
			preparationError = err
		} else if proof, err := engine.manageVM(sandboxContext, connection, machine, "prepare", remote.Execution{
			AgentID:            record.ID,
			ProjectID:          project.ID,
			Workspace:          project.Directory,
			TemporaryDirectory: temporary,
		}); err != nil {
			status.Error = err.Error()
			preparationError = err
		} else {
			preparedBootID = stringArg(proof, "bootId")
		}
	}
	cancelSandbox()

	if err := engine.rememberSandboxStatus(machine, status); err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), Sandbox: status}, err
	}

	if status.Error != "" {
		if preparationError == nil {
			// InspectSandbox exposes a diagnostic string only. Do not guess
			// whether its failure is retryable from its wording.
			preparationError = errors.New(status.Error)
		}
		return domain.MachineConnectionCheck{Error: status.Error, Sandbox: status}, preparationError
	}

	if err := engine.prepareProjectSourcesForConnection(ctx, connection, machine, project, record.ID, reuseSources); err != nil {
		return domain.MachineConnectionCheck{Error: err.Error(), Sandbox: status}, err
	}
	engine.rememberReadyConnection(ctx, record, connection, machine, project, status, preparedBootID)

	return domain.MachineConnectionCheck{Connected: true, Sandbox: status}, nil
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
	engine.releaseConnectionLocked(agentID, entry)
}

// Caller owns entry.mutex; keep the state/lease release atomic with the
// snapshot comparison used by concurrent readiness validation.
func (engine *Engine) releaseConnectionLocked(agentID string, entry *agentConnection) {
	if entry.expiry != nil {
		entry.expiry.Stop()
	}

	if entry.connection != nil {
		entry.connection.Close()
		entry.connection = nil
	}
	entry.sourcesKey = ""
	entry.ready = nil

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
