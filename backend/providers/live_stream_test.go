package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type streamingFixture struct {
	kind, path, prefix, suffix string
}

func streamingFixtures() []streamingFixture {
	return []streamingFixture{
		{
			"mistral",
			"/chat/completions",

			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":[{\"type\":\"thinking\",\"thinking\":[{\"type\":\"text\",\"text\":\"private \"}]}]}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":[{\"type\":\"thinking\",\"thinking\":[{\"type\":\"text\",\"text\":\"continuation\"}]},{\"type\":\"text\",\"text\":\"Hel\"}]}}]}\n\n",

			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\",\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"harness\",\"arguments\":\"{\\\"action\\\":\\\"note_list\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":60}}}\n\ndata: [DONE]\n\n",
		},
		{
			"openai",
			"/responses",

			"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\"private\"}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n",

			"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"encrypted_content\":\"signature\"},{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello\"}]},{\"type\":\"function_call\",\"call_id\":\"call\",\"name\":\"harness\",\"arguments\":\"{\\\"action\\\":\\\"note_list\\\"}\"}],\"usage\":{\"input_tokens\":100,\"output_tokens\":20,\"input_tokens_details\":{\"cached_tokens\":60}}}}\n\n",
		},
		{
			"openrouter",
			"/chat/completions",

			"data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"private\",\"reasoning_details\":[{\"index\":0,\"type\":\"reasoning.text\",\"text\":\"private\"}],\"content\":\"Hel\"}}]}\n\n",

			"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"lo\",\"reasoning_details\":[{\"index\":0,\"signature\":\"signature\"}],\"tool_calls\":[{\"index\":0,\"id\":\"call\",\"function\":{\"name\":\"harness\",\"arguments\":\"{\\\"action\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"note_list\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"prompt_tokens_details\":{\"cached_tokens\":60}}}\n\ndata: [DONE]\n\n",
		},
		{
			"anthropic",
			"/messages",

			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":30,\"cache_read_input_tokens\":60,\"cache_creation_input_tokens\":10}}}\n\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"private\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"signature\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hel\"}}\n\n",

			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
				"data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call\",\"name\":\"harness\",\"input\":{}}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"action\\\":\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"note_list\\\"}\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":20}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
		},
		{
			"gemini",
			"/models/test:streamGenerateContent",

			"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"private\",\"thought\":true,\"thoughtSignature\":\"signature\"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"Hel\"}]}}]}\n\n",

			"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"lo\"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"index\":0,\"finishReason\":\"STOP\",\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"harness\",\"args\":{\"action\":\"note_list\"}},\"thoughtSignature\":\"signature\"}]}}],\"usageMetadata\":{\"promptTokenCount\":100,\"candidatesTokenCount\":20,\"cachedContentTokenCount\":60}}\n\n",
		},
	}
}

func TestTextIsDeliveredBeforeEachProviderCompletes(t *testing.T) {
	for _, fixture := range streamingFixtures() {
		t.Run(fixture.kind, func(t *testing.T) {
			release := make(chan struct{})
			resume := sync.OnceFunc(func() { close(release) })
			defer resume()

			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != fixture.path {
					t.Errorf("unexpected streaming endpoint: %s", request.URL)
				}

				var body map[string]any
				_ = json.NewDecoder(request.Body).Decode(&body)

				if fixture.kind == "gemini" {
					if request.URL.Query().Get("alt") != "sse" {
						t.Error("missing Gemini SSE format")
					}
				} else if body["stream"] != true {
					t.Error("streaming was not requested")
				}

				if fixture.kind == "mistral" && body["stream_options"] != nil {
					t.Error("OpenAI-specific usage option sent to Mistral")
				}

				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(writer, fixture.prefix)
				writer.(http.Flusher).Flush()

				select {
				case <-release:
					_, _ = fmt.Fprint(writer, fixture.suffix)
				case <-request.Context().Done():
				}
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			first := make(chan string, 8)
			var text strings.Builder
			type completion struct {
				result Result
				err    error
			}

			finished := make(chan completion, 1)
			go func() {
				result, err := New().Complete(ctx, Request{
					Config: domain.Provider{Kind: fixture.kind, BaseURL: server.URL},
					Model:  domain.LanguageModel{ID: "test"},
					OnText: func(chunk string) {
						text.WriteString(chunk)
						first <- chunk
					},
				})

				finished <- completion{result, err}
			}()

			select {
			case chunk := <-first:
				if chunk != "Hel" {
					t.Fatal("hidden reasoning was exposed as live text", chunk)
				}
			case <-ctx.Done():
				t.Fatal("no live text before response completion")
			}

			select {
			case result := <-finished:
				t.Fatal("provider finished before its terminal event", result)
			default:
			}

			resume()
			completed := <-finished
			result := completed.result
			if completed.err != nil || text.String() != "Hello" || result.Text != "Hello" {
				t.Fatal(text.String(), result, completed.err)
			}

			replayMarker := "signature"

			if fixture.kind == "mistral" {
				replayMarker = "private continuation"
			}

			if len(result.Calls) != 1 ||
				result.Calls[0].Arguments["action"] != "note_list" ||
				!strings.Contains(result.Wire, replayMarker) {
				t.Fatal("tool arguments or replay signature lost", result)
			}

			if result.InputTokens != 100 ||
				result.OutputTokens != 20 ||
				result.CachedTokens != 60 ||
				!result.UsageKnown {
				t.Fatal("stream usage lost", result)
			}
		})
	}
}

func TestTruncatedStreamsNeverReturnExecutableCalls(t *testing.T) {
	for _, fixture := range streamingFixtures() {
		t.Run(fixture.kind, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(writer, fixture.prefix)
			}))
			defer server.Close()

			client := New()
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			result, err := client.Complete(context.Background(), Request{
				Config: domain.Provider{Kind: fixture.kind, BaseURL: server.URL},

				Model:  domain.LanguageModel{ID: "test"},
				OnText: func(string) {},
				// This observer discards all display-only text, including retries.
				// Real visible consumers must reset their draft before replay.
				OnRetry: func(Retry) {},
			})
			if err == nil || len(result.Calls) != 0 || requests.Load() != providerAttempts {
				t.Fatal("a partial generation became executable or retries were unbounded", result, err, requests.Load())
			}
		})
	}
}

func TestCancellingAStreamClosesTheProviderRequest(t *testing.T) {
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(writer, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Partial\"}\n\n")
		writer.(http.Flusher).Flush()
		<-request.Context().Done()
		close(stopped)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := New().Complete(ctx, Request{
		Config: domain.Provider{Kind: "openai", BaseURL: server.URL},

		Model:  domain.LanguageModel{ID: "test"},
		OnText: func(string) { cancel() },
	})
	if err == nil || len(result.Calls) != 0 {
		t.Fatal("cancelled generation was accepted", result, err)
	}

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("provider request remained open after cancellation")
	}
}

func TestVisibleRefusalsAndUnfinishedContentBlocks(t *testing.T) {
	for _, fixture := range []struct{ kind, stream string }{
		{
			"openai",
			"data: {\"type\":\"response.refusal.delta\",\"delta\":\"Cannot comply\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"refusal\",\"refusal\":\"Cannot comply\"}]}]}}\n\n",
		},
		{
			"openrouter",
			"data: {\"choices\":[{\"index\":0,\"delta\":{\"refusal\":\"Cannot comply\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
		},
	} {
		t.Run(fixture.kind, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(writer, fixture.stream)
			}))
			defer server.Close()

			var visible strings.Builder
			result, err := New().Complete(context.Background(), Request{
				Config: domain.Provider{Kind: fixture.kind, BaseURL: server.URL},

				Model: domain.LanguageModel{
					ID: "test",
				},
				OnText: func(text string) { visible.WriteString(text) },
			})
			if err != nil || result.Text != "Cannot comply" || visible.String() != result.Text {
				t.Fatal(result, visible.String(), err)
			}
		})
	}

	for _, fixture := range streamingFixtures() {
		if fixture.kind == "anthropic" {
			_, err := readProviderStream(
				strings.NewReader(fixture.prefix+"data: {\"type\":\"message_stop\"}\n\n"),
				streamRequest{
					kind: "anthropic",
				},
			)
			if err == nil {
				t.Fatal("unfinished Anthropic content block was accepted")
			}
		}
	}
}
