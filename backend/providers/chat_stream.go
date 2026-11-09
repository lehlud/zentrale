package providers

import (
	"errors"
	"slices"
)

type chatStream struct {
	request streamRequest
	message map[string]any
	tools   map[int]map[string]any
	details map[int]map[string]any
	usage   any
	finish  string
	content []any
}

func newChatStream(request streamRequest) *chatStream {
	return &chatStream{
		request: request, message: map[string]any{"role": "assistant", "content": ""},
		tools: map[int]map[string]any{}, details: map[int]map[string]any{},
	}
}

func (stream *chatStream) consume(event map[string]any) (bool, error) {
	if event["usage"] != nil {
		stream.usage = event["usage"]
	}
	for _, value := range asArray(event["choices"]) {
		choice := asObject(value)
		if asInt(choice["index"]) != 0 {
			continue
		}
		if reason := asString(choice["finish_reason"]); reason != "" {
			stream.finish = reason
		}
		delta := asObject(choice["delta"])
		if delta["content"] != nil {
			stream.content = append(stream.content, delta["content"])
			stream.request.text(chatVisibleText(delta["content"]))
		}
		for _, key := range []string{"refusal", "reasoning", "reasoning_content"} {
			if text := asString(delta[key]); text != "" {
				stream.message[key] = asString(stream.message[key]) + text
				if key == "refusal" {
					stream.request.text(text)
				}
			}
		}
		for _, value := range asArray(delta["tool_calls"]) {
			call := asObject(value)
			index := asInt(call["index"])
			target := stream.tools[index]
			if target == nil {
				target = map[string]any{"type": "function", "function": map[string]any{}}
				stream.tools[index] = target
			}
			if id := asString(call["id"]); id != "" {
				target["id"] = id
			}
			function := asObject(target["function"])
			for _, key := range []string{"name", "arguments"} {
				function[key] = asString(function[key]) + asString(asObject(call["function"])[key])
			}
		}
		// Reasoning records are retained for tool replay, never published.
		for _, value := range asArray(delta["reasoning_details"]) {
			detail := asObject(value)
			index := asInt(detail["index"])
			if stream.details[index] == nil {
				stream.details[index] = map[string]any{}
			}
			for key, value := range detail {
				switch key {
				case "text", "summary", "data", "signature":
					stream.details[index][key] = asString(stream.details[index][key]) + asString(value)
				default:
					if value != nil {
						stream.details[index][key] = value
					}
				}
			}
		}
	}
	return false, nil
}

func orderedStreamItems(items map[int]map[string]any) []any {
	indices := make([]int, 0, len(items))
	for index := range items {
		indices = append(indices, index)
	}
	slices.Sort(indices)
	result := make([]any, 0, len(indices))
	for _, index := range indices {
		result = append(result, items[index])
	}
	return result
}

func (stream *chatStream) result() (map[string]any, error) {
	switch stream.finish {
	case "stop", "tool_calls", "function_call":
	default:
		return nil, errors.New("provider stream ended before completion")
	}
	if len(stream.tools) > 0 {
		stream.message["tool_calls"] = orderedStreamItems(stream.tools)
	}
	stream.message["content"] = assembleChatContent(stream.content)
	if len(stream.details) > 0 {
		stream.message["reasoning_details"] = orderedStreamItems(stream.details)
	}
	return map[string]any{
		"choices": []any{map[string]any{"message": stream.message}}, "usage": stream.usage,
	}, nil
}
