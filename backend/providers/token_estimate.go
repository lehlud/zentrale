package providers

import "encoding/json"

// EstimatedResponsesTokens counts model content rather than its JSON envelope.
// Opaque-state ratios follow Codex's coarse heuristics, not a tokenizer or a
// billing rule. Unknown protocols/items retain their conservative wire estimate.
// Estimation never changes the canonical items sent back to the provider.
func EstimatedResponsesTokens(wire string) (int, bool) {
	var items []json.RawMessage
	if json.Unmarshal([]byte(wire), &items) != nil || len(items) == 0 {
		return 0, false
	}

	tokens, recognized := 0, false
	for _, raw := range items {
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			tokens += (len(raw) + 3) / 4
			continue
		}

		var size int
		switch asString(item["type"]) {
		case "message":
			size = responsesContentBytes(item["content"])
		case "reasoning":
			size = max(0, len(asString(item["encrypted_content"]))*3/4-650)
		case "compaction", "context_compaction":
			// Replay caches can make encrypted checkpoints roughly three times
			// larger than their model-visible content (12 wire bytes/token).
			size = (len(asString(item["encrypted_content"])) + 2) / 3
		case "function_call", "custom_tool_call":
			namespace := asString(item["namespace"])
			if namespace == "" {
				namespace = "functions"
			}

			size = len(asString(item["name"])) + len(namespace) + len(asString(item["arguments"])) + len(asString(item["input"]))
		case "function_call_output", "custom_tool_call_output":
			size = len(asString(item["call_id"])) + responsesContentBytes(item["output"])
		default:
			tokens += (len(raw) + 3) / 4
			continue
		}

		recognized = true
		tokens += (size + 3) / 4
	}

	return tokens, recognized
}

func responsesContentBytes(content any) int {
	if text, ok := content.(string); ok {
		return len(text)
	}

	parts, ok := content.([]any)
	if !ok {
		return len(encodeWire(content))
	}

	size := 0
	for _, value := range parts {
		part := asObject(value)
		switch asString(part["type"]) {
		case "input_text", "output_text":
			size += len(asString(part["text"]))
		case "refusal":
			size += len(asString(part["refusal"]))
		case "input_image":
			// Match Codex's resized-image allowance; base64 length is not the
			// image token count. Original-detail images need a larger budget.
			if asString(part["detail"]) == "original" {
				size += 40000
			} else {
				size += 7373
			}
		default:
			size += len(encodeWire(value))
		}
	}

	return size
}
