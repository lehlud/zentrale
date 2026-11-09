package providers

import "strings"

// Mistral reasoning models switch between typed content blocks and strings
// while streaming. Only ordinary text belongs in the visible answer.
func chatVisibleText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	var text strings.Builder
	for _, value := range asArray(value) {
		part := asObject(value)
		if part["type"] == "text" {
			text.WriteString(asString(part["text"]))
		}
	}
	return text.String()
}

// Reassemble the original content shape, coalescing fragments so replay does not
// grow a separate JSON block for every token. Thinking blocks remain in Wire.
func assembleChatContent(fragments []any) any {
	var parts []any
	var text strings.Builder
	structured := false
	flushText := func() {
		if text.Len() > 0 {
			parts = append(parts, map[string]any{"type": "text", "text": text.String()})
			text.Reset()
		}
	}
	var visit func(any)
	visit = func(value any) {
		switch value := value.(type) {
		case string:
			text.WriteString(value)
		case []any:
			for _, part := range value {
				visit(part)
			}
		case map[string]any:
			structured = true
			if value["type"] == "text" {
				text.WriteString(asString(value["text"]))
				return
			}
			flushText()
			if value["type"] == "thinking" && len(parts) > 0 {
				previous := asObject(parts[len(parts)-1])
				if previous["type"] == "thinking" {
					previous["thinking"] = append(asArray(previous["thinking"]), asArray(value["thinking"])...)
					for key, metadata := range value {
						if key != "thinking" && metadata != nil {
							previous[key] = metadata
						}
					}
					return
				}
			}
			parts = append(parts, value)
		}
	}
	for _, fragment := range fragments {
		visit(fragment)
	}
	flushText()
	if !structured {
		if len(parts) == 0 {
			return ""
		}
		return asString(asObject(parts[0])["text"])
	}
	for _, value := range parts {
		part := asObject(value)
		if part["type"] == "thinking" {
			thinking := assembleChatContent(asArray(part["thinking"]))
			if text, plain := thinking.(string); plain {
				thinking = []any{}
				if text != "" {
					thinking = []any{map[string]any{"type": "text", "text": text}}
				}
			}
			part["thinking"] = thinking
		}
	}
	return parts
}
