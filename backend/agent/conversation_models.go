package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) conversationModelTargets(agentID string) (domain.Agent, domain.Agent, error) {
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return record, domain.Agent{}, err
	}
	root, err := engine.approvalRoot(record)
	if err != nil {
		return root, domain.Agent{}, err
	}
	approver, err := engine.familyApprover(root)
	return root, approver, err
}

func (engine *Engine) ConversationModels(agentID string) (domain.ModelDefaults, error) {
	main, approver, err := engine.conversationModelTargets(agentID)
	if err != nil {
		return domain.ModelDefaults{}, err
	}
	current := func(record domain.Agent) domain.ModelChoice {
		choice := agentModelChoice(record)
		if resolved, err := engine.recordModelChoice(record); err == nil {
			return resolved
		}
		// Keep the old selection visible so a removed or disconnected model
		// can be replaced, even when it cannot currently be used for inference.
		return choice
	}
	return domain.ModelDefaults{Main: current(main), Approver: current(approver)}, nil
}

func (engine *Engine) SetConversationModel(agentID, role string, choice domain.ModelChoice) error {
	if role != "main" && role != "approver" {
		return errors.New("select the main agent or the auto-approver")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	main, approver, err := engine.conversationModelTargets(agentID)
	if err != nil {
		return err
	}
	target := main
	if role == "approver" {
		if main.ChatOwnerID != "" {
			return errors.New("the short-chat safety reviewer is configured by an administrator")
		}
		target = approver
	}
	if engine.agentRemoving(agentID) || engine.agentRemoving(target.ID) {
		return errAgentRemoving
	}
	if choice.ProviderID == "" || choice.ModelID == "" {
		return errors.New("select a model")
	}
	choice, err = engine.resolveScopedChoice(modelScope(target), target.ChatOwnerID, choice, role)
	if err != nil {
		return err
	}
	return engine.patchAgent(target.ID, map[string]any{
		"providerId": choice.ProviderID, "modelId": choice.ModelID, "reasoningLevel": choice.ReasoningLevel,
	})
}
