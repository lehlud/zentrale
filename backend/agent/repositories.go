package agent

import (
	"context"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) ConnectRepository(ctx context.Context, projectID, connectionID, repositoryID string) (domain.Source, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	source, err := engine.GitForge.AttachSource(ctx, projectID, connectionID, repositoryID)
	if err == nil {
		engine.Emit("resources", "", nil)
	}
	return source, err
}

func (engine *Engine) RevokeRepositoryKey(ctx context.Context, keyID int) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	err := engine.GitForge.RevokeKey(ctx, keyID)
	if err == nil {
		engine.Emit("resources", "", nil)
	}
	return err
}
