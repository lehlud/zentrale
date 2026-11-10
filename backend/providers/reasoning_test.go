package providers

import (
	"github.com/lehlud/zentrale/domain"
	"reflect"
	"testing"
)

func TestReasoningPayloads(t *testing.T) {
	for _, tc := range []struct {
		kind, model, effort, protocol, key string
		want                               any
	}{
		{"openai", "gpt-5.4", "xhigh", "responses", "reasoning", map[string]any{"effort": "xhigh"}},
		{"litellm", "o3", "xhigh", "chat", "reasoning_effort", "high"},
		{"openrouter", "openai/gpt-5.4", "medium", "chat", "reasoning", map[string]any{"effort": "medium"}},
		{"anthropic", "claude-sonnet-4-6", "medium", "anthropic", "output_config", map[string]any{"effort": "medium"}},
		{"anthropic", "claude-sonnet-4-5", "high", "anthropic", "thinking", map[string]any{"type": "enabled", "budget_tokens": 6144}},
		{"gemini", "gemini-3-flash-preview", "medium", "gemini", "generationConfig", map[string]any{"thinkingConfig": map[string]any{"thinkingLevel": "medium"}}},
		{"gemini", "gemini-2.5-pro", "high", "gemini", "generationConfig", map[string]any{"thinkingConfig": map[string]any{"thinkingBudget": 24576}}},
	} {
		t.Run(tc.kind+"/"+tc.model, func(t *testing.T) {
			body := map[string]any{}
			applyReasoning(body, Request{Config: domain.Provider{Kind: tc.kind}, Model: domain.LanguageModel{ID: tc.model}, ReasoningLevel: tc.effort}, tc.protocol)
			if !reflect.DeepEqual(body[tc.key], tc.want) {
				t.Fatalf("payload = %#v, want %#v", body, tc.want)
			}
		})
	}
}

func TestReasoningOmittedForUnknownAndDefault(t *testing.T) {
	for _, tc := range []struct{ model, effort string }{{"gpt-4.1", "xhigh"}, {"gpt-5.4", "default"}, {"gpt-5.4", ""}} {
		for _, protocol := range []string{"responses", "chat", "anthropic", "gemini"} {
			body := map[string]any{}
			applyReasoning(body, Request{Config: domain.Provider{Kind: "openai"}, Model: domain.LanguageModel{ID: tc.model}, ReasoningLevel: tc.effort}, protocol)
			if len(body) != 0 {
				t.Fatalf("unexpected reasoning parameters: %#v", body)
			}
		}
	}
}
