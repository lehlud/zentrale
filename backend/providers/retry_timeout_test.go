package providers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type timeoutBody struct{ prefix io.Reader }

func (body *timeoutBody) Read(value []byte) (int, error) {
	if body.prefix != nil {
		n, err := body.prefix.Read(value)
		if n > 0 {
			return n, nil
		}
		if err != io.EOF {
			return n, err
		}
		body.prefix = nil
	}
	return 0, context.DeadlineExceeded
}
func (*timeoutBody) Close() error { return nil }

func TestProviderRetriesAttemptTimeoutsButNotCallerCancellation(t *testing.T) {
	for _, phase := range []string{"transport", "body", "stream"} {
		t.Run(phase, func(t *testing.T) {
			client, attempts := New(), 0
			var waits []time.Duration
			client.waitRetry = func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil }
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				attempts++
				_ = request.Body.Close()
				if attempts == 3 {
					return fixtureResponse(200, `{"ok":true}`), nil
				}
				if phase == "transport" {
					return nil, context.DeadlineExceeded
				}
				response := fixtureResponse(200, "")
				response.Body = &timeoutBody{}
				if phase == "stream" {
					response.Header.Set("Content-Type", "text/event-stream")
				}
				return response, nil
			})
			result, err := client.post(context.Background(), domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
			if err != nil || result["ok"] != true || attempts != 3 || len(waits) != 2 || waits[1] <= waits[0] {
				t.Fatal(result, err, attempts, waits)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	client := New()
	client.waitRetry = func(context.Context, time.Duration) error { t.Fatal("retry after cancellation"); return nil }
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		_ = request.Body.Close()
		cancel()
		return nil, context.DeadlineExceeded
	})
	_, err := client.post(ctx, domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestProviderDoesNotDuplicatePublishedTextOnReadTimeout(t *testing.T) {
	client, attempts := New(), 0
	client.waitRetry = func(context.Context, time.Duration) error { t.Fatal("published text replayed"); return nil }
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		attempts++
		_ = request.Body.Close()
		response := fixtureResponse(200, "")
		response.Header.Set("Content-Type", "text/event-stream")
		response.Body = &timeoutBody{prefix: strings.NewReader("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")}
		return response, nil
	})
	var text string
	_, err := client.post(context.Background(), domain.Provider{Kind: "openai", BaseURL: "https://provider.example"}, "/inference", map[string]any{}, streamRequest{onText: func(value string) { text += value }})
	if err == nil || attempts != 1 || text != "hello" {
		t.Fatal(err, attempts, text)
	}
}

func TestProviderReadTimeoutCanResetTransientDraftBeforeRetry(t *testing.T) {
	client, attempts, resets := New(), 0, 0
	var text string
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		attempts++
		_ = request.Body.Close()
		if attempts == 2 {
			if text != "" || resets != 1 {
				t.Fatal("draft not reset", text, resets)
			}
			return fixtureResponse(200, `{"ok":true}`), nil
		}
		response := fixtureResponse(200, "")
		response.Header.Set("Content-Type", "text/event-stream")
		response.Body = &timeoutBody{prefix: strings.NewReader("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"discard me\"}\n\n")}
		return response, nil
	})
	result, err := client.post(context.Background(), domain.Provider{Kind: "openai", BaseURL: "https://provider.example"}, "/inference", map[string]any{}, streamRequest{
		onText:  func(value string) { text += value },
		onRetry: func(Retry) { text = ""; resets++ },
	})
	if err != nil || result["ok"] != true || attempts != 2 || resets != 1 || text != "" {
		t.Fatal(result, err, attempts, resets, text)
	}
}
