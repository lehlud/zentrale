package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var errAgentRemoving = errors.New("this agent is being removed")

func (engine *Engine) agentRemoving(agentID string) bool {
	_, removing := engine.agentsRemoving.Load(agentID)
	return removing
}

// Called while configMu is held. A main agent owns its descendants and private
// approvers; deleting one must never leave their history or jobs orphaned.
func (engine *Engine) agentRemovalTargets(agentID string) (map[string]bool, error) {
	var target domain.Agent
	if err := engine.Store.Load("agent", agentID, &target); err != nil {
		return nil, err
	}

	if target.Role == "approver" {
		return nil, errors.New("remove the main agent to remove its approver")
	}

	selected, err := engine.agentFamily(target)
	if err != nil {
		return nil, err
	}
	for id := range selected {
		if engine.agentRemoving(id) {
			return nil, errAgentRemoving
		}
	}
	return selected, nil
}

func (engine *Engine) AgentRemovalBusy(agentID string) (bool, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	selected, err := engine.agentRemovalTargets(agentID)
	return err == nil && engine.agentsAreBusy(selected), err
}

func (engine *Engine) deleteAgent(agentID string) error {
	selected, err := engine.agentRemovalTargets(agentID)
	if err != nil {
		return err
	}
	if engine.agentsAreBusy(selected) {
		return errors.New("stop the agent and its jobs before removing it")
	}
	return engine.removeAgentRecords(agentID, selected)
}

// StopAndDeleteAgent requires the user's confirmation. Hold configMu only for
// lifecycle transitions so canceled workers can finish and other requests run.
func (engine *Engine) StopAndDeleteAgent(ctx context.Context, agentID string) error {
	engine.configMu.Lock()
	if err := engine.ctx.Err(); err != nil {
		engine.configMu.Unlock()
		return err
	}
	selected, err := engine.agentRemovalTargets(agentID)
	if err != nil {
		engine.configMu.Unlock()
		return err
	}
	for id := range selected {
		engine.agentsRemoving.Store(id, true)
	}
	engine.workers.Add(1)
	engine.configMu.Unlock()
	defer engine.workers.Done()
	defer func() {
		for id := range selected {
			engine.agentsRemoving.Delete(id)
		}
	}()

	for id := range selected {
		if err := engine.Stop(id); err != nil {
			return err
		}
	}
	// A job whose daemon is unreachable keeps its independent job_stop receipt.
	// runJob writes this before dropping its pin; removing history retains it.
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		engine.configMu.Lock()
		if err := engine.ctx.Err(); err != nil {
			engine.configMu.Unlock()
			return err
		}
		if !engine.agentsAreBusy(selected) {
			err := engine.removeAgentRecords(agentID, selected)
			engine.configMu.Unlock()
			return err
		}
		engine.configMu.Unlock()
		select {
		case <-wait.Done():
			return errors.New("the agent is still stopping; retry removing it")
		case <-engine.ctx.Done():
			return engine.ctx.Err()
		case <-poll.C:
		}
	}
}

func (engine *Engine) removeAgentRecords(agentID string, selected map[string]bool) error {
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	if err := engine.Store.RemoveAgents(ids); err != nil {
		return err
	}
	engine.mu.Lock()
	for id := range selected {
		delete(engine.stopRequested, id)
	}
	engine.mu.Unlock()
	engine.scheduleRemovalCleanup(selected, "")

	engine.Emit("resources", "", map[string]string{"removedAgent": agentID})
	return nil
}

// Register while configMu protects shutdown, then close only the removed
// resources outside that mutex. Logical deletion never waits for a remote host.
func (engine *Engine) scheduleRemovalCleanup(selected map[string]bool, projectID string) {
	previous := map[string]<-chan struct{}{}
	completed := map[string]chan struct{}{}
	engine.mu.Lock()
	if engine.connectionCleanup == nil {
		engine.connectionCleanup = map[string]<-chan struct{}{}
	}
	for id := range selected {
		previous[id] = engine.connectionCleanup[id]
		completed[id] = make(chan struct{})
		engine.connectionCleanup[id] = completed[id]
	}
	engine.mu.Unlock()
	engine.workers.Add(1)
	go func() {
		defer engine.workers.Done()
		if projectID != "" {
			_ = engine.Editors.StopProject(projectID)
		}
		for id := range selected {
			if wait := previous[id]; wait != nil {
				<-wait
			}
			engine.closeMCPServers(id)
			engine.invalidate(id)
			engine.releaseConnection(id)
			engine.mu.Lock()
			delete(engine.connections, id)
			if engine.connectionCleanup[id] == completed[id] {
				delete(engine.connectionCleanup, id)
			}
			close(completed[id])
			engine.mu.Unlock()
		}
	}()
}

func (engine *Engine) agentsAreBusy(selected map[string]bool) bool {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	for id := range selected {
		if engine.running[id] != nil || engine.preparing[id] > 0 {
			return true
		}
	}

	for _, running := range engine.jobs {
		running.mu.Lock()
		owned := selected[running.job.AgentID]
		running.mu.Unlock()

		if owned {
			return true
		}
	}

	return false
}
