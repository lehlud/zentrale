package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

// Terminal shares VS Code's preparation without starting VS Code. Activity
// and leases remain held until use returns and the PTY is closed.
func (engine *Engine) Terminal(ctx context.Context, projectID, agentID string, cols, rows int, use func(context.Context, *remote.Terminal) error) error {
	_, err := engine.withEditorEnvironment(ctx, projectID, agentID, func(ctx context.Context, connection *remote.Connection, machine domain.Machine, project domain.Project, target remote.EditorTarget) (domain.EditorSession, error) {
		logStartup := func(lockWait, ptyStart time.Duration, ready bool) {
			// Exactly one startup receipt, before use() can keep the terminal
			// open for hours. No commands, input, credentials or error text.
			slog.Info("terminal startup", "project", project.ID, "agent", target.AgentID, "machine", machine.ID,
				"lock_wait_ms", lockWait.Milliseconds(), "pty_start_ms", ptyStart.Milliseconds(), "ready", ready, "error", !ready)
		}
		lockStarted := time.Now()
		if err := target.RuntimeLock.Lock(ctx); err != nil {
			logStartup(time.Since(lockStarted), 0, false)
			return domain.EditorSession{}, err
		}
		lockWait := time.Since(lockStarted)
		ptyStarted := time.Now()
		terminal, err := remote.StartTerminal(ctx, connection, machine, project, target, cols, rows)
		ptyStart := time.Since(ptyStarted)
		target.RuntimeLock.Unlock()
		logStartup(lockWait, ptyStart, err == nil)
		if err != nil {
			return domain.EditorSession{}, err
		}
		defer terminal.Close()
		return domain.EditorSession{}, use(ctx, terminal)
	})
	return err
}
