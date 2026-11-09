package agent

import (
	"context"

	"github.com/lehlud/zentrale/domain"
)

// Native wire formats and encrypted reasoning are not portable across accounts,
// providers or models. Rebuild semantic context before the destination samples.
// A user-cleared window remains cleared; a native checkpoint needs its original
// audit history because its opaque state cannot serve as a portable summary.
func (engine *Engine) ensureInferenceScope(
	ctx context.Context,
	agent domain.Agent,
	configuration inferenceConfig,
) (domain.Agent, error) {
	owner := nativeContextOwner(configuration)
	rows, err := engine.listAll("inference_scope", &agent.ID)
	if err != nil {
		return agent, err
	}

	if len(rows) > 0 && stringArg(rows[0], "owner") != owner {
		history, err := engine.loadHistory(agent.ID)
		if err != nil {
			return agent, err
		}

		start := min(max(agent.ContextStart, 0), len(history))
		floor, err := engine.contextFloor(agent.ID, len(history))
		if err != nil {
			return agent, err
		}

		start = max(start, floor)
		checkpoints, err := engine.listAll("native_context", &agent.ID)
		if err != nil {
			return agent, err
		}

		if len(checkpoints) > 0 {
			start = floor
		}

		if len(history[start:]) > 0 || agent.Summary != "" {
			agent, err = engine.compactContext(
				ctx,
				agent,
				configuration,
				modelHistory(history[start:], configuration.Model.ContextWindow),
				len(history),
			)
			if err != nil {
				return agent, err
			}
		}

		if err := engine.Store.Remove("native_context", agent.ID); err != nil {
			return agent, err
		}
	}

	if len(rows) == 0 || stringArg(rows[0], "owner") != owner {
		err = engine.Store.Save("inference_scope", agent.ID, agent.ID, map[string]any{
			"agentId": agent.ID,
			"owner":   owner,
		})
	}

	return agent, err
}

func (engine *Engine) contextFloor(agentID string, historyLength int) (int, error) {
	rows, err := engine.listAll("context_boundary", &agentID)
	if err != nil || len(rows) == 0 {
		return 0, err
	}

	var boundary struct {
		Start int `json:"start"`
	}
	if err := decode(JSON(rows[0]), &boundary); err != nil {
		return 0, err
	}

	return min(max(boundary.Start, 0), historyLength), nil
}
