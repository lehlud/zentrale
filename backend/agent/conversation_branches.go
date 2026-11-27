package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

var errConversationUnavailable = errors.New("conversation not found or not available")

func (engine *Engine) accessibleConversation(userID, agentID string) (domain.Agent, error) {
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil ||
		userID == "" || record.Role != "main" || record.ParentID != "" {
		return domain.Agent{}, errConversationUnavailable
	}

	if record.ChatOwnerID != "" {
		if record.ProjectID != "" || record.ChatOwnerID != userID {
			return domain.Agent{}, errConversationUnavailable
		}
	} else {
		var project domain.Project
		if record.ProjectID == "" || engine.Store.Load("project", record.ProjectID, &project) != nil ||
			!project.HasMember(userID) {
			return domain.Agent{}, errConversationUnavailable
		}
	}

	return record, nil
}

func (engine *Engine) ConversationBranches(ctx context.Context, userID, agentID string) ([]domain.Agent, error) {
	source, err := engine.accessibleConversation(userID, agentID)
	if err != nil {
		return nil, err
	}

	return engine.Store.ConversationBranches(ctx, source)
}

// BranchConversation forks a settled transcript without changing the source or
// inheriting its runtime resources. When editing, only the replacement becomes
// a new input, committed atomically with the branch before waking its worker.
func (engine *Engine) BranchConversation(ctx context.Context, userID, agentID, messageID string, text *string) (domain.Agent, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	source, err := engine.accessibleConversation(userID, agentID)
	if err != nil {
		return domain.Agent{}, err
	}

	if err := engine.ctx.Err(); err != nil {
		return domain.Agent{}, err
	}

	if engine.agentRemoving(source.ID) {
		return domain.Agent{}, errAgentRemoving
	}

	if err := engine.projectEnvironmentAvailable(source.ProjectID); err != nil {
		return domain.Agent{}, err
	}

	if engine.AgentWorkerActive(source.ID) || source.Status == "running" || source.Status == "waiting" {
		return domain.Agent{}, errors.New("stop the conversation before branching")
	}

	inputs, err := engine.Store.AgentInputs(source.ID)
	if err != nil {
		return domain.Agent{}, err
	}
	for _, input := range inputs {
		if input.Status == "pending" || input.Status == "queued" || input.Status == "active" {
			return domain.Agent{}, errors.New("resolve pending inputs before branching")
		}
	}

	branch := domain.Agent{
		ID: NewID(), ProjectID: source.ProjectID, ChatOwnerID: source.ChatOwnerID,
		Name: source.Name, ChatNamed: source.ChatNamed, Role: "main", Status: "idle",
		ProviderID: source.ProviderID, ModelID: source.ModelID, ReasoningLevel: source.ReasoningLevel,
		ApproverID: NewID(), BranchParentID: source.ID, BranchRootID: source.BranchRootID,
		BranchMessageID: messageID,
	}
	if branch.BranchRootID == "" {
		branch.BranchRootID = source.ID
	}

	previousApprover, err := engine.familyApprover(source)
	if err != nil {
		return domain.Agent{}, err
	}
	approver := domain.Agent{
		ID: branch.ApproverID, ProjectID: branch.ProjectID, ChatOwnerID: branch.ChatOwnerID,
		ParentID: branch.ID, Role: "approver", Name: "Auto-Approver", Status: "idle",
		ProviderID: previousApprover.ProviderID, ModelID: previousApprover.ModelID,
		ReasoningLevel: previousApprover.ReasoningLevel,
	}

	var edit *domain.AgentInput
	if text != nil {
		if len(*text) > 100000 || strings.ContainsRune(*text, 0) {
			return domain.Agent{}, errors.New("edited instruction must contain at most 100000 bytes without NUL")
		}
		edit = &domain.AgentInput{
			ID: NewID(), AgentID: branch.ID, ProjectID: branch.ProjectID, Text: *text,
			Mode: "steer", Status: "pending", CreatedAt: time.Now().UTC(),
		}
	}

	branch, err = engine.Store.CreateConversationBranch(ctx, source, branch, approver, edit)
	if err != nil {
		return domain.Agent{}, err
	}

	engine.Emit("resources", branch.ID, nil)
	if edit != nil {
		engine.wakeAcceptedInput(branch, *edit)
	}

	return branch, nil
}
