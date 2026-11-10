package domain

import "testing"

func TestReasoningDefaultsAndFallbacks(t *testing.T) {
	for _, tc := range []struct{ kind, model, requested, role, want string }{
		{"openai", "gpt-5.4", "", "main", "xhigh"},
		{"openai", "gpt-5.4", "", "approver", "medium"},
		{"openai", "gpt-5.4", "regular", "main", "medium"},
		{"openai", "gpt-5.4", "default", "main", "medium"},
		{"openai", "o3", "xhigh", "main", "high"},
		{"anthropic", "claude-sonnet-4-6", "xhigh", "main", "high"},
		{"anthropic", "claude-sonnet-4-6", "", "approver", "medium"},
		{"gemini", "gemini-3-pro-preview", "medium", "approver", "low"},
		{"gemini", "gemini-3-flash-preview", "medium", "approver", "medium"},
		{"litellm", "custom-chat-model", "xhigh", "main", "xhigh"},
		{"litellm", "custom-chat-model", "medium", "approver", "medium"},
		{"litellm", "custom-chat-model", "", "main", "default"},
		{"litellm", "custom-chat-model", "default", "main", "default"},
		{"openai", "gpt-4.1", "", "main", "default"},
	} {
		t.Run(tc.kind+"/"+tc.model+"/"+tc.requested+"/"+tc.role, func(t *testing.T) {
			if got := ResolveReasoningLevel(tc.kind, tc.model, tc.requested, tc.role); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
