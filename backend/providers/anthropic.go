package providers

import (
	"context"
	"encoding/json"
)

func (client *HTTPClient) anthropic(ctx context.Context, request Request) (Result, error) {
	messages := []any{}
	for _, message := range request.Messages {
		if message.Role == "tool" {
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": []any{map[string]any{"type": "tool_result", "tool_use_id": message.CallID, "content": message.Content}},
			})
		} else if message.Wire != "" {
			var item any
			if json.Unmarshal([]byte(message.Wire), &item) == nil {
				messages = append(messages, map[string]any{"role": "assistant", "content": item})
			}
		} else {
			messages = append(messages, map[string]any{"role": conversationRole(message.Role), "content": message.Content})
		}
	}
	tools := []any{}
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{"name": tool.Name, "description": tool.Description, "input_schema": tool.Schema})
	}
	body := map[string]any{
		"model":      request.Model.ID,
		"max_tokens": 8192,
		"system": []any{
			map[string]any{"type": "text", "text": request.System, "cache_control": map[string]any{"type": "ephemeral"}},
		},
		"messages": messages,
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	if request.OnText != nil {
		body["stream"] = true
	}
	data, err := client.post(ctx, request.Config, "/messages", body, request.OnText)
	if err != nil {
		return Result{}, err
	}
	result := Result{Wire: encodeWire(data["content"])}
	for _, item := range asArray(data["content"]) {
		item := asObject(item)
		switch asString(item["type"]) {
		case "text":
			result.Text += asString(item["text"])
		case "tool_use":
			result.Calls = append(result.Calls, Call{asString(item["id"]), asString(item["name"]), asObject(item["input"])})
		}
	}
	usage := asObject(data["usage"])
	result.UsageKnown = usage["input_tokens"] != nil && usage["output_tokens"] != nil
	result.CachedTokens = asInt(usage["cache_read_input_tokens"])
	result.CacheReadKnown = usage["cache_read_input_tokens"] != nil
	result.CacheWriteTokens = asInt(usage["cache_creation_input_tokens"])
	result.CacheWriteKnown = usage["cache_creation_input_tokens"] != nil
	result.InputTokens = asInt(usage["input_tokens"]) + result.CachedTokens + result.CacheWriteTokens
	result.OutputTokens = asInt(usage["output_tokens"])
	return result, nil
}
