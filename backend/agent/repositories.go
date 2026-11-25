package agent

import (
	"context"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) ConnectRepository(ctx context.Context, projectID, connectionID, repositoryID string) (domain.Source, error) {
	engine.configMu.Lock()
	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil {
		engine.configMu.Unlock()
		return domain.Source{}, err
	}
	release, err := engine.reserveProject(project)
	engine.configMu.Unlock()
	if err != nil {
		return domain.Source{}, err
	}
	defer release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	source, err := engine.GitFor(project.Owner()).AttachSource(ctx, projectID, connectionID, repositoryID)
	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return source, err
}

func (engine *Engine) RevokeRepositoryKey(ctx context.Context, keyID int) error {
	return engine.RevokePersonalRepositoryKey(ctx, domain.InitialUserID, keyID)
}

func (engine *Engine) RevokePersonalRepositoryKey(ctx context.Context, userID string, keyID int) error {
	err := engine.GitFor(userID).RevokeKey(ctx, keyID)
	if err == nil {
		engine.Emit("resources", "", nil)
	}

	return err
}
