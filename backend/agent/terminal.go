package agent

import (
	"context"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// Terminal shares VS Code's preparation without starting VS Code. Activity
// and leases remain held until use returns and the PTY is closed.
func (engine *Engine) Terminal(ctx context.Context, projectID, agentID string, cols, rows int, use func(context.Context, *remote.Terminal) error) error {
	_, err := engine.withEditorEnvironment(ctx, projectID, agentID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, project domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
		if err := target.RuntimeLock.Lock(ctx); err != nil {
			return domain.EditorSession{}, err
		}
		terminal, err := remote.StartTerminal(ctx, connection, machine, project, target, cols, rows)
		target.RuntimeLock.Unlock()
		if err != nil {
			return domain.EditorSession{}, err
		}
		defer terminal.Close()
		return domain.EditorSession{}, use(ctx, terminal)
	})
	return err
}
