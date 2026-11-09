package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestAllProviderProtocolsAndReplay(tool *testing.T) {
	tests := []struct{ kind, path, response string }{
		{
			"openai",
			"/responses",
			`{"output":[{"type":"reasoning","id":"reason-1","summary":[]},{"type":"function_call","call_id":"call-1","name":"harness","arguments":"{\"action\":\"note_list\"}"}],"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":10,"cache_write_tokens_details":{"ttl_12h_tokens":4}}}}`,
		},
		{
			"anthropic",
			"/messages",
			`{"content":[{"type":"thinking","thinking":"provider reasoning","signature":"signature"},{"type":"tool_use","id":"call-1","name":"harness","input":{"action":"note_list"}}],"usage":{"input_tokens":30,"cache_read_input_tokens":60,"cache_creation_input_tokens":10,"output_tokens":20}}`,
		},
		{
			"gemini",
			"/models/test-model:generateContent",
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"harness","args":{"action":"note_list"}},"thoughtSignature":"signature"}]}}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"cachedContentTokenCount":60}}`,
		},
	}
	chatResponse := `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"harness","arguments":"{\"action\":\"note_list\"}"}}]}}],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":60},"cost":0.001}}`
	for _, kind := range []string{"openrouter", "opencode", "mistral", "litellm"} {
		tests = append(tests, struct{ kind, path, response string }{kind, "/chat/completions", chatResponse})
	}
	for _, test := range tests {
		tool.Run(test.kind, func(tool *testing.T) {
			requests := []map[string]any{}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					tool.Errorf("path: %s", request.URL.Path)
				}
				switch test.kind {
				case "anthropic":
					if request.Header.Get("x-api-key") != "secret" {
						tool.Error("missing Anthropic credential")
					}
				case "gemini":
					if request.Header.Get("x-goog-api-key") != "secret" {
						tool.Error("missing Gemini credential")
					}
				default:
					if request.Header.Get("Authorization") != "Bearer secret" {
						tool.Error("missing bearer credential")
					}
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					tool.Error(err)
				}
				requests = append(requests, body)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(test.response))
			}))
			defer server.Close()
			client := New()
			request := Request{
				Config:   domain.Provider{Kind: test.kind, BaseURL: server.URL, Credential: "secret"},
				Model:    domain.LanguageModel{ID: "test-model"},
				System:   "Test system",
				Messages: []domain.Message{{Role: "user", Content: "List notes"}},
				Tools:    []Tool{{Name: "harness", Schema: map[string]any{"type": "object", "properties": map[string]any{}}}},
				CacheKey: "separate-agent-context",
			}
			result, err := client.Complete(context.Background(), request)
			if err != nil {
				tool.Fatal(err)
			}
			if len(result.Calls) != 1 || result.Calls[0].Name != "harness" || result.Calls[0].Arguments["action"] != "note_list" {
				tool.Fatalf("tool call: %+v", result)
			}
			if result.InputTokens != 100 || result.OutputTokens != 20 || result.CachedTokens != 60 || !result.UsageKnown {
				tool.Fatalf("usage normalization: %+v", result)
			}
			if !result.CacheReadKnown || (test.kind == "openai" && (!result.CacheWriteKnown || result.CacheWriteTokens != 10 || result.CacheWriteLongTokens != 4)) {
				tool.Fatalf("cache metadata: %+v", result)
			}
			request.Messages = append(request.Messages, domain.Message{Role: "assistant", Wire: result.Wire}, domain.Message{Role: "tool", CallID: result.Calls[0].ID, ToolName: "harness", Content: "[]"})
			if _, err := client.Complete(context.Background(), request); err != nil {
				tool.Fatal(err)
			}
			replay := encodeWire(requests[1])
			if !strings.Contains(replay, "[]") {
				tool.Fatal("tool result not replayed")
			}
			if test.kind == "openai" && requests[0]["prompt_cache_key"] != "separate-agent-context" {
				tool.Fatal("agent cache key missing")
			}
			if test.kind == "anthropic" && !strings.Contains(replay, "cache_control") {
				tool.Fatal("Anthropic prompt caching missing")
			}
			if (test.kind == "gemini" || test.kind == "anthropic") && !strings.Contains(replay, "signature") {
				tool.Fatal("provider reasoning signature lost during replay")
			}
		})
	}
}

func TestSubscriptionGatewayAndCredentialErrors(tool *testing.T) {
	configuration := domain.Provider{Kind: "openai", AuthMode: "subscription"}
	if err := Validate(&configuration); err == nil {
		tool.Fatal("subscription silently used an API-billed endpoint")
	}
	configuration.AuthMode = "api"
	configuration.BaseURL = "http://remote.example/v1"
	if err := Validate(&configuration); err == nil {
		tool.Fatal("credentials could be sent over remote plaintext HTTP")
	}
}

func TestNativeAccountStreamingInference(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer account-access" || request.Header.Get("ChatGPT-Account-Id") != "account-one" {
			t.Error("native account headers missing")
		}
		var body map[string]any
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["model"] != "native-model" || body["stream"] != true || body["store"] != false {
			t.Fatalf("native request: %+v", body)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n"))
		_, _ = writer.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"reasoning\",\"encrypted_content\":\"opaque\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}],\"usage\":{\"input_tokens\":50,\"output_tokens\":2}}}\n\n"))
	}))
	defer server.Close()
	result, err := New().Complete(context.Background(), Request{Config: domain.Provider{Kind: "openai", AuthMode: "device", BaseURL: server.URL, OAuth: &domain.OAuthTokens{AccessToken: "account-access", AccountID: "account-one"}}, Model: domain.LanguageModel{ID: "native-model"}, System: "Test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "OK" || result.InputTokens != 50 || !strings.Contains(result.Wire, "opaque") {
		t.Fatalf("native response: %+v", result)
	}
	if _, err := readResponseStream(strings.NewReader("data: {\"type\":\"response.failed\"}\n\n")); err == nil {
		t.Fatal("failed stream accepted")
	}
	if _, err := readResponseStream(strings.NewReader("data: {\"type\":\"response.created\"}\n\n")); err == nil {
		t.Fatal("truncated stream accepted")
	}
}
