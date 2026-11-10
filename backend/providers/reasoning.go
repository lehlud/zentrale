package providers

import (
	"github.com/lehlud/zentrale/domain"
	"strings"
)

// applyReasoning runs after building a request and before either streaming or
// non-streaming transport, so effort survives tool loops and context reviews.
func applyReasoning(body map[string]any, request Request, protocol string) {
	if request.ReasoningLevel == "" || request.ReasoningLevel == "default" {
		return
	}
	level := domain.ResolveReasoningLevel(request.Config.Kind, request.Model.ID, request.ReasoningLevel, "main")
	if level == "default" {
		return
	}
	switch protocol {
	case "responses":
		body["reasoning"] = map[string]any{"effort": level}
	case "chat":
		if request.Config.Kind == "openrouter" {
			body["reasoning"] = map[string]any{"effort": level}
		} else if request.Config.Kind != "mistral" {
			body["reasoning_effort"] = level
		}
	case "anthropic":
		id := request.Model.ID
		if strings.Contains(id, "4-6") || strings.Contains(id, "4.6") {
			body["thinking"] = map[string]any{"type": "adaptive"}
			body["output_config"] = map[string]any{"effort": level}
		} else {
			budget := map[string]int{"low": 1024, "medium": 4096, "high": 6144}[level]
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
	case "gemini":
		thinking := map[string]any{}
		if strings.Contains(request.Model.ID, "gemini-3") {
			thinking["thinkingLevel"] = level
		} else {
			thinking["thinkingBudget"] = map[string]int{"low": 1024, "medium": 8192, "high": 24576}[level]
		}
		body["generationConfig"] = map[string]any{"thinkingConfig": thinking}
	}
}
