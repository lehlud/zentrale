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
	records, err := engine.listAll("agent", &target.ProjectID)
	if err != nil {
		return err
	}
	selected := map[string]bool{agentID: true}
	for changed := true; changed; {
		changed = false
		for _, record := range records {
			id := stringArg(record, "id")
			if selected[stringArg(record, "parentId")] && !selected[id] {
				selected[id] = true
				changed = true
			}
		}
	}
	if engine.agentsAreBusy(selected) {
		return errors.New("stop the agent and its jobs before removing it")
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		engine.closeMCPServers(id)
		if err := engine.Editors.StopAgent(id); err != nil {
			return err
		}
		if err := engine.removeAgentMicroVMs(id); err != nil {
			return err
		}
		engine.invalidate(id)
		engine.releaseConnection(id)
		engine.mu.Lock()
		delete(engine.connections, id)
		engine.mu.Unlock()
		ids = append(ids, id)
	}
	if err := engine.Store.RemoveAgents(ids); err != nil {
		return err
	}
	engine.Emit("resources", "", map[string]string{"removedAgent": agentID})
	return nil
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
