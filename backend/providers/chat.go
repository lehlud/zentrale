package providers

import (
	"context"
	"encoding/json"
	"errors"
)

func (client *HTTPClient) chat(ctx context.Context, request Request) (Result, error) {
	messages := []any{map[string]any{"role": "system", "content": request.System}}
	for _, message := range request.Messages {
		if message.Role == "tool" {
			messages = append(messages, map[string]any{
				"role":         "tool",
				"tool_call_id": message.CallID,
				"content":      message.Content,
			})
		} else if message.Wire != "" {
			var item any
			if json.Unmarshal([]byte(message.Wire), &item) == nil {
				messages = append(messages, item)
			}
		} else {
			messages = append(messages, map[string]any{
				"role":    conversationRole(message.Role),
				"content": message.Content,
			})
		}
	}

	tools := []any{}
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Schema,
			},
		})
	}

	body := map[string]any{"model": request.Model.ID, "messages": messages}

	if request.OnText != nil {
		body["stream"] = true

		if request.Config.Kind != "mistral" {
			body["stream_options"] = map[string]any{"include_usage": true}
		}
	}

	if len(tools) > 0 {
		body["tools"] = tools
	}

	applyReasoning(body, request, "chat")
	data, err := client.post(ctx, request.Config, "/chat/completions", body, request.OnText)
	if err != nil {
		return Result{}, err
	}

	choices := asArray(data["choices"])
	if len(choices) == 0 {
		return Result{}, errors.New("provider returned no choices")
	}

	message := asObject(asObject(choices[0])["message"])
	result := Result{Text: chatVisibleText(message["content"]), Wire: encodeWire(message)}
	if result.Text == "" {
		result.Text = asString(message["refusal"])
	}

	for _, item := range asArray(message["tool_calls"]) {
		item := asObject(item)
		f := asObject(item["function"])
		result.Calls = append(result.Calls, Call{
			asString(item["id"]),
			asString(f["name"]),
			parseToolArguments(f["arguments"]),
		})
	}

	usage := asObject(data["usage"])
	result.UsageKnown = usage["prompt_tokens"] != nil && usage["completion_tokens"] != nil
	result.InputTokens = asInt(usage["prompt_tokens"])
	result.OutputTokens = asInt(usage["completion_tokens"])
	result.CachedTokens = asInt(asObject(usage["prompt_tokens_details"])["cached_tokens"])
	result.CacheReadKnown = asObject(usage["prompt_tokens_details"])["cached_tokens"] != nil

	if cost, ok := usage["cost"].(float64); ok {
		result.Cost = &cost
	}

	return result, nil
}
