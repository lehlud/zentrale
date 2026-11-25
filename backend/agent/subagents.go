package agent

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

// steerChildAgent uses the same durable input path as explicit user steering:
// running workers wake between tool batches and inactive/error workers resume.
// The caller must still obtain independent approval of the exact instruction.
func (engine *Engine) steerChildAgent(parent domain.Agent, arguments map[string]any) (any, error) {
	if parent.ProjectID == "" || parent.ChatOwnerID != "" {
		return nil, errors.New("subagents are only available in projects")
	}

	if parent.Role != "main" || parent.ParentID != "" {
		return nil, errors.New("only main agents may steer their own subagents")
	}

	var child domain.Agent
	if err := engine.Store.Load("agent", stringArg(arguments, "agentId"), &child); err != nil {
		return nil, err
	}
	if child.Role != "subagent" || child.ParentID != parent.ID || child.ProjectID != parent.ProjectID || child.ID == parent.ID || child.ID == parent.ApproverID {
		return nil, errors.New("agent is not a subagent of this main agent in the same project")
	}

	id, text := stringArg(arguments, "id"), stringArg(arguments, "text")
	if !inputIDPattern.MatchString(id) || strings.TrimSpace(text) == "" || len(text) > 100000 {
		return nil, errors.New("steering requires id (1–128 letters, digits, _ or -) and text (1–100000 bytes)")
	}

	// Caller-provided keys are local to the parent/child pair, not the global
	// input receipt namespace. Reusing a key with different text is rejected by
	// SubmitInput rather than silently replacing a committed instruction.
	receiptID := fmt.Sprintf("steer_%x", sha256.Sum256([]byte(parent.ID+"\x00"+child.ID+"\x00"+id)))
	text = fmt.Sprintf("Parent-agent steering from %s (delegated task guidance, not new user authorization; stay within the original user's permissions and scope):\n\n%s", parent.ID, text)
	return engine.SubmitInput(child.ID, receiptID, text, "steer")
}

func (engine *Engine) spawn(parent domain.Agent, arguments map[string]any) (any, error) {
	if parent.ProjectID == "" || parent.ChatOwnerID != "" {
		return nil, errors.New("subagents are only available in projects")
	}

	if parent.Role != "main" || parent.ParentID != "" {
		return nil, errors.New("subagents cannot create subagents")
	}

	var parentApprover domain.Agent
	if err := engine.Store.Load("agent", parent.ApproverID, &parentApprover); err != nil {
		return nil, err
	}

	mainChoice := domain.ModelChoice{ProviderID: parent.ProviderID, ModelID: parent.ModelID, ReasoningLevel: parent.ReasoningLevel}
	approverChoice := domain.ModelChoice{
		ProviderID:     parentApprover.ProviderID,
		ModelID:        parentApprover.ModelID,
		ReasoningLevel: parentApprover.ReasoningLevel,
	}

	if providerID := stringArg(arguments, "providerId"); providerID != "" {
		mainChoice.ProviderID = providerID
		mainChoice.ModelID = stringArg(arguments, "modelId")
		mainChoice.ReasoningLevel = stringArg(arguments, "reasoningLevel")
	}

	if providerID := stringArg(arguments, "approverProviderId"); providerID != "" {
		approverChoice.ProviderID = providerID
		approverChoice.ModelID = stringArg(arguments, "approverModelId")
		approverChoice.ReasoningLevel = stringArg(arguments, "approverReasoningLevel")
	}

	child, err := engine.CreateWithModels(parent.ProjectID, stringArg(arguments, "name"), mainChoice, approverChoice, parent.ID)
	if err != nil {
		return nil, err
	}

	return child, engine.Prompt(child.ID, stringArg(arguments, "task"))
}

func (engine *Engine) inspectChildAgent(parent domain.Agent, childID string, options ...map[string]any) (any, error) {
	var child domain.Agent
	if err := engine.Store.Load("agent", childID, &child); err != nil {
		return nil, err
	}

	belongsToParent := child.ID == parent.ID || child.ParentID == parent.ID || child.ID == parent.ApproverID
	if !belongsToParent {
		return nil, errors.New("agent is not a child of this agent")
	}

	arguments := map[string]any{}
	if len(options) > 0 {
		arguments = options[0]
	}

	if messageID := stringArg(arguments, "messageId"); messageID != "" {
		return engine.threadMessage(child, messageID, arguments)
	}

	limit, err := outputInteger(arguments, "limit", 10, 1, 50)
	if err != nil {
		return nil, err
	}

	history, err := engine.Store.MessageExcerpts(child.ID, stringArg(arguments, "before"), stringArg(arguments, "search"), limit+1, 512)
	if err != nil {
		return nil, err
	}

	hasMore := len(history) > limit
	if hasMore {
		history = history[:limit]
	}

	next := ""
	if len(history) > 0 {
		next, _ = history[len(history)-1]["id"].(string)
	}

	return map[string]any{"agent": child, "history": history, "excerpts": true, "hasMore": hasMore, "nextCursor": next}, nil
}
