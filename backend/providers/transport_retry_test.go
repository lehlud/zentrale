package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestProviderRecoversFromTruncatedStreamsAcrossProtocols(t *testing.T) {
	for _, fixture := range streamingFixtures() {
		t.Run(fixture.kind, func(t *testing.T) {
			var attempts atomic.Int32
			var mutex sync.Mutex
			var payload string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				attempt := attempts.Add(1)
				mutex.Lock()
				if attempt == 1 {
					payload = string(body)
				} else if string(body) != payload {
					t.Error("retry changed context, tools or cache identity")
				}
				mutex.Unlock()
				writer.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(writer, fixture.prefix)
				if attempt > 1 {
					fmt.Fprint(writer, fixture.suffix)
				}
			}))
			defer server.Close()
			client := New()
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			var visible strings.Builder
			retries := 0
			result, err := client.Complete(context.Background(), Request{
				Config: domain.Provider{Kind: fixture.kind, BaseURL: server.URL},
				Model:  domain.LanguageModel{ID: "test"}, CacheKey: "stable-cache-key",
				OnText: func(text string) { visible.WriteString(text) },
				OnRetry: func(retry Retry) {
					retries++
					if retry.Attempt != 2 || retry.MaxAttempts != providerAttempts {
						t.Error(retry)
					}
					visible.Reset()
				},
			})
			if err != nil || result.Text != "Hello" || visible.String() != "Hello" || len(result.Calls) != 1 || retries != 1 || attempts.Load() != 2 {
				t.Fatal(result, visible.String(), err, retries, attempts.Load())
			}
		})
	}
}

func TestHTTPClientTimeoutWhileReadingBodyGetsAFreshAttempt(t *testing.T) {
	for _, headersOnly := range []bool{false, true} {
		t.Run(fmt.Sprint(headersOnly), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				if attempts.Add(1) == 1 {
					if !headersOnly {
						writer.Header().Set("Content-Type", "application/json")
						fmt.Fprint(writer, `{"ok":`)
						writer.(http.Flusher).Flush()
					}
					<-request.Context().Done()
					return
				}
				fmt.Fprint(writer, `{"ok":true}`)
			}))
			defer server.Close()
			client := New()
			// Reproduce the old production limit in milliseconds, including the
			// exact "Client.Timeout ... while reading body" error path.
			client.HTTP.Timeout = 50 * time.Millisecond
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			result, err := client.post(context.Background(), domain.Provider{BaseURL: server.URL}, "/inference", map[string]any{})
			if err != nil || result["ok"] != true || attempts.Load() != 2 {
				t.Fatal(result, err, attempts.Load())
			}
		})
	}
}

func TestHealthyStreamCanOutliveTheIdleBudget(t *testing.T) {
	client := New()
	if client.HTTP.Timeout != 0 {
		t.Fatal("a fixed HTTP deadline still interrupts healthy generations")
	}
	client.idleTimeout = 150 * time.Millisecond
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		for range 12 {
			fmt.Fprint(writer, ": keepalive\n\n")
			writer.(http.Flusher).Flush()
			select {
			case <-request.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		fmt.Fprint(writer, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.Complete(ctx, Request{Config: domain.Provider{Kind: "openai", BaseURL: server.URL}, Model: domain.LanguageModel{ID: "test"}, OnText: func(string) {}})
	if err != nil || attempts.Load() != 1 {
		t.Fatal(err, attempts.Load())
	}
}

func TestIdleStreamIsClosedAndRetriedWithAFreshContext(t *testing.T) {
	var attempts atomic.Int32
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		if attempts.Add(1) == 1 {
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, ": keepalive\n\n")
			writer.(http.Flusher).Flush()
			<-request.Context().Done()
			close(stopped)
			return
		}
		fmt.Fprint(writer, `{"ok":true}`)
	}))
	defer server.Close()
	client := New()
	client.idleTimeout = 50 * time.Millisecond
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	result, err := client.post(context.Background(), domain.Provider{BaseURL: server.URL}, "/inference", map[string]any{})
	if err != nil || result["ok"] != true || attempts.Load() != 2 {
		t.Fatal(result, err, attempts.Load())
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("the stalled request was left running")
	}
}

func TestTransportDisconnectsHaveBoundedRetries(t *testing.T) {
	client, attempts := New(), 0
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		attempts++
		request.Body.Close()
		return nil, io.ErrUnexpectedEOF
	})
	_, err := client.post(context.Background(), domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
	if !errors.Is(err, io.ErrUnexpectedEOF) || attempts != providerAttempts {
		t.Fatal(err, attempts)
	}
}

func TestCallerDeadlineNeverStartsAnotherModelRequest(t *testing.T) {
	client, attempts := New(), 0
	client.waitRetry = func(context.Context, time.Duration) error {
		t.Fatal("a cancelled caller must not back off or retry")
		return nil
	}
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		attempts++
		request.Body.Close()
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := client.post(ctx, domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
	if !errors.Is(err, context.DeadlineExceeded) || attempts != 1 {
		t.Fatal(err, attempts)
	}
}

func TestProviderRetriesOnlyRecoverableResponseFailures(t *testing.T) {
	for _, fixture := range []struct {
		name, kind, body string
		retry            bool
	}{
		{"truncated JSON", "openai", `{"output":[`, true},
		{"empty body", "openai", "", true},
		{"truncated SSE event", "openai", "data: {\"type\":\"response.completed\",\"response\":", true},
		{"failed JSON", "openai", `{"status":"failed","error":{"code":"server_error"}}`, true},
		{"failed SSE", "openai", "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_error\"}}}\n\n", true},
		{"Anthropic overloaded", "anthropic", "event: error\ndata: {\"error\":{\"type\":\"overloaded_error\"}}\n\n", true},
		{"billing in JSON", "openai", `{"error":{"code":"insufficient_quota"}}`, false},
		{"Anthropic billing", "anthropic", "event: error\ndata: {\"error\":{\"type\":\"credit_balance_too_low\"}}\n\n", false},
		{"chat token limit", "openrouter", "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n", false},
		{"Gemini token limit", "gemini", "data: {\"candidates\":[{\"index\":0,\"finishReason\":\"MAX_TOKENS\"}]}\n\n", false},
		{"malformed framed SSE", "openai", "data: {invalid}\n\n", false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client, attempts, retries := New(), 0, 0
			client.waitRetry = func(context.Context, time.Duration) error { return nil }
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				attempts++
				request.Body.Close()
				if attempts == 1 {
					return fixtureResponse(200, fixture.body), nil
				}
				return fixtureResponse(200, `{"ok":true}`), nil
			})
			result, err := client.post(context.Background(), domain.Provider{Kind: fixture.kind, BaseURL: "https://provider.example"}, "/inference", map[string]any{}, streamRequest{onRetry: func(Retry) { retries++ }})
			if fixture.retry {
				if err != nil || result["ok"] != true || attempts != 2 || retries != 1 {
					t.Fatal(result, err, attempts, retries)
				}
			} else if err == nil || result != nil || attempts != 1 || retries != 0 {
				t.Fatal("permanent error was retried or exposed an unfinished response", result, err, attempts, retries)
			}
		})
	}
}
