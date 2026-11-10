package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// Retry idle cleanup after connection failures, including environments left by
// an interrupted server. Old per-subagent guests are no longer used.
func (engine *Engine) superviseVMs() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		engine.reconcileVMs(engine.ctx)
		select {
		case <-engine.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (engine *Engine) reconcileVMs(ctx context.Context) {
	records, err := engine.listAll("microvm", nil)
	if err != nil {
		return
	}

	families := make(map[string]map[string]bool)
	var workers sync.WaitGroup
	limit := make(chan struct{}, 8)

	for _, record := range records {
		id := stringArg(record, "agentId")
		owner, err := engine.vmOwner(id)
		if err != nil {
			continue
		}

		var family map[string]bool // An obsolete subagent scope has no users.
		if owner.ID == id {
			family = families[id]
			if family == nil {
				family, err = engine.agentFamily(owner)
				if err != nil {
					continue
				}
				families[id] = family
			}
		}

		workers.Go(func() {
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}

			// Each host gets its own budget. An unreachable host must not consume
			// every later environment's opportunity to release its resources.
			stopContext, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if err := engine.stopVMRecord(stopContext, record, family); err != nil && ctx.Err() == nil {
				slog.Warn("release unused environment", "agent", id, "error", err)
			}
		})
	}
	workers.Wait()
}

// Brief user inspections and command preparation hold the environment too.
func (engine *Engine) useEnvironment(agentID string) func() {
	engine.mu.Lock()
	engine.preparing[agentID]++
	engine.mu.Unlock()

	return func() {
		engine.mu.Lock()
		engine.preparing[agentID]--
		if engine.preparing[agentID] == 0 {
			delete(engine.preparing, agentID)
		}
		engine.mu.Unlock()

		engine.scheduleVMIdleCheck(agentID)
	}
}

func (engine *Engine) scheduleVMIdleCheck(agentID string) {
	if engine.ctx.Err() != nil {
		return
	}

	owner, err := engine.vmOwner(agentID)
	if err != nil {
		return
	}

	engine.mu.Lock()
	if engine.ctx.Err() != nil {
		engine.mu.Unlock()
		return
	}
	engine.workers.Add(1)
	engine.mu.Unlock()

	go func() {
		defer engine.workers.Done()
		ctx, cancel := context.WithTimeout(engine.ctx, 30*time.Second)
		defer cancel()
		if err := engine.stopIdleVMs(ctx, owner); err != nil && ctx.Err() == nil {
			slog.Warn("stop idle environment", "agent", owner.ID, "error", err)
		}
	}()
}

func (engine *Engine) stopIdleVMs(ctx context.Context, owner domain.Agent) error {
	family, err := engine.agentFamily(owner)
	if err != nil {
		return err
	}

	var records []map[string]any
	for id := range family {
		items, err := engine.listAll("microvm", &id)
		if err != nil {
			return err
		}

		records = append(records, items...)
	}

	return engine.stopVMRecords(ctx, records, family)
}

func (engine *Engine) environmentBusy(family map[string]bool, machineID string) bool {
	machines := make(map[string]string, len(family))
	for id := range family {
		var record domain.Agent
		if engine.Store.Load("agent", id, &record) == nil {
			machines[id] = record.MachineID
		}
	}

	engine.mu.Lock()
	busy := false
	for id := range family {
		workingHere := engine.running[id] != nil && (machines[id] == "" || machines[id] == machineID)
		if engine.preparing[id] > 0 || workingHere {
			busy = true
			break
		}
	}

	if !busy {
		for _, running := range engine.jobs {
			running.mu.Lock()
			owned := family[running.job.AgentID] && (running.job.MachineID == "" || running.job.MachineID == machineID)
			running.mu.Unlock()

			if owned {
				busy = true
				break
			}
		}
	}
	engine.mu.Unlock()

	return busy || engine.Editors.HasActive(family, machineID)
}

// A stop preserves the disk. Starting another action takes this same lock, so
// new work either keeps the guest alive or boots it after the stop completes.
func (engine *Engine) stopVMRecords(ctx context.Context, records []map[string]any, family map[string]bool) error {
	var workers sync.WaitGroup
	var failures []error
	var mutex sync.Mutex
	limit := make(chan struct{}, 8)

	for _, record := range records {
		workers.Go(func() {
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				return
			}

			if err := engine.stopVMRecord(ctx, record, family); err != nil {
				mutex.Lock()
				failures = append(failures, err)
				mutex.Unlock()
			}
		})
	}
	workers.Wait()

	return errors.Join(append(failures, ctx.Err())...)
}

func (engine *Engine) stopVMRecord(ctx context.Context, record map[string]any, family map[string]bool) error {
	agentID, machineID := stringArg(record, "agentId"), stringArg(record, "machineId")
	operation := engine.vmOperation(agentID, machineID)
	if err := operation.Lock(ctx); err != nil {
		return err
	}
	defer operation.Unlock()

	if operation.stopped || (family != nil && engine.environmentBusy(family, machineID)) {
		return nil
	}
	if family != nil {
		engine.closeMCPServersMatching(func(server *mcpServer) bool {
			return family[server.agentID] && server.info.MachineID == machineID
		})
	}

	connection, err := engine.Remote.Connector.Dial(ctx, machineID)
	if err != nil {
		return err
	}
	defer connection.Close()

	machine, err := engine.Remote.Connector.Machine(machineID)
	if err != nil {
		return err
	}
	_, err = remote.ManageMicroVM(ctx, connection, machine, "stop", remote.Execution{
		AgentID:            agentID,
		ProjectID:          stringArg(record, "projectId"),
		Workspace:          stringArg(record, "workspace"),
		TemporaryDirectory: agentTemporaryDirectory(agentID),
	})
	if err == nil {
		operation.stopped = true
	}
	return err
}

func (engine *Engine) stopUnusedVMsOnShutdown() {
	records, err := engine.listAll("microvm", nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := engine.stopVMRecords(ctx, records, nil); err != nil {
		slog.Warn("stop environments during shutdown", "error", err)
	}
}
