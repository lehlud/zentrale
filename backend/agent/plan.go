package agent

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
)

type planStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

type taskPlan struct {
	AgentID     string     `json:"agentId"`
	Explanation string     `json:"explanation"`
	Plan        []planStep `json:"plan"`
}

func (engine *Engine) updatePlan(agentRecord domain.Agent, arguments map[string]any) (any, error) {
	var steps []planStep
	if err := decode(JSON(arguments["plan"]), &steps); err != nil || len(steps) == 0 || len(steps) > 20 {
		return nil, errors.New("plan must contain 1–20 steps")
	}
	active := 0
	for index := range steps {
		step := &steps[index]
		step.Step = strings.TrimSpace(step.Step)
		if step.Step == "" || utf8.RuneCountInString(step.Step) > 300 || !noSecrets(step.Step) {
			return nil, errors.New("each plan step needs 1–300 characters")
		}
		switch step.Status {
		case "pending", "completed":
		case "in_progress":
			active++
		default:
			return nil, errors.New("plan status must be pending, in_progress or completed")
		}
	}
	if active > 1 {
		return nil, errors.New("only one plan step can be in progress")
	}
	explanation := strings.TrimSpace(stringArg(arguments, "explanation"))
	if len(explanation) > 4000 || !noSecrets(explanation) {
		return nil, errors.New("plan explanation exceeds 4000 bytes or contains invalid characters")
	}
	plan := taskPlan{AgentID: agentRecord.ID, Explanation: explanation, Plan: steps}
	if err := engine.Store.Save("plan", agentRecord.ID, agentRecord.ID, plan); err != nil {
		return nil, err
	}
	return map[string]any{"plan": steps, "explanation": explanation}, nil
}
