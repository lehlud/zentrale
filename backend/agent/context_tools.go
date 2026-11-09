package agent

import (
	"context"
	"errors"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) manageContext(ctx context.Context, agentRecord domain.Agent, action string, args map[string]any) (any, error) {
	// Reload current state so tools from the same inference cannot undo compaction.
	if err := engine.Store.Load("agent", agentRecord.ID, &agentRecord); err != nil {
		return nil, err
	}
	history, err := engine.loadHistory(agentRecord.ID)
	if err != nil {
		return nil, err
	}
	if action == "context_compact" {
		configuration, err := engine.inferenceConfiguration(ctx, agentRecord)
		if err != nil {
			return nil, err
		}
		_, _, err = engine.prepareContext(ctx, agentRecord, configuration, true)
		return true, err
	}
	start := len(history)
	if action == "context_rotate" {
		n, ok := args["start"].(float64)
		if !ok || n < 0 || n > float64(len(history)) || n != float64(int(n)) {
			return nil, errors.New("invalid context start")
		}
		start = int(n)
		if start < len(history) && history[start].Role != "user" {
			return nil, errors.New("rotation must start at a user message")
		}
	}
	agentRecord.ContextStart = start
	agentRecord.ContextTokens = 0
	agentRecord.Summary = ""
	if err := engine.Store.Save("context_boundary", agentRecord.ID, agentRecord.ID, map[string]any{"agentId": agentRecord.ID, "start": start}); err != nil {
		return nil, err
	}
	if err := engine.Store.Remove("native_context", agentRecord.ID); err != nil {
		return nil, err
	}
	engine.Emit("context", agentRecord.ID, map[string]any{"action": action, "start": start})
	return true, engine.saveAgent(agentRecord)
}
