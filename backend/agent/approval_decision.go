package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type approvalDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"`
	Blanket bool   `json:"blanket"`
}

func parseApprovalDecision(text string) approvalDecision {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "```json")
	text = strings.TrimSuffix(text, "```")
	var decision approvalDecision
	if err := json.Unmarshal([]byte(text), &decision); err != nil {
		// Unmarshal may populate Allowed before rejecting a later field. Discard the
		// entire partial result so malformed responses can never authorize an action.
		return approvalDecision{Reason: "Auto-Approver returned no valid decision"}
	}
	if decision.Allowed {
		decision.Reason = ""
	} else if decision.Reason == "" {
		decision.Reason = "Auto-Approver denied this action"
	}
	return decision
}

func (engine *Engine) recordApproval(agentRecord domain.Agent, request interaction, decision approvalDecision) (bool, error) {
	sandbox := stringArg(request.Arguments, "sandbox")
	sandboxEnabled := sandbox == "bwrap" || sandbox == "microvm"
	canGrantRepeatedRead := request.Action == "read_file" && !request.Network && sandboxEnabled
	approval := domain.Approval{
		ID:        NewID(),
		AgentID:   agentRecord.ID,
		Tool:      request.Action,
		Arguments: JSON(request.Arguments),
		Allowed:   decision.Allowed,
		Reason:    decision.Reason,
		Blanket:   decision.Allowed && decision.Blanket && canGrantRepeatedRead,
		CreatedAt: time.Now().UTC(),
	}
	if err := engine.Store.Save("approval", approval.ID, agentRecord.ID, approval); err != nil {
		return false, err
	}
	engine.Emit("approval", agentRecord.ID, approval)
	if !decision.Allowed {
		return false, fmt.Errorf("auto-approval denied: %s", decision.Reason)
	}
	return approval.Blanket, nil
}
