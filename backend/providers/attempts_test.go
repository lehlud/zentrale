package providers

import (
	"context"
	"encoding/json"
	"github.com/lehlud/zentrale/domain"
	"testing"
	"time"
)

func TestAttemptUsageKeepsProviderTokenSemantics(t *testing.T) {
	for _, test := range []struct {
		name                  string
		body                  map[string]any
		input, output, cached int
	}{
		{"anthropic", map[string]any{"usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "cache_read_input_tokens": 20, "cache_creation_input_tokens": 5}}, 35, 2, 20},
		{"chat", map[string]any{"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 4, "prompt_tokens_details": map[string]any{"cached_tokens": 10}}}, 30, 4, 10},
		{"gemini", map[string]any{"usageMetadata": map[string]any{"promptTokenCount": 40, "candidatesTokenCount": 5, "thoughtsTokenCount": 6, "cachedContentTokenCount": 12}}, 40, 11, 12},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attempt domain.InferenceAttempt
			ctx := WithAttemptObserver(context.Background(), func(value domain.InferenceAttempt) { attempt = value })
			var body map[string]any
			if err := json.Unmarshal([]byte(encodeWire(test.body)), &body); err != nil {
				t.Fatal(err)
			}
			observeAttempt(ctx, "http", time.Now(), 100, 200, body, nil)
			if !attempt.TokensKnown || !attempt.CacheReadKnown || attempt.InputTokens != test.input || attempt.OutputTokens != test.output || attempt.CachedTokens != test.cached {
				t.Fatal(attempt)
			}
		})
	}
}
