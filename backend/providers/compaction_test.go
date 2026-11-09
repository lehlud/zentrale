package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (transport fixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func fixtureResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestNativeCompactionCanonicalWindowAndCapabilityErrors(t *testing.T) {
	window := `[ {"type":"message","role":"user","content":"Keep this original"}, {"type":"compaction","encrypted_content":"opaque-checkpoint"} ]`
	client := New()
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/responses/compact" {
			t.Fatal(request.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["model"] != "test" || !strings.Contains(encodeWire(body["input"]), "original-tool-state") {
			t.Fatal(body)
		}
		return fixtureResponse(200, `{"output":`+window+`,"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":30,"cache_write_tokens":50}}}`), nil
	})
	request := Request{Config: domain.Provider{Kind: "openai", BaseURL: "https://api.openai.com/v1"}, Model: domain.LanguageModel{ID: "test"}, Messages: []domain.Message{{Role: "assistant", Wire: `[{"type":"reasoning","encrypted_content":"original-tool-state"}]`}}}
	result, err := client.Compact(context.Background(), request)
	if err != nil || result.InputTokens != 100 || !result.UsageKnown || !result.CacheWriteKnown || result.CacheWriteTokens != 50 || result.CachedTokens != 30 {
		t.Fatal(result, err)
	}
	var expected any
	_ = json.Unmarshal([]byte(window), &expected)
	if result.Wire != encodeWire(expected) {
		t.Fatal("canonical output was pruned or reordered", result.Wire)
	}
	request.Messages = []domain.Message{{Role: "context", Wire: result.Wire}, {Role: "context", Content: "Current plan"}}
	input := responsesInput(request.Messages)
	if !reflect.DeepEqual(input[:2], asArray(expected)) {
		t.Fatal("canonical window changed during replay")
	}
	for _, status := range []int{400, 422, 404, 405, 501, 401, 403, 429, 500} {
		client.HTTP.Transport = fixtureTransport(func(*http.Request) (*http.Response, error) {
			return fixtureResponse(status, `{"error":{"code":"request_failed"}}`), nil
		})
		_, err := client.Compact(context.Background(), request)
		unsupported := status == 400 || status == 422 || status == 404 || status == 405 || status == 501
		if err == nil || errors.Is(err, ErrCompactionUnsupported) != unsupported {
			t.Fatal(status, err)
		}
	}
	client.HTTP.Transport = fixtureTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsupported provider contacted native route")
		return nil, nil
	})
	for _, kind := range []string{"anthropic", "gemini", "openrouter", "opencode", "mistral", "litellm"} {
		request.Config.Kind = kind
		if _, err := client.Compact(context.Background(), request); !errors.Is(err, ErrCompactionUnsupported) {
			t.Fatal(kind, err)
		}
	}
}

func TestRuntimeContextPrefixesAcrossProviderProtocols(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic", "gemini", "openrouter", "mistral", "litellm", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			bodies := []map[string]any{}
			client := New()
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				var body map[string]any
				_ = json.NewDecoder(request.Body).Decode(&body)
				bodies = append(bodies, body)
				reply := `{"choices":[{"message":{"role":"assistant","content":"Done"}}]}`
				switch kind {
				case "openai":
					reply = `{"output":[{"type":"message","content":[{"type":"output_text","text":"Done"}]}]}`
				case "anthropic":
					reply = `{"content":[{"type":"text","text":"Done"}]}`
				case "gemini":
					reply = `{"candidates":[{"content":{"role":"model","parts":[{"text":"Done"}]}}]}`
				}
				return fixtureResponse(200, reply), nil
			})
			request := Request{Config: domain.Provider{Kind: kind, BaseURL: "https://provider.example/v1"}, Model: domain.LanguageModel{ID: "test"}, System: "Stable instructions", Messages: []domain.Message{{Role: "context", Content: "Initial state"}, {Role: "user", Content: "Original task"}}}
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			request.Messages = append(request.Messages, domain.Message{Role: "context", Content: "Updated plan"})
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			field := "messages"
			if kind == "openai" {
				field = "input"
			}
			if kind == "gemini" {
				field = "contents"
			}
			first, second := asArray(bodies[0][field]), asArray(bodies[1][field])
			if len(second) != len(first)+1 || !reflect.DeepEqual(first, second[:len(first)]) {
				t.Fatal("context update rewrote serialized prefix", kind)
			}
		})
	}
}
