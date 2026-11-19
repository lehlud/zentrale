package agent

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) SaveMachine(machine domain.Machine) (domain.Machine, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if machine.ID == "" {
		machine.ID = NewID()
	}

	machine = remote.WithMachineDefaults(machine)
	if err := remote.ValidateMachine(machine); err != nil {
		return machine, err
	}

	if _, err := engine.Store.PrivateKey(machine.KeyID); err != nil {
		return machine, errors.New("unknown SSH key")
	}

	visited := map[string]bool{machine.ID: true}
	var visit func(string) error
	visit = func(id string) error {
		if visited[id] {
			return errors.New("cyclic jump hosts")
		}

		visited[id] = true
		defer delete(visited, id)

		candidate, err := engine.Remote.Connector.Machine(id)
		if err != nil {
			return err
		}

		for _, jump := range candidate.Jumps {
			if err := visit(jump); err != nil {
				return err
			}
		}

		return nil
	}

	for _, jump := range machine.Jumps {
		if err := visit(jump); err != nil {
			return machine, err
		}
	}

	err := engine.Store.SaveWithSSHKeys(
		"machine",
		machine.ID,
		"",
		machine,
		datastore.SSHKeyClaim{
			KeyID:     machine.KeyID,
			Purpose:   "machine",
			PurposeID: machine.ID,
		},
	)

	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return machine, err
}

func (engine *Engine) SavePool(data string) (domain.Pool, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	pool, err := remote.ParsePool(data)
	if err != nil {
		return pool, err
	}

	for _, machine := range pool.Machines {
		for _, jump := range machine.Jumps {
			if strings.HasPrefix(jump, pool.ID+"/") {
				return pool, errors.New("pool jump hosts must reference standalone machines")
			}

			if _, err := engine.Remote.Connector.Machine(jump); err != nil {
				return pool, err
			}
		}
	}

	pool, err = engine.Store.SaveManagedPool(pool)
	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return pool, err
}

func (engine *Engine) SaveProject(project domain.Project) (domain.Project, error) {
	return engine.SaveProjectContext(context.Background(), project)
}

func (engine *Engine) SaveProjectContext(ctx context.Context, project domain.Project) (domain.Project, error) {
	if project.ID == "" {
		project.ID = NewID()
	}
	unlock, gateErr := engine.projectGates.LockContext(ctx, project.ID)
	if gateErr != nil {
		return project, gateErr
	}
	defer unlock()
	engine.configMu.Lock()
	locked := true
	defer func() {
		if locked {
			engine.configMu.Unlock()
		}
	}()

	project.CommitName, project.CommitEmail = strings.TrimSpace(project.CommitName), strings.TrimSpace(project.CommitEmail)
	for index, directory := range project.RepositoryCheckoutPaths {
		directory = strings.TrimRight(directory, "/")
		if directory == "" || directory == "." || directory == ".." || path.IsAbs(directory) ||
			path.Clean(directory) != directory || strings.HasPrefix(directory, "../") ||
			strings.ContainsAny(directory, "\x00\r\n\\") {
			return project, errors.New("checkout paths must be repository-relative directory names")
		}
		project.RepositoryCheckoutPaths[index] = directory
	}

	if project.CommitName != "" || project.CommitEmail != "" {
		identity, err := domain.NormalizeCommitIdentity(domain.CommitIdentity{
			Name:  project.CommitName,
			Email: project.CommitEmail,
		})
		if err != nil {
			return project, err
		}

		project.CommitName, project.CommitEmail = identity.Name, identity.Email
	}

	if project.ID == "" {
		project.ID = NewID()
	}

	if !remote.ValidID(project.ID) || project.Name == "" {
		return project, errors.New("project id and name are required")
	}

	var previous domain.Project
	if err := engine.Store.Load("project", project.ID, &previous); err == nil {
		if (previous.RepositoryID != "" || previous.RepositorySSHURL != "") &&
			(previous.RepositoryID != project.RepositoryID || previous.GitConnectionID != project.GitConnectionID ||
				previous.RepositorySSHURL != strings.TrimSpace(project.RepositorySSHURL)) {
			family, err := engine.agentFamily(domain.Agent{ID: remote.EditorScopeID(project.ID, ""), ProjectID: project.ID})
			if err != nil {
				return project, err
			}
			terminal, err := engine.Store.HasTerminalAttachment(project.ID, "")
			if err != nil {
				return project, err
			}
			if terminal || engine.agentsAreBusy(family) || engine.Editors.HasActive(family, "") {
				return project, errors.New("finish project work and close its editors and terminals before changing the repository")
			}
		}
		if previous.ManagedWorkspace {
			project.ManagedWorkspace = true
		} else if project.Directory == "" {
			// Editing an old project through the new UI must not move its files.
			project.Directory = previous.Directory
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return project, err
	}

	if project.ManagedWorkspace || project.Directory == "" {
		project.ManagedWorkspace, project.Directory = true, ""
	} else {
		if _, err := remote.Workspace(project.Directory, ""); err != nil {
			return project, err
		}

		project.Directory = path.Clean(project.Directory)
	}

	if len(project.MachineIDs)+len(project.PoolIDs) != 1 {
		return project, errors.New("select exactly one remote machine or exactly one pool")
	}

	for _, id := range project.MachineIDs {
		if err := engine.Remote.Connector.RequireWorkTarget(id); err != nil {
			return project, err
		}
	}

	for _, id := range project.PoolIDs {
		var pool domain.Pool
		if err := engine.Store.Load("pool", id, &pool); err != nil {
			return project, err
		}

		if !project.ManagedWorkspace &&
			!pool.SharedHome &&
			pool.WorkspaceRoot != "" &&
			project.Directory != path.Clean(pool.WorkspaceRoot) &&
			!strings.HasPrefix(project.Directory, path.Clean(pool.WorkspaceRoot)+"/") {
			return project, errors.New("project must be within the pool's workspace_root")
		}
	}

	release, err := engine.reserveProject(project)
	if err != nil {
		return project, err
	}
	engine.configMu.Unlock()
	locked = false
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()
	project, err = engine.GitForge.SaveProject(ctx, project)
	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return project, err
}

func (engine *Engine) SaveSource(source domain.Source) (domain.Source, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if err := engine.projectAvailable(source.ProjectID); err != nil {
		return source, err
	}

	var existing domain.Source

	if source.ID != "" {
		if err := engine.Store.Load("source", source.ID, &existing); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return source, err
		}
	}

	if existing.BindingID != "" || existing.ProjectRoot {
		if err := engine.projectAvailable(existing.ProjectID); err != nil {
			return source, err
		}
		if source.URL != existing.URL ||
			source.KeyID != existing.KeyID ||
			source.ProjectID != existing.ProjectID {
			return source, errors.New("managed repository connection cannot change its URL or SSH key")
		}

		source.BindingID, source.RepositoryName, source.ProjectRoot = existing.BindingID, existing.RepositoryName, existing.ProjectRoot
	} else if source.BindingID != "" || source.ProjectRoot || source.RepositoryName != "" {
		return source, errors.New("use repository selection to create a managed connection")
	}

	if source.ID == "" {
		source.ID = NewID()
	}

	if !remote.ValidID(source.ID) || !remote.ValidID(source.Name) {
		return source, errors.New("source name must be a simple directory name")
	}

	var project domain.Project
	if err := engine.Store.Load("project", source.ProjectID, &project); err != nil {
		return source, err
	}

	if _, err := engine.Store.PrivateKey(source.KeyID); err != nil {
		return source, errors.New("unknown source SSH key")
	}

	if err := domain.ValidateRepositorySSHURL(source.URL); err != nil {
		return source, err
	}

	rows, err := engine.listAll("source", &source.ProjectID)
	if err != nil {
		return source, err
	}

	for _, row := range rows {
		if row["name"] == source.Name && row["id"] != source.ID {
			return source, errors.New("source directory is already used")
		}
	}

	purpose, owner := "source", source.ID

	if source.BindingID != "" {
		purpose, owner = "repository", source.BindingID
	}

	err = engine.Store.SaveWithSSHKeys(
		"source",
		source.ID,
		source.ProjectID,
		source,
		datastore.SSHKeyClaim{
			KeyID:     source.KeyID,
			Purpose:   purpose,
			PurposeID: owner,
		},
	)

	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return source, err
}
