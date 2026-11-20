package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

// Called under configMu. Mutations of this project retry rather than racing a
// remote Git transition; unrelated projects and all read queries keep running.
func (engine *Engine) reserveProject(project domain.Project) (func(), error) {
	if engine.ctx.Err() != nil {
		return nil, engine.ctx.Err()
	}
	if engine.projectOperations == nil {
		engine.projectOperations = map[string]domain.Project{}
	}
	if _, busy := engine.projectOperations[project.ID]; busy {
		return nil, errors.New("a repository operation for this project is still in progress")
	}
	engine.projectOperations[project.ID] = project
	engine.workers.Add(1)
	return func() {
		engine.configMu.Lock()
		delete(engine.projectOperations, project.ID)
		engine.configMu.Unlock()
		engine.workers.Done()
	}, nil
}

func (engine *Engine) projectAvailable(id string) error {
	if _, busy := engine.projectOperations[id]; busy {
		return errors.New("a repository operation for this project is still in progress")
	}
	return engine.projectEnvironmentAvailable(id)
}
