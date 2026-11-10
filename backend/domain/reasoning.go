package domain

import "strings"

// ReasoningLevels stays conservative for unknown OpenAI-compatible models.
func ReasoningLevels(kind, model string) []string {
	id := strings.ToLower(model)
	levels := []string{}
	switch {
	case strings.Contains(id, "gpt-5"):
		levels = []string{"low", "medium", "high"}
		if strings.Contains(id, "5.1-codex-max") || strings.Contains(id, "5.2") || strings.Contains(id, "5.3") || strings.Contains(id, "5.4") {
			levels = append(levels, "xhigh")
		}
	case strings.Contains(id, "o1") || strings.Contains(id, "o3") || strings.Contains(id, "o4"):
		levels = []string{"low", "medium", "high"}
	case strings.Contains(id, "claude") && (strings.Contains(id, "3-7") || strings.Contains(id, "3.7") || strings.Contains(id, "sonnet-4") || strings.Contains(id, "opus-4") || strings.Contains(id, "haiku-4")):
		levels = []string{"low", "medium", "high"}
	case strings.Contains(id, "gemini-3"):
		levels = []string{"low", "high"}
		if strings.Contains(id, "flash") {
			levels = []string{"minimal", "low", "medium", "high"}
		}
	case strings.Contains(id, "gemini-2.5"):
		levels = []string{"low", "medium", "high"}
	case kind == "openrouter" && (strings.Contains(id, "deepseek-r") || strings.Contains(id, "qwen3") || strings.Contains(id, "qwq")):
		levels = []string{"low", "medium", "high"}
	}
	return levels
}

// ResolveReasoningLevel maps semantic defaults to supported equivalents.
func ResolveReasoningLevel(kind, model, requested, role string) string {
	levels := ReasoningLevels(kind, model)
	if len(levels) == 0 {
		// Unknown capabilities are not evidence that reasoning is unsupported.
		// Keep an explicit user override; automatic defaults remain conservative.
		switch requested {
		case "minimal", "low", "medium", "high", "xhigh":
			return requested
		case "regular":
			return "medium"
		}
		return "default"
	}
	if requested == "" {
		requested = "xhigh"
		if role == "approver" {
			requested = "medium"
		}
	}
	if requested == "regular" || requested == "default" {
		requested = "medium"
	}
	for _, level := range levels {
		if level == requested {
			return level
		}
	}
	if requested == "xhigh" || requested == "high" {
		return levels[len(levels)-1]
	}
	// Binary models have no regular level. Prefer the lower supported effort
	// rather than a provider default which may silently run at maximum effort.
	if requested == "medium" {
		return levels[0]
	}
	return levels[0]
}
