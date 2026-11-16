package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/lehlud/zentrale/codemode"
	"github.com/lehlud/zentrale/domain"
)

const maximumModelToolOutputBytes = 16000

// Bound only the inference view. Persisted history, user instructions and UI
// logs remain complete; an agent can recover omitted bytes with tool_output.
func modelHistory(history []domain.Message, contextWindow int) []domain.Message {
	result := append([]domain.Message(nil), history...)
	budget := max(1024, min(maximumModelToolOutputBytes, contextWindow/2))
	seen := map[string]string{}

	for index, message := range result {
		if message.Role != "tool" {
			continue
		}
		// References depend only on earlier results in this window. Rotation
		// starts with the actual result again; stored evidence stays complete.
		if message.ID != "" && message.AgentID != "" && message.ToolName != "tool_output" &&
			len(message.Content) >= 1024 && !toolOutputHasFailure(message.Content) && json.Valid([]byte(message.Content)) {
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(message.Content)))
			key := message.AgentID + ":" + message.ToolName + ":" + digest
			if original, exists := seen[key]; exists {
				reference := JSON(map[string]any{
					"repeatedOutput": true, "originalMessageId": original,
					"sha256": digest, "originalBytes": len(message.Content),
					"readMore": map[string]any{"action": "tool_output", "arguments": map[string]any{"messageId": message.ID, "offset": 0, "limit": 4096}},
				})
				if len(reference) < len(message.Content) && len(reference) <= budget {
					result[index].Content = reference
					continue
				}
			} else {
				seen[key] = message.ID
			}
		}

		observation := compactJobObservation(message)
		if len(observation) <= budget {
			result[index].Content = observation
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

// Observation tools repeatedly return the launch command, which can itself be
// an entire script. Keep its original record, not another copy in every poll.
// Other tool results are deliberately untouched: a field named "command" in an
// application document is not launch metadata.
func compactJobObservation(message domain.Message) string {
	if message.ID == "" {
		return message.Content
	}

	switch message.ToolName {
	case "job_status", "job_logs", "job_wait", "write_stdin":
	default:
		return message.Content
	}

	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(message.Content), &envelope) != nil {
		return message.Content
	}

	var result map[string]json.RawMessage
	if json.Unmarshal(envelope["result"], &result) != nil || result == nil {
		return message.Content
	}

	job := result
	nested := false
	if raw, found := result["job"]; found {
		job = nil
		if json.Unmarshal(raw, &job) != nil || job == nil {
			return message.Content
		}

		nested = true
	}

	if _, found := job["command"]; !found || job["id"] == nil || job["status"] == nil {
		return message.Content
	}

	delete(job, "command")
	if nested {
		result["job"] = json.RawMessage(JSON(job))
	}

	envelope["result"] = json.RawMessage(JSON(result))
	envelope["observation"] = json.RawMessage(JSON("Repeated launch command omitted; all output and status retained."))
	envelope["readOriginal"] = json.RawMessage(JSON(map[string]any{
		"action":    "tool_output",
		"arguments": map[string]any{"messageId": message.ID, "offset": 0, "limit": 4096},
	}))

	compact := JSON(envelope)
	if len(compact) >= len(message.Content) {
		return message.Content
	}

	return compact
}

func toolOutputError(content string) string {
	// The result can be any JSON value. Preserve an explicit outer failure even
	// when its partial result does not match the known job/result structures.
	var outer struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(content), &outer) == nil && outer.Error != "" {
		return outer.Error
	}
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

// A repeated inspection batch must retain its individual errors. This is
// deliberately conservative for arbitrary documents with error-like fields.
func toolOutputHasFailure(content string) bool {
	var value any
	if json.Unmarshal([]byte(content), &value) != nil {
		return true
	}
	var failed func(any, int) bool
	failed = func(value any, depth int) bool {
		if depth > 64 {
			return true
		}
		switch value := value.(type) {
		case map[string]any:
			if message, ok := value["error"]; ok && message != nil && message != "" && message != false {
				return true
			}
			if value["status"] == "failed" || value["status"] == "interrupted" {
				return true
			}
			for _, child := range value {
				if failed(child, depth+1) {
					return true
				}
			}
		case []any:
			for _, child := range value {
				if failed(child, depth+1) {
					return true
				}
			}
		}
		return false
	}
	return failed(value, 0)
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

	content := message.Content
	metadata := map[string]any{}
	if _, requested := arguments["activity"]; requested {
		activity, err := outputInteger(arguments, "activity", 0, 1, codemode.MaximumCalls)
		if err != nil {
			return nil, err
		}

		var audit struct {
			AgentID    string                `json:"agentId"`
			Activities []domain.ToolActivity `json:"activities"`
		}
		if err := engine.Store.Load("tool_activity", message.ID, &audit); err != nil {
			return nil, err
		}
		if audit.AgentID != agentRecord.ID || activity > len(audit.Activities) {
			return nil, errors.New("inner tool result is unavailable or belongs to another agent")
		}

		recorded := audit.Activities[activity-1]
		content = recorded.Content
		metadata["activity"], metadata["toolName"] = activity, recorded.Name
		var preview struct {
			Truncated     bool `json:"truncated"`
			OriginalBytes int  `json:"originalBytes"`
		}
		_ = json.Unmarshal([]byte(content), &preview)
		metadata["recordingTruncated"] = preview.Truncated && preview.OriginalBytes > 0
		if metadata["recordingTruncated"] == true {
			metadata["originalBytes"] = preview.OriginalBytes
			metadata["recovery"] = "Only the stored preview is available here; use source file/job tools for omitted original data."
		}
	}

	offset, err := outputInteger(arguments, "offset", 0, 0, len(content))
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "limit", 4096, 4, maximumModelToolOutputBytes)
	if err != nil {
		return nil, err
	}

	for offset < len(content) && !utf8.RuneStart(content[offset]) {
		offset++
	}

	end := min(len(content), offset+limit)

	for end < len(content) && !utf8.RuneStart(content[end]) {
		end--
	}

	response := map[string]any{
		"messageId":  message.ID,
		"content":    content[offset:end],
		"offset":     offset,
		"nextOffset": end,
		"totalBytes": len(content),
		"hasMore":    end < len(content),
		"unit":       "UTF-8 bytes",
	}
	for key, value := range metadata {
		response[key] = value
	}

	return response, nil
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
