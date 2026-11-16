package providers

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestResponsesEstimateCountsContentAndOpaqueStateWithoutChangingReplay(t *testing.T) {
	wire := encodeWire([]any{
		map[string]any{"type": "message", "id": strings.Repeat("metadata", 1000), "role": "assistant", "content": []any{
			map[string]any{"type": "output_text", "text": strings.Repeat("\"\n", 200)},
		}},
		map[string]any{"type": "reasoning", "encrypted_content": strings.Repeat("x", 1000)},
		map[string]any{"type": "compaction", "encrypted_content": strings.Repeat("x", 72000)},
	})
	tokens, ok := EstimatedResponsesTokens(wire)
	// 400 visible bytes / 4 + (1000 * .75 - 650) / 4 + 72000 / 12.
	if !ok || tokens != 6125 {
		t.Fatal(tokens, ok)
	}

	replay := responsesInput([]domain.Message{{Role: "context", Wire: wire}})
	if encodeWire(replay) != wire {
		t.Fatal("estimation changed native replay")
	}

	t.Logf("same native window: wire estimate %d tokens, content estimate %d", (len(wire)+3)/4, tokens)
}

func TestResponsesEstimateFallsBackForOtherProtocolsAndUnknownItems(t *testing.T) {
	for _, wire := range []string{
		`{"role":"assistant","content":"chat"}`,
		`[{"type":"text","text":"Anthropic"}]`,
		`[{"type":"tool_use","input":{"key":"value"}}]`,
		`not JSON`, `[]`, `null`,
	} {
		if _, ok := EstimatedResponsesTokens(wire); ok {
			t.Fatal("treated another protocol as Responses", wire)
		}
	}

	unknown := `{"type":"future_item","payload":"` + strings.Repeat("u", 1000) + `"}`
	tokens, ok := EstimatedResponsesTokens(`[{"type":"reasoning","encrypted_content":"short"},` + unknown + `]`)
	if !ok || tokens != (len(unknown)+3)/4 {
		t.Fatal("unknown item was discarded from the estimate", tokens, ok)
	}
}

func TestResponsesEstimateIncludesCallsResultsAndImages(t *testing.T) {
	for _, test := range []struct {
		name string
		item map[string]any
		want int
	}{
		{"call", map[string]any{"type": "function_call", "name": "exec", "arguments": `{"cmd":"true"}`}, 7},
		{"result", map[string]any{"type": "function_call_output", "call_id": "call", "output": "done"}, 2},
		{"image", map[string]any{"type": "message", "content": []any{
			map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + strings.Repeat("x", 100000)},
		}}, 1844},
		{"original image", map[string]any{"type": "message", "content": []any{
			map[string]any{"type": "input_image", "detail": "original", "image_url": "file-id"},
		}}, 10000},
	} {
		t.Run(test.name, func(t *testing.T) {
			tokens, ok := EstimatedResponsesTokens(encodeWire([]any{test.item}))
			if !ok || tokens != test.want {
				t.Fatal(tokens, ok)
			}
		})
	}
}
