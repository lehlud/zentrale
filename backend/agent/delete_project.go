package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// Called with configMu held, so no new agent or preparation can enter while the
// complete project's runtime resources and persisted records are removed.
func (engine *Engine) deleteProject(id string) error {
	if err := engine.projectAvailable(id); err != nil {
		return err
	}
	var project domain.Project
	if err := engine.Store.Load("project", id, &project); err != nil {
		return err
	}
	agents, err := engine.listAll("agent", &id)
	if err != nil {
		return err
	}
	selected := make(map[string]bool, len(agents))
	for _, agent := range agents {
		selected[stringArg(agent, "id")] = true
	}
	// Retain the existing safety contract for running agents/jobs. Check every
	// family before touching anything, rather than deleting idle families first.
	if engine.agentsAreBusy(selected) {
		return errors.New("stop the project agents and their jobs before removing it")
	}
	engine.mu.Lock()
	starting := engine.editorPreparing[id] > 0
	engine.mu.Unlock()
	if starting {
		return errors.New("the project editor is still starting; retry after it finishes")
	}
	if err := engine.Store.RemoveProject(id); err != nil {
		return err
	}
	engine.scheduleRemovalCleanup(selected, id)
	engine.Emit("resources", "", map[string]string{"removedProject": id})
	return nil
}
