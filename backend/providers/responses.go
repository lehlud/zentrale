package providers

import (
	"context"
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
)

func responsesInput(messages []domain.Message) []any {
	input := []any{}
	for _, message := range messages {
		if message.Role == "tool" {
			input = append(
				input,
				map[string]any{
					"type":    "function_call_output",
					"call_id": message.CallID,
					"output":  message.Content,
				},
			)
		} else if message.Wire != "" {
			var items []any
			if json.Unmarshal([]byte(message.Wire), &items) == nil {
				input = append(input, items...)
			}
		} else {
			input = append(input, map[string]any{
				"role":    conversationRole(message.Role),
				"content": message.Content,
			})
		}
	}

	return input
}

func (client *HTTPClient) responses(ctx context.Context, request Request) (Result, error) {
	input := responsesInput(request.Messages)
	tools := []any{}
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Schema,
			"strict":      false,
		})
	}

	body := map[string]any{
		"model":            request.Model.ID,
		"instructions":     request.System,
		"input":            input,
		"tools":            tools,
		"prompt_cache_key": request.CacheKey,
		"store":            false,
		"include":          []string{"reasoning.encrypted_content"},
	}

	if request.Config.AuthMode == "device" || request.OnText != nil {
		body["stream"] = true
	}

	data, err := client.post(ctx, request.Config, "/responses", body, request.OnText)
	if err != nil {
		return Result{}, err
	}

	if data["error"] != nil {
		return Result{}, responseFailure(data)
	}

	if status := asString(data["status"]); status != "" && status != "completed" {
		return Result{}, responseFailure(data)
	}

	result := Result{Wire: encodeWire(data["output"])}

	for _, item := range asArray(data["output"]) {
		item := asObject(item)

		switch asString(item["type"]) {
		case "message":
			// Preserve phase in both the raw replay and turn control. A standalone
			// commentary message is progress, not a completed user task.
			result.Phase = asString(item["phase"])

			for _, part := range asArray(item["content"]) {
				part := asObject(part)
				if asString(part["type"]) == "output_text" {
					result.Text += asString(part["text"])
				} else if asString(part["type"]) == "refusal" {
					result.Text += asString(part["refusal"])
				}
			}
		case "function_call":
			result.Calls = append(
				result.Calls,
				Call{
					asString(item["call_id"]),
					asString(item["name"]),
					parseToolArguments(item["arguments"]),
				},
			)
		}
	}

	readResponsesUsage(&result, data["usage"])

	return result, nil
}

// Cache reads and writes are disjoint subsets of input_tokens. Keep missing
// metadata distinct from an explicitly reported zero, including on compaction.
func readResponsesUsage(result *Result, value any) {
	usage := asObject(value)
	result.UsageKnown = usage["input_tokens"] != nil && usage["output_tokens"] != nil
	result.InputTokens = asInt(usage["input_tokens"])
	result.OutputTokens = asInt(usage["output_tokens"])

	details := asObject(usage["input_tokens_details"])
	result.CachedTokens = asInt(details["cached_tokens"])
	result.CacheReadKnown = details["cached_tokens"] != nil

	result.CacheWriteTokens = asInt(details["cache_write_tokens"])
	result.CacheWriteKnown = details["cache_write_tokens"] != nil
	result.CacheWriteLongTokens = asInt(asObject(details["cache_write_tokens_details"])["ttl_12h_tokens"])
}
