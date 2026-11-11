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

	ids, err := engine.prepareAgentRemoval(selected)
	if err != nil {
		return err
	}
	if err := engine.Store.RemoveAgents(ids); err != nil {
		return err
	}

	engine.Emit("resources", "", map[string]string{"removedAgent": agentID})
	return nil
}

// Release runtime resources before committing any database deletions. The caller
// holds configMu and has checked the complete set for active work.
func (engine *Engine) prepareAgentRemoval(selected map[string]bool) ([]string, error) {
	ids := make([]string, 0, len(selected))

	for id := range selected {
		engine.closeMCPServers(id)

		if err := engine.Editors.StopAgent(id); err != nil {
			return nil, err
		}

		if err := engine.removeAgentMicroVMs(id); err != nil {
			return nil, err
		}

		engine.invalidate(id)
		engine.releaseConnection(id)
		engine.mu.Lock()
		delete(engine.connections, id)
		engine.mu.Unlock()
		ids = append(ids, id)
	}

	return ids, nil
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
