package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

// Read call identities without reconstructing provider wire items. Reasoning
// signatures, phases and encrypted state remain untouched during normal replay.
func storedCalls(message domain.Message) []providers.Call {
	var wire any
	if json.Unmarshal([]byte(message.Wire), &wire) != nil {
		return nil
	}
	entries, _ := wire.([]any)
	if object, ok := wire.(map[string]any); ok {
		switch {
		case object["tool_calls"] != nil:
			entries, _ = object["tool_calls"].([]any)
		case object["parts"] != nil:
			entries, _ = object["parts"].([]any)
		default:
			entries = []any{object}
		}
	}
	calls := []providers.Call{}
	for index, value := range entries {
		entry, ok := value.(map[string]any)
		if !ok {
			continue
		}
		function := entry
		id := stringArg(entry, "call_id")
		if id == "" {
			id = stringArg(entry, "id")
		}
		if nested, ok := entry["function"].(map[string]any); ok {
			function = nested
		}
		if nested, ok := entry["functionCall"].(map[string]any); ok {
			function = nested
			id = fmt.Sprintf("gemini-%d", index)
		}
		name := stringArg(function, "name")
		if name == "" || id == "" {
			continue
		}
		arguments := function["arguments"]
		if arguments == nil {
			arguments = function["input"]
		}
		if arguments == nil {
			arguments = function["args"]
		}
		var values map[string]any
		if text, ok := arguments.(string); ok {
			_ = decode(text, &values)
		} else {
			values, _ = arguments.(map[string]any)
		}
		calls = append(calls, providers.Call{ID: id, Name: name, Arguments: values})
	}
	return calls
}

// A request may be interrupted between tool results, or user input may arrive
// while tools are running. Present complete call/result groups to every provider
// and deliver the queued user instructions immediately after those results.
// This is a projection only; the original chronology stays in the UI history.
func restoreConversationBoundary(messages []domain.Message) []domain.Message {
	result := make([]domain.Message, 0, len(messages))
	for index := 0; index < len(messages); index++ {
		message := messages[index]
		calls := storedCalls(message)
		if message.Role != "assistant" || len(calls) == 0 {
			if message.Role == "tool" {
				message = orphanToolResult(message)
			}
			result = append(result, message)
			continue
		}
		end := index + 1
		for end < len(messages) && messages[end].Role != "assistant" {
			end++
		}
		outputs := map[string]domain.Message{}
		for _, following := range messages[index+1 : end] {
			if following.Role == "tool" {
				outputs[following.CallID] = following
			}
		}
		result = append(result, message)
		for _, call := range calls {
			output, found := outputs[call.ID]
			if !found {
				output = domain.Message{
					Role:     "tool",
					CallID:   call.ID,
					ToolName: call.Name,
					Content: JSON(map[string]any{
						"error": "Tool execution was interrupted or its result is unavailable. " +
							"Inspect current state before retrying; this is not a successful result.",
					}),
				}
			}
			result = append(result, output)
			delete(outputs, call.ID)
		}
		for _, following := range messages[index+1 : end] {
			if following.Role != "tool" {
				result = append(result, following)
			} else if _, unmatched := outputs[following.CallID]; unmatched {
				result = append(result, orphanToolResult(following))
			}
		}
		index = end - 1
	}
	return result
}

func orphanToolResult(message domain.Message) domain.Message {
	return domain.Message{
		Role: "user",
		Content: "Harness result from the preceding context (not a new user instruction):\n" +
			message.Content,
	}
}

// Keep recent complete groups, never just the output half of a tool call.
func retainedContextStart(messages []domain.Message, tokenBudget int) int {
	start, tokens, groups := len(messages), 0, 0
	pendingTools := false
	for index := len(messages) - 1; index >= 0; index-- {
		tokens += estimatedMessageTokens(messages[index : index+1])
		if tokens > tokenBudget {
			break
		}
		if messages[index].Role == "tool" {
			pendingTools = true
			continue
		}
		if pendingTools && messages[index].Role != "assistant" {
			continue
		}
		pendingTools = false
		start = index
		if messages[index].Role == "assistant" {
			groups++
		}
		if messages[index].Role == "user" || groups >= 2 {
			break
		}
	}
	return start
}

func contextAnchors(history []domain.Message, role string, start int) []domain.Message {
	indices := []int{}
	for index, message := range history {
		if role == "approver" && message.Role == "event" {
			indices = []int{index}
		} else if role != "approver" && message.Role == "user" {
			indices = append(indices, index)
		}
	}
	if len(indices) > 3 {
		indices = append(indices[:1], indices[len(indices)-2:]...)
	}
	result := []domain.Message{}
	for _, index := range indices {
		if index < start {
			result = append(result, history[index])
		}
	}
	return result
}

// Compaction needs semantic conversation text, not database IDs, timestamps,
// duplicate message text or opaque encrypted reasoning/signature blobs.
func compactionTranscript(previous string, messages []domain.Message) string {
	var transcript strings.Builder
	if previous != "" {
		transcript.WriteString("Previous retained summary:\n" + previous + "\n\n")
	}
	for _, message := range messages {
		transcript.WriteString(strings.ToUpper(message.Role) + ":\n")
		transcript.WriteString(message.Content)
		for _, call := range storedCalls(message) {
			transcript.WriteString("\nTool " + call.Name + ": " + JSON(call.Arguments))
		}
		if message.Role == "tool" && message.ID != "" {
			transcript.WriteString("\nStored tool output ID: " + message.ID)
		}
		transcript.WriteString("\n\n")
	}
	return transcript.String()
}
