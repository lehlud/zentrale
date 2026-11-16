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

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/domain"
)

func socketFixtureRequest(address string) Request {
	return Request{Config: domain.Provider{ID: "test", Kind: "openai", AuthMode: "api", BaseURL: address, ResponsesTransport: "websocket", PromptCacheMode: "off"}, Model: domain.LanguageModel{ID: "test"}, System: "Keep the task", CacheKey: "thread", Messages: []domain.Message{{Role: "user", Content: strings.Repeat("original task; ", 500)}}}
}

func fixtureCompleted(id string) map[string]any {
	return map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "done"}}}}, "usage": map[string]any{"input_tokens": 100, "output_tokens": 5, "input_tokens_details": map[string]any{"cached_tokens": 80}}}}
}

func TestResponsesSocketSendsOnlyExactContinuationAndResetsForSettings(t *testing.T) {
	var mutex sync.Mutex
	requests := []map[string]any{}
	var upgrades atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		upgrades.Add(1)
		for {
			var body map[string]any
			if connection.ReadJSON(&body) != nil {
				return
			}
			mutex.Lock()
			requests = append(requests, body)
			number := len(requests)
			mutex.Unlock()
			if connection.WriteJSON(fixtureCompleted(fmt.Sprint(number))) != nil {
				return
			}
		}
	}))
	defer server.Close()
	client := New()
	ctx := WithResponsesTurn(context.Background())
	defer CloseResponsesTurn(ctx)
	attempts := []domain.InferenceAttempt{}
	ctx = WithAttemptObserver(ctx, func(attempt domain.InferenceAttempt) { attempts = append(attempts, attempt) })
	request := socketFixtureRequest(server.URL)
	first, err := client.Complete(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = append(request.Messages, domain.Message{Role: "assistant", Wire: first.Wire}, domain.Message{Role: "user", Content: "continue"})
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.System = "New system instructions"
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	mutex.Lock()
	defer mutex.Unlock()
	if upgrades.Load() != 1 || len(requests) != 3 || requests[1]["previous_response_id"] != "1" || len(asArray(requests[1]["input"])) != 1 || requests[2]["previous_response_id"] != nil || len(asArray(requests[2]["input"])) != 3 || requests[1]["store"] != false || requests[1]["stream"] != nil {
		t.Fatal("invalid incremental requests", requests)
	}
	if len(attempts) != 3 || attempts[0].SentBytes <= attempts[1].SentBytes || !attempts[1].TokensKnown || attempts[1].CachedTokens != 80 {
		t.Fatal("socket attempt metrics lost transfer savings or reported usage", attempts)
	}
}

func TestResponsesAttemptObserverIncludesFailedHTTPAttemptsWithoutInventedUsage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(fixtureCompleted("retry")["response"])
	}))
	defer server.Close()
	client := New()
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	request := socketFixtureRequest(server.URL)
	request.Config.ResponsesTransport = "http"
	attempts := []domain.InferenceAttempt{}
	ctx := WithAttemptObserver(context.Background(), func(attempt domain.InferenceAttempt) { attempts = append(attempts, attempt) })
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != 500 || attempts[0].TokensKnown || attempts[1].Status != 200 || !attempts[1].TokensKnown || attempts[0].SentBytes <= 0 {
		t.Fatal(attempts)
	}
}

func TestResponsesCacheRejectionAfterPublishedTextDoesNotReplay(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: {\"type\":\"error\",\"error\":{\"code\":\"unsupported_parameter\",\"param\":\"prompt_cache_options\"}}\n\n"))
	}))
	defer server.Close()
	request := socketFixtureRequest(server.URL)
	request.Config.ResponsesTransport = "http"
	request.Config.PromptCacheMode = "explicit"
	text := ""
	request.OnText = func(value string) { text += value }
	if _, err := New().Complete(context.Background(), request); err == nil || calls.Load() != 1 || text != "partial" {
		t.Fatal("cache fallback replayed visible text", err, calls.Load(), text)
	}
}

func TestResponsesSocketMissingPreviousUsesFullReplayOnANewConnection(t *testing.T) {
	var count atomic.Int32
	var upgrades atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		upgrades.Add(1)
		for {
			var body map[string]any
			if connection.ReadJSON(&body) != nil {
				return
			}
			number := count.Add(1)
			if number == 2 {
				_ = connection.WriteJSON(map[string]any{"type": "error", "error": map[string]any{"code": "previous_response_not_found"}})
				continue
			}
			if number == 3 && (body["previous_response_id"] != nil || len(asArray(body["input"])) != 3) {
				t.Error("missing state was not replayed in full")
			}
			if connection.WriteJSON(fixtureCompleted(fmt.Sprint(number))) != nil {
				return
			}
		}
	}))
	defer server.Close()
	client := New()
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	ctx := WithResponsesTurn(context.Background())
	defer CloseResponsesTurn(ctx)
	request := socketFixtureRequest(server.URL)
	first, err := client.Complete(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Messages = append(request.Messages, domain.Message{Role: "assistant", Wire: first.Wire}, domain.Message{Role: "user", Content: "continue"})
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	if count.Load() != 3 || upgrades.Load() != 2 {
		t.Fatal(count.Load(), upgrades.Load())
	}
}

func TestResponsesSocketUnsupportedHandshakeFallsBackAndIsRemembered(t *testing.T) {
	var sockets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			sockets.Add(1)
			http.NotFound(w, r)
			return
		}
		posts.Add(1)
		_ = json.NewEncoder(w).Encode(fixtureCompleted("http")["response"])
	}))
	defer server.Close()
	client := New()
	request := socketFixtureRequest(server.URL)
	for range 2 {
		if _, err := client.Complete(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	if sockets.Load() != 1 || posts.Load() != 2 {
		t.Fatal("unsupported transport was repeatedly probed", sockets.Load(), posts.Load())
	}
}

func TestResponsesSocketInterruptedDraftResetsAndCancellationStopsRead(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer connection.Close()
				var body map[string]any
				if connection.ReadJSON(&body) != nil {
					return
				}
				if calls.Add(1) == 1 {
					_ = connection.WriteJSON(map[string]any{"type": "response.output_text.delta", "delta": "partial"})
					return
				}
				_ = connection.WriteJSON(map[string]any{"type": "response.output_text.delta", "delta": "done"})
				_ = connection.WriteJSON(fixtureCompleted("retry"))
			}))
			defer server.Close()
			client := New()
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			request := socketFixtureRequest(server.URL)
			text := ""
			request.OnText = func(delta string) { text += delta }
			retries := 0
			if reset {
				request.OnRetry = func(Retry) { retries++; text = "" }
			}
			_, err := client.Complete(context.Background(), request)
			if reset {
				if err != nil || retries != 1 || text != "done" || calls.Load() != 2 {
					t.Fatal(err, retries, text, calls.Load())
				}
			} else if err == nil || calls.Load() != 1 || text != "partial" {
				t.Fatal("published text silently replayed", err, calls.Load(), text)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := New().Complete(ctx, socketFixtureRequest(server.URL))
	if err == nil || time.Since(started) > time.Second {
		t.Fatal("cancellation did not close socket", err)
	}
}

func TestResponsesSocketAccountsAndTurnsDoNotShareState(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("originator") != "zentrale" || r.Header.Get("session-id") != "thread" || r.Header.Get("thread-id") != "thread" {
			t.Error("missing account routing headers")
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(w, r, http.Header{"X-Codex-Turn-State": []string{"opaque"}})
		if err != nil {
			return
		}
		defer connection.Close()
		connections.Add(1)
		var body map[string]any
		if connection.ReadJSON(&body) != nil {
			return
		}
		if body["previous_response_id"] != nil {
			t.Error("state crossed account/turn")
		}
		_ = connection.WriteJSON(fixtureCompleted("account"))
	}))
	defer server.Close()
	client := New()
	client.waitRetry = func(context.Context, time.Duration) error {
		t.Error("account/token changes must reconnect before sending")
		return nil
	}
	request := socketFixtureRequest(server.URL)
	request.Config.AuthMode = "device"
	request.Config.OAuth = &domain.OAuthTokens{AccountID: "one", AccessToken: "secret"}
	ctx := WithResponsesTurn(context.Background())
	defer CloseResponsesTurn(ctx)
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Config.OAuth = &domain.OAuthTokens{AccountID: "two", AccessToken: "other"}
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	request.Config.OAuth = &domain.OAuthTokens{AccountID: "two", AccessToken: "refreshed"}
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}
	next := WithResponsesTurn(context.Background())
	defer CloseResponsesTurn(next)
	if _, err := client.Complete(next, request); err != nil {
		t.Fatal(err)
	}
	if connections.Load() != 4 {
		t.Fatal(connections.Load())
	}
}

func TestResponsesCacheBoundariesAreStableAndUnsupportedParametersFallBack(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(fmt.Sprint(reject), func(t *testing.T) {
			var count atomic.Int32
			var first []any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				number := count.Add(1)
				if reject && number == 1 {
					w.WriteHeader(400)
					_, _ = w.Write([]byte(`{"error":{"code":"unsupported_parameter","param":"input[0].content[0].prompt_cache_breakpoint"}}`))
					return
				}
				if reject {
					if body["prompt_cache_options"] != nil {
						t.Error("rejected cache option repeated")
					}
				} else {
					if asObject(body["prompt_cache_options"])["mode"] != "implicit" {
						t.Error("implicit tail caching was disabled")
					}
					input := asArray(body["input"])
					if number == 1 {
						first = input
					} else if encodeWire(first) != encodeWire(input[:len(first)]) {
						t.Error("appending messages changed old breakpoints")
					}
				}
				_ = json.NewEncoder(w).Encode(fixtureCompleted("cache")["response"])
			}))
			defer server.Close()
			client := New()
			request := socketFixtureRequest(server.URL)
			request.Config.ResponsesTransport = "http"
			request.Config.PromptCacheMode = "explicit"
			request.Messages = append(request.Messages, domain.Message{Role: "context", Content: "Stable runtime baseline"})
			original := encodeWire(request.Messages)
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if encodeWire(request.Messages) != original {
				t.Fatal("cache annotations changed stored messages")
			}
			request.Messages = append(request.Messages, domain.Message{Role: "user", Content: "new input"})
			if _, err := client.Complete(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			expected := int32(2)
			if reject {
				expected = 3
			}
			if count.Load() != expected {
				t.Fatal(count.Load())
			}
		})
	}
}

func TestResponsesCacheAutoIsLimitedToKnownPublicRoutes(t *testing.T) {
	client := New()
	request := socketFixtureRequest("https://api.openai.com/v1")
	request.Model.ID = "gpt-6.1-sol"
	request.Config.PromptCacheMode = "auto"
	if !client.useResponsesBreakpoints(request) {
		t.Fatal("public supported model lost cache annotations")
	}
	request.Config.AuthMode = "device"
	request.Config.BaseURL = "https://chatgpt.com/backend-api/codex"
	if client.useResponsesBreakpoints(request) {
		t.Fatal("private account capabilities were assumed")
	}
	request.Config.PromptCacheMode = "explicit"
	if !client.useResponsesBreakpoints(request) {
		t.Fatal("account opt-in ignored")
	}
	request.Config.PromptCacheMode = "off"
	if client.useResponsesBreakpoints(request) {
		t.Fatal("cache opt-out ignored")
	}
}
