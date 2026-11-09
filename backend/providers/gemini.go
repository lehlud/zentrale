package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

func (client *HTTPClient) gemini(ctx context.Context, request Request) (Result, error) {
	contents := []any{}
	for _, message := range request.Messages {
		switch {
		case message.Role == "tool":
			contents = append(contents, map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{
						"functionResponse": map[string]any{
							"name": message.ToolName,
							"response": map[string]any{
								"result": message.Content,
							},
						},
					},
				},
			})
		case message.Wire != "":
			var item any
			if json.Unmarshal([]byte(message.Wire), &item) == nil {
				contents = append(contents, item)
			}
		default:
			role := conversationRole(message.Role)
			if role == "assistant" {
				role = "model"
			}

			contents = append(contents, map[string]any{
				"role": role,
				"parts": []any{
					map[string]any{
						"text": message.Content,
					},
				},
			})
		}
	}

	tools := []any{}
	for _, tool := range request.Tools {
		tools = append(tools, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  tool.Schema,
		})
	}

	body := map[string]any{
		"contents":          contents,
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": request.System}}},
	}

	if len(tools) > 0 {
		body["tools"] = []any{map[string]any{"functionDeclarations": tools}}
	}

	endpoint := "/models/" + url.PathEscape(request.Model.ID) + ":generateContent"

	if request.OnText != nil {
		endpoint = "/models/" + url.PathEscape(request.Model.ID) + ":streamGenerateContent?alt=sse"
	}

	data, err := client.post(ctx, request.Config, endpoint, body, request.OnText)
	if err != nil {
		return Result{}, err
	}

	candidates := asArray(data["candidates"])
	if len(candidates) == 0 {
		return Result{}, errors.New("Gemini returned no candidates")
	}

	content := asObject(asObject(candidates[0])["content"])
	result := Result{Wire: encodeWire(content)}

	for index, part := range asArray(content["parts"]) {
		part := asObject(part)
		if part["thought"] != true {
			result.Text += asString(part["text"])
		}

		if part["functionCall"] != nil {
			f := asObject(part["functionCall"])
			result.Calls = append(result.Calls, Call{
				fmt.Sprintf("gemini-%d", index),
				asString(f["name"]),
				asObject(f["args"]),
			})
		}
	}

	usage := asObject(data["usageMetadata"])
	result.UsageKnown = usage["promptTokenCount"] != nil && usage["candidatesTokenCount"] != nil
	result.InputTokens = asInt(usage["promptTokenCount"])
	result.OutputTokens = asInt(usage["candidatesTokenCount"]) + asInt(usage["thoughtsTokenCount"])
	result.CachedTokens = asInt(usage["cachedContentTokenCount"])
	result.CacheReadKnown = usage["cachedContentTokenCount"] != nil

	return result, nil
}
