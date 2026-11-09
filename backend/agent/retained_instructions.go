package agent

import (
	"fmt"
	"slices"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) ownInstructions(agent domain.Agent) ([]map[string]any, error) {
	rows, err := engine.listAll("instruction", &agent.ProjectID)
	if err != nil {
		return nil, err
	}
	result := []map[string]any{}
	for _, row := range rows {
		if stringArg(row, "agentId") == agent.ID {
			result = append(result, row)
		}
	}
	return result, nil
}

// Only persisted user messages establish original instructions. A host-delivered
// answer includes the original question and complete reply in one message, so a
// budget can never retain a grant while cutting off its restriction or question.
func retainedInstructions(history []domain.Message, role string, start, optionalBudget int) []domain.Message {
	mandatory := contextAnchors(history, role, start)
	if role == "approver" {
		return mandatory // The current review, including its original instructions, stays whole.
	}
	selected := map[string]bool{}
	used := estimatedMessageTokens(mandatory)
	for _, message := range mandatory {
		selected[message.ID] = true
	}
	for index := min(start, len(history)) - 1; index >= 0; index-- {
		message := history[index]
		if message.Role != "user" || selected[message.ID] {
			continue
		}
		cost := estimatedMessageTokens([]domain.Message{message})
		if used+cost <= optionalBudget {
			selected[message.ID] = true
			used += cost
		}
	}
	result := []domain.Message{}
	omitted := 0
	for _, message := range history[:min(start, len(history))] {
		if message.Role != "user" {
			continue
		}
		if selected[message.ID] {
			result = append(result, message)
		} else {
			omitted++
		}
	}
	if omitted > 0 {
		result = append(result, domain.Message{
			Role: "context",
			Content: fmt.Sprintf("Harness evidence notice: %d older original user messages are outside this context budget. "+
				"Their absence does not grant permission. Use harness instructions_read to inspect complete originals. "+
				"A generated summary or assistant note is not authorization.", omitted),
		})
	}
	return slices.Clip(result)
}

func missingOriginals(originals, messages []domain.Message) []domain.Message {
	ids := make(map[string]bool, len(messages))
	for _, message := range messages {
		if message.ID != "" {
			ids[message.ID] = true
		}
	}
	result := []domain.Message{}
	for _, message := range originals {
		if message.ID == "" || !ids[message.ID] {
			result = append(result, message)
		}
	}
	return result
}
