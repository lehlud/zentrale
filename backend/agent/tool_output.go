package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
)

const maximumModelToolOutputBytes = 16000

// Bound only the inference view. Persisted history, user instructions and UI
// logs remain complete; an agent can recover omitted bytes with tool_output.
func modelHistory(history []domain.Message, contextWindow int) []domain.Message {
	result := append([]domain.Message(nil), history...)
	budget := max(1024, min(maximumModelToolOutputBytes, contextWindow/2))

	for index, message := range result {
		if message.Role != "tool" || len(message.Content) <= budget {
			continue
		}

		failure, _ := outputPreview(toolOutputError(message.Content), 64)
		previewBudget := budget

		for {
			preview, offset := outputPreview(message.Content, previewBudget)
			envelope := map[string]any{
				"truncated":     true,
				"originalBytes": len(message.Content),
				"messageId":     message.ID,
				"preview":       preview,
				"readMore": map[string]any{
					"action": "tool_output",
					"arguments": map[string]any{
						"messageId": message.ID,
						"offset":    offset,
						"limit":     min(4096, budget/2),
					},
				},
			}

			// A long log must not hide a failed tool call from the model.
			if failure != "" {
				envelope["error"] = failure
			}

			content := JSON(envelope)
			if len(content) <= budget || previewBudget == 0 {
				result[index].Content = content
				break
			}

			// Account for JSON escaping as well as the recovery metadata.
			previewBudget = max(0, min(previewBudget-1, previewBudget*budget/len(content)-64))
		}
	}

	return result
}

func toolOutputError(content string) string {
	var result struct {
		Error  string `json:"error"`
		Result struct {
			Error  string `json:"error"`
			Status string `json:"status"`
			Job    struct {
				Error  string `json:"error"`
				Status string `json:"status"`
			} `json:"job"`
		} `json:"result"`
	}
	if json.Unmarshal([]byte(content), &result) != nil {
		return ""
	}

	if result.Error != "" {
		return result.Error
	}

	if result.Result.Error != "" {
		return result.Result.Error
	}

	if result.Result.Status == "failed" || result.Result.Status == "interrupted" {
		return "job " + result.Result.Status
	}

	if result.Result.Job.Error != "" {
		return result.Result.Job.Error
	}

	if result.Result.Job.Status == "failed" || result.Result.Job.Status == "interrupted" {
		return "job " + result.Result.Job.Status
	}

	return ""
}

func outputPreview(content string, budget int) (string, int) {
	if len(content) <= budget {
		return content, len(content)
	}

	head := budget / 2

	for head > 0 && !utf8.RuneStart(content[head]) {
		head--
	}

	tail := len(content) - (budget - head)

	for tail < len(content) && !utf8.RuneStart(content[tail]) {
		tail++
	}

	return content[:head] + "\n[... output omitted; use tool_output ...]\n" + content[tail:], head
}

func (engine *Engine) toolOutput(agentRecord domain.Agent, arguments map[string]any) (any, error) {
	var message domain.Message
	if err := engine.Store.Load("message", stringArg(arguments, "messageId"), &message); err != nil {
		return nil, err
	}

	if message.AgentID != agentRecord.ID || message.Role != "tool" {
		return nil, errors.New("tool output belongs to another agent or is not a tool result")
	}

	offset, err := outputInteger(arguments, "offset", 0, 0, len(message.Content))
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "limit", 4096, 4, maximumModelToolOutputBytes)
	if err != nil {
		return nil, err
	}

	for offset < len(message.Content) && !utf8.RuneStart(message.Content[offset]) {
		offset++
	}

	end := min(len(message.Content), offset+limit)

	for end < len(message.Content) && !utf8.RuneStart(message.Content[end]) {
		end--
	}

	return map[string]any{
		"messageId":  message.ID,
		"content":    message.Content[offset:end],
		"offset":     offset,
		"nextOffset": end,
		"totalBytes": len(message.Content),
		"hasMore":    end < len(message.Content),
		"unit":       "UTF-8 bytes",
	}, nil
}

func outputInteger(arguments map[string]any, name string, fallback, minimum, maximum int) (int, error) {
	value, exists := arguments[name]
	if !exists {
		return fallback, nil
	}

	if integer, ok := value.(int); ok {
		value = float64(integer)
	}

	number, ok := value.(float64)
	if !ok ||
		math.IsNaN(number) ||
		math.IsInf(number, 0) ||
		number < float64(minimum) ||
		number > float64(maximum) ||
		math.Trunc(number) != number {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}

	return int(number), nil
}
