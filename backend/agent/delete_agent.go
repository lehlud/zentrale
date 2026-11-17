package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// Called while configMu is held. A main agent owns its descendants and private
// approvers; deleting one must never leave their history or jobs orphaned.
func (engine *Engine) deleteAgent(agentID string) error {
	var target domain.Agent
	if err := engine.Store.Load("agent", agentID, &target); err != nil {
		return err
	}

	if target.Role == "approver" {
		return errors.New("remove the main agent to remove its approver")
	}

	selected, err := engine.agentFamily(target)
	if err != nil {
		return err
	}

	if engine.agentsAreBusy(selected) {
		return errors.New("stop the agent and its jobs before removing it")
	}

	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	if err := engine.Store.RemoveAgents(ids); err != nil {
		return err
	}
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
