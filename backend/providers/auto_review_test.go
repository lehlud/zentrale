package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestAutoReviewUsesNormalAccountResponsesWithStructuredFinalOutput(t *testing.T) {
	schema := map[string]any{"type": "object", "additionalProperties": false,
		"properties": map[string]any{"allowed": map[string]any{"type": "boolean"}, "reason": map[string]any{"type": "string"}, "blanket": map[string]any{"type": "boolean"}},
		"required":   []string{"allowed", "reason", "blanket"}}
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer review-access" || r.Header.Get("ChatGPT-Account-Id") != "review-account" {
			t.Error("unexpected review endpoint or auth")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		if body["model"] != "codex-auto-review" || body["stream"] != true || body["store"] != false || body["prompt_cache_key"] != "family-reviewer" {
			t.Errorf("review request: %v", body)
		}
		format := asObject(asObject(body["text"])["format"])
		if format["type"] != "json_schema" || format["strict"] != true || format["name"] != "zentrale_approval" {
			t.Errorf("missing structured final contract: %v", format)
		}
		want, _ := json.Marshal(schema)
		got, _ := json.Marshal(format["schema"])
		if string(got) != string(want) {
			t.Error("review schema changed")
		}
		if !reflect.DeepEqual(body["reasoning"], map[string]any{"effort": "medium"}) {
			t.Error("configured reviewer reasoning lost")
		}
		output := []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": `{"allowed":false,"reason":"Outside user scope","blanket":false}`}}}}
		if len(bodies) == 1 {
			output = []any{map[string]any{"type": "function_call", "call_id": "inspect-1", "name": "inspect", "arguments": `{"kind":"instructions"}`}}
		}
		response, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"status": "completed", "output": output}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\n", response)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request := Request{Config: domain.Provider{Kind: "openai", AuthMode: "device", BaseURL: server.URL, OAuth: &domain.OAuthTokens{AccessToken: "review-access", AccountID: "review-account"}},
		Model: domain.LanguageModel{ID: "codex-auto-review", ReasoningLevels: []string{"low", "medium", "high"}}, ReasoningLevel: "medium", CacheKey: "family-reviewer", OutputSchema: schema,
		System: "Independent approval; strict JSON allowed/reason/blanket.", Tools: []Tool{{Name: "inspect", Schema: map[string]any{"type": "object"}}}}
	first, err := New().Complete(ctx, request)
	if err != nil || len(first.Calls) != 1 {
		t.Fatalf("inspection round: %+v %v", first, err)
	}
	request.Messages = []domain.Message{{Role: "assistant", Wire: first.Wire}, {Role: "tool", CallID: "inspect-1", Content: "Original human instructions"}}
	last, err := New().Complete(ctx, request)
	if err != nil || last.Text != `{"allowed":false,"reason":"Outside user scope","blanket":false}` || len(last.Calls) != 0 {
		t.Fatalf("decision round: %+v %v", last, err)
	}
	if len(bodies) != 2 || len(asArray(bodies[1]["input"])) != 2 {
		t.Fatal("inspection evidence was not replayed")
	}
}
