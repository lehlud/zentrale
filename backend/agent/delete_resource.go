package agent

import (
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) Delete(kind, id string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if kind == "agent" {
		return engine.deleteAgent(id)
	}
	if kind == "project" {
		return engine.deleteProject(id)
	}

	switch kind {
	case "machine", "pool", "project", "source", "provider":
	default:
		return errors.New("unsupported deletion")
	}

	if err := engine.checkResourceReferences(kind, id); err != nil {
		return err
	}

	if kind == "source" {
		var source domain.Source
		if err := engine.Store.Load("source", id, &source); err != nil {
			return err
		}

		if source.ProjectRoot {
			var project domain.Project
			if err := engine.Store.Load("project", source.ProjectID, &project); err != nil {
				return err
			}

			project.GitConnectionID, project.RepositoryID, project.RepositoryName = "", "", ""

			if err := engine.Store.SaveProjectRepository(project, nil); err != nil {
				return err
			}
		}
	}

	if kind == "provider" {
		engine.accountMu.Lock()
		defer engine.accountMu.Unlock()

		engine.catalogRevisions[id]++

		for _, collection := range []string{"model", "model_metadata", "provider_login"} {
			if err := engine.Store.ReplaceCollection(collection, id, nil); err != nil {
				return err
			}
		}
	}

	err := engine.Store.Remove(kind, id)
	engine.Emit("resources", "", nil)

	return err
}

func (engine *Engine) checkResourceReferences(kind, id string) error {
	for _, dependent := range []string{"machine", "pool", "project", "source", "agent", "microvm"} {
		resources, err := engine.listAll(dependent, nil)
		if err != nil {
			return err
		}

		for _, resource := range resources {
			if dependent == kind && stringArg(resource, "id") == id {
				continue
			}

			if kind == "project" && stringArg(resource, "projectId") == id {
				if dependent == "microvm" && stringArg(resource, "agentId") == remote.EditorScopeID(id, "") {
					continue
				}

				return errors.New("project is still referenced")
			}

			if kind == "provider" && stringArg(resource, "providerId") == id {
				return errors.New("provider is still referenced")
			}

			if kind != "machine" && kind != "pool" {
				continue
			}

			references, err := machineReferences(dependent, resource)
			if err != nil {
				return err
			}

			for _, reference := range references {
				if reference == id || strings.HasPrefix(reference, id+"/") {
					return errors.New("resource is still referenced")
				}
			}
		}
	}

	return nil
}

func machineReferences(kind string, resource map[string]any) ([]string, error) {
	switch kind {
	case "project":
		var project domain.Project
		if err := decode(JSON(resource), &project); err != nil {
			return nil, err
		}

		return append(project.MachineIDs, project.PoolIDs...), nil
	case "machine":
		var machine domain.Machine
		if err := decode(JSON(resource), &machine); err != nil {
			return nil, err
		}

		return machine.Jumps, nil
	case "pool":
		var pool domain.Pool
		if err := decode(JSON(resource), &pool); err != nil {
			return nil, err
		}

		var references []string

		for _, machine := range pool.Machines {
			references = append(references, machine.Jumps...)
		}

		return references, nil
	case "agent", "microvm":
		return []string{stringArg(resource, "machineId")}, nil
	default:
		return nil, nil
	}
}
