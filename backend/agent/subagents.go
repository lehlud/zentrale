package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) spawn(parent domain.Agent, arguments map[string]any) (any, error) {
	if parent.Role != "main" {
		return nil, errors.New("subagents cannot create subagents")
	}
	var parentApprover domain.Agent
	if err := engine.Store.Load("agent", parent.ApproverID, &parentApprover); err != nil {
		return nil, err
	}
	mainChoice := domain.ModelChoice{ProviderID: parent.ProviderID, ModelID: parent.ModelID}
	approverChoice := domain.ModelChoice{ProviderID: parentApprover.ProviderID, ModelID: parentApprover.ModelID}
	if providerID := stringArg(arguments, "providerId"); providerID != "" {
		mainChoice.ProviderID = providerID
		mainChoice.ModelID = stringArg(arguments, "modelId")
	}
	if providerID := stringArg(arguments, "approverProviderId"); providerID != "" {
		approverChoice.ProviderID = providerID
		approverChoice.ModelID = stringArg(arguments, "approverModelId")
	}
	child, err := engine.CreateWithModels(parent.ProjectID, stringArg(arguments, "name"), mainChoice, approverChoice, parent.ID)
	if err != nil {
		return nil, err
	}
	return child, engine.Prompt(child.ID, stringArg(arguments, "task"))
}

func (engine *Engine) inspectChildAgent(parent domain.Agent, childID string) (any, error) {
	var child domain.Agent
	if err := engine.Store.Load("agent", childID, &child); err != nil {
		return nil, err
	}
	belongsToParent := child.ID == parent.ID || child.ParentID == parent.ID || child.ID == parent.ApproverID
	if !belongsToParent {
		return nil, errors.New("agent is not a child of this agent")
	}
	history, err := engine.loadHistory(child.ID)
	return map[string]any{"agent": child, "history": history}, err
}
