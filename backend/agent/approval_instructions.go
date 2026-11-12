package agent

import (
	"errors"

	"github.com/lehlud/zentrale/domain"
)

type approvalInstructionScope struct {
	RootAgentID       string           `json:"rootAgentId"`
	UserInstructions  []map[string]any `json:"userInstructions"`
	AgentCoordination []map[string]any `json:"agentCoordination"`
}

// Instruction resources historically include every delivered input in a
// project, including parent-generated child tasks (stored as role=user).
// Resource ownership, not role or content heuristics, establishes provenance.
// Unrelated conversations and sibling coordination cannot authorize/restrict
// the reviewed agent. Missing/cyclic/cross-project lineage fails closed.
func (engine *Engine) approvalInstructions(record domain.Agent) (approvalInstructionScope, error) {
	scope := approvalInstructionScope{UserInstructions: []map[string]any{}, AgentCoordination: []map[string]any{}}
	root, err := engine.approvalRoot(record)
	if err != nil {
		return scope, err
	}
	scope.RootAgentID = root.ID
	rows, err := engine.listAll("instruction", &record.ProjectID)
	if err != nil {
		return scope, err
	}
	for _, row := range rows {
		owner := stringArg(row, "agentId")
		if owner != root.ID && owner != record.ID {
			continue
		}
		copy := make(map[string]any, len(row)+4)
		for key, value := range row {
			copy[key] = value
		}
		copy["recipientAgentId"] = owner
		if owner == root.ID {
			copy["origin"] = "user"
			copy["authority"] = "original_user_instruction_at_stated_scope"
			scope.UserInstructions = append(scope.UserInstructions, copy)
		} else {
			if stringArg(row, "origin") == "parent_agent" {
				copy["origin"] = "parent_agent"
				copy["authority"] = "delegated_coordination_not_user_authorization_or_global_pause"
			} else {
				copy["origin"] = "legacy_agent_input"
				copy["authority"] = "recipient_scoped_legacy_input: preserve_possible_human_restrictions_until_verified; never_global_pause_or_new_authorization"
			}
			scope.AgentCoordination = append(scope.AgentCoordination, copy)
		}
	}
	return scope, nil
}

func (engine *Engine) approvalRoot(record domain.Agent) (domain.Agent, error) {
	root := record
	seen := map[string]bool{}
	for root.ParentID != "" {
		if seen[root.ID] {
			return domain.Agent{}, errors.New("cyclic instruction lineage")
		}
		seen[root.ID] = true
		var parent domain.Agent
		if err := engine.Store.Load("agent", root.ParentID, &parent); err != nil {
			return domain.Agent{}, err
		}
		if parent.ProjectID != record.ProjectID {
			return domain.Agent{}, errors.New("instruction lineage crosses project scope")
		}
		root = parent
	}
	if root.Role != "main" {
		return domain.Agent{}, errors.New("instruction lineage has no main agent")
	}
	return root, nil
}

// Current public mutations accept human prompts only for root main agents;
// child inputs are delivered internally from their parent. Store that verified
// provenance separately from role=user (needed by conversation turn delivery).
func instructionRecord(record domain.Agent, message domain.Message) map[string]any {
	var row map[string]any
	_ = decode(JSON(message), &row)
	row["recipientAgentId"] = record.ID
	if record.Role == "main" && record.ParentID == "" {
		row["origin"] = "user"
	} else {
		row["origin"] = "parent_agent"
		row["authorAgentId"] = record.ParentID
	}
	return row
}

func isAgentCoordination(message domain.Message) bool {
	if message.Role != "event" {
		return false
	}
	var envelope map[string]any
	return decode(message.Content, &envelope) == nil && stringArg(envelope, "kind") == "agent_coordination" && stringArg(envelope, "origin") == "parent_agent" && stringArg(envelope, "recipientAgentId") == message.AgentID
}
