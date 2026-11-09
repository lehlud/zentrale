package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type nativeContext struct {
	AgentID string `json:"agentId"`
	Owner   string `json:"owner"`
	Start   int    `json:"start"`
	Wire    string `json:"wire"`
}

func nativeContextOwner(configuration inferenceConfig) string {
	provider := configuration.Provider
	account := provider.Credential
	if provider.OAuth != nil {
		account = provider.OAuth.AccountID
	}
	digest := sha256.Sum256([]byte(JSON([]string{
		provider.ID, provider.Kind, provider.BaseURL, provider.AuthMode, account, configuration.Model.ID,
	})))
	return hex.EncodeToString(digest[:])
}

func (engine *Engine) loadNativeContext(agent *domain.Agent, configuration inferenceConfig) (string, error) {
	rows, err := engine.listAll("native_context", &agent.ID)
	if err != nil || len(rows) == 0 {
		return "", err
	}
	var checkpoint nativeContext
	if err := decode(JSON(rows[0]), &checkpoint); err != nil {
		return "", err
	}
	if checkpoint.Owner == nativeContextOwner(configuration) && checkpoint.Start == agent.ContextStart {
		return checkpoint.Wire, nil
	}
	// Opaque provider state cannot cross models/accounts/protocols. Rebuild from
	// the full audit history using the destination provider's portable compactor.
	agent.ContextStart, agent.ContextTokens, agent.Summary = 0, 0, ""
	if err := engine.saveAgent(*agent); err != nil {
		return "", err
	}
	return "", engine.Store.Remove("native_context", agent.ID)
}

func (engine *Engine) tryNativeCompaction(ctx context.Context, agent domain.Agent, configuration inferenceConfig, messages []domain.Message, nextStart, reserved int) (domain.Agent, string, bool, error) {
	compactor, available := engine.Provider.(providers.NativeCompactor)
	if !available {
		return agent, "", false, nil
	}
	owner := nativeContextOwner(configuration)
	var disabled struct {
		Owner string    `json:"owner"`
		Until time.Time `json:"until"`
	}
	_ = engine.Store.Load("native_capability", agent.ID, &disabled)
	if disabled.Owner == owner && time.Now().Before(disabled.Until) {
		return agent, "", false, nil
	}
	system := engine.buildSystemPrompt(agent)
	tools := toolsForModel(configuration.Model)
	if agent.Role == "approver" {
		system, tools = approverInstructions, []providers.Tool{inspectTool()}
	}
	result, err := compactor.Compact(ctx, providers.Request{
		Config: configuration.Provider, Model: configuration.Model,
		System: system, Tools: tools, Messages: messages, CacheKey: agent.ID,
	})
	if errors.Is(err, providers.ErrCompactionUnsupported) {
		err = engine.disableNativeCompaction(agent.ID, owner)
		return agent, "", false, err
	}
	if err != nil {
		return agent, "", false, err
	}
	if err := engine.recordUsage(agent, configuration, result, messages, estimatedPromptTokens(system, tools)); err != nil {
		return agent, "", false, err
	}
	if result.Wire == "" {
		return agent, "", false, errors.New("native compaction returned an empty context")
	}
	checkpointTokens := estimatedMessageTokens([]domain.Message{{Wire: result.Wire}})
	if checkpointTokens+estimatedPromptTokens(system, tools)+reserved > configuration.Model.ContextWindow*80/100 {
		return agent, "", false, engine.disableNativeCompaction(agent.ID, owner)
	}
	checkpoint := nativeContext{AgentID: agent.ID, Owner: owner, Start: nextStart, Wire: result.Wire}
	if err := engine.Store.Save("native_context", agent.ID, agent.ID, checkpoint); err != nil {
		return agent, "", false, err
	}
	agent.ContextStart, agent.Summary = nextStart, ""
	agent.ContextTokens = estimatedMessageTokens([]domain.Message{{Wire: result.Wire}})
	if err := engine.saveAgent(agent); err != nil {
		return agent, "", false, err
	}
	engine.Emit("context", agent.ID, map[string]any{"action": "compact", "mode": "native", "contextStart": nextStart})
	return agent, result.Wire, true, nil
}

func (engine *Engine) disableNativeCompaction(agentID, owner string) error {
	return engine.Store.Save("native_capability", agentID, agentID, map[string]any{
		"agentId": agentID, "owner": owner, "until": time.Now().Add(24 * time.Hour),
	})
}
