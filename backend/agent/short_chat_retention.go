package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

const shortChatDay = 24 * time.Hour

func (engine *Engine) pruneShortChats(ctx context.Context, now time.Time) {
	settings, err := engine.ShortChatSettings()
	if err != nil || settings.RetentionDays < 1 || settings.RetentionDays > maxShortChatDays {
		return
	}

	retention := time.Duration(settings.RetentionDays) * shortChatDay
	cutoff := now.Add(-retention)
	unpinnedCutoff := now.Add(-retention / 2)
	const pageSize = 100
	for after := ""; ctx.Err() == nil; {
		// Inputs, branches and pin changes share this lock. Keep selection
		// and deletion together, but release the lock between batches.
		engine.configMu.Lock()
		rows, err := engine.Store.ExpiredShortChats(cutoff, unpinnedCutoff, after, pageSize)
		for _, row := range rows {
			after = stringArg(row, "id")
			selected, err := engine.agentRemovalTargets(after)
			if err != nil || engine.agentsAreBusy(selected) {
				continue
			}
			if err := engine.removeAgentRecords(after, selected); err != nil {
				slog.Warn("failed to prune a short chat", "agent", after, "error", err)
			}
		}
		engine.configMu.Unlock()

		if err != nil {
			slog.Warn("failed to list expired short chats", "error", err)
			return
		}
		if len(rows) < pageSize {
			return
		}
	}
}

// Use the host recorded at deletion, even if the user's current placement
// later changes. A deleted chat's ID and derived directory are never reused.
func (engine *Engine) cleanupShortChatDirectories(ctx context.Context) {
	rows, err := engine.Store.PendingShortChatCleanup()
	if err != nil {
		return
	}

	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		chatID, ownerID := stringArg(row, "id"), stringArg(row, "ownerId")
		scope, directory := engine.shortChatWorkspace(domain.Agent{ID: chatID, ChatOwnerID: ownerID})
		entry, _ := engine.vmOperations.LoadOrStore(scope, &vmOperation{gate: make(chan struct{}, 1)})
		operation := entry.(*vmOperation)
		select {
		case operation.gate <- struct{}{}:
			err := engine.removeShortChatDirectory(ctx, stringArg(row, "memberId"), scope, directory)
			operation.Unlock()
			if err == nil {
				_ = engine.Store.Remove("short_chat_cleanup", chatID)
				continue
			}
			slog.Warn("failed to remove short-chat workspace files", "agent", chatID, "error", err)
		default:
			// A code invocation is still using this workspace.
		}
		row["retryAt"] = time.Now().UTC().Add(time.Minute)
		_ = engine.Store.Save("short_chat_cleanup", chatID, "", row)
	}
}

func (engine *Engine) removeShortChatDirectory(ctx context.Context, memberID, scope, directory string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	connection, _, err := engine.dialShortChatVM(ctx, memberID)
	if err != nil {
		return err
	}
	defer connection.Close()

	return remote.RemoveShortChatDirectory(ctx, connection, scope, directory)
}
