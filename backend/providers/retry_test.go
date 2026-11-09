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

func TestProviderRetriesTemporaryRejectionsWithIdenticalRequest(t *testing.T) {
	for _, kind := range []string{"openai", "anthropic", "gemini", "mistral", "openrouter", "opencode", "litellm"} {
		t.Run(kind, func(t *testing.T) {
			client := New()
			attempts, waits := 0, 0
			client.waitRetry = func(context.Context, time.Duration) error { waits++; return nil }
			var original string
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				attempts++
				body, _ := io.ReadAll(request.Body)
				_ = request.Body.Close()
				if attempts == 1 {
					original = string(body)
				}
				if string(body) != original || !strings.Contains(string(body), "stable-cache-key") || request.Header.Get("Content-Type") != "application/json" {
					t.Fatal("request changed across retry")
				}
				credential := request.Header.Get("Authorization") + request.Header.Get("x-api-key") + request.Header.Get("x-goog-api-key")
				if !strings.Contains(credential, "test-credential") {
					t.Fatal("authentication lost")
				}
				if attempts < 3 {
					return fixtureResponse(503, `{"error":{"code":"overloaded"}}`), nil
				}
				return fixtureResponse(200, `{"ok":true}`), nil
			})
			result, err := client.post(context.Background(), domain.Provider{Kind: kind, BaseURL: "https://provider.example/v1", Credential: "test-credential"}, "/inference", map[string]any{"prompt_cache_key": "stable-cache-key"})
			if err != nil || result["ok"] != true || attempts != 3 || waits != 2 {
				t.Fatal(result, err, attempts, waits)
			}
		})
	}
}

func TestProviderDoesNotReplayAmbiguousOrPermanentFailures(t *testing.T) {
	for _, fixture := range []struct {
		name           string
		status         int
		body, advice   string
		transportError bool
	}{
		{"invalid credentials", 401, `{}`, "", false},
		{"invalid model", 400, `{}`, "", false},
		{"billing quota", 429, `{"error":{"code":"insufficient_quota"}}`, "", false},
		{"anthropic billing", 429, `{"error":{"type":"credit_balance_too_low"}}`, "", false},
		{"long server backoff", 429, `{}`, "3600", false},
		{"malformed response", 200, `invalid`, "", false},
		{"incomplete SSE", 200, "event: response.created\ndata: {}\n\n", "", false},
		{"transport disconnect", 0, "", "", true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client, attempts := New(), 0
			client.waitRetry = func(context.Context, time.Duration) error { t.Fatal("unexpected retry"); return nil }
			client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
				attempts++
				_ = request.Body.Close()
				if fixture.transportError {
					return nil, io.ErrUnexpectedEOF
				}
				response := fixtureResponse(fixture.status, fixture.body)
				response.Header.Set("Retry-After", fixture.advice)
				return response, nil
			})
			_, err := client.post(context.Background(), domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
			if err == nil || attempts != 1 {
				t.Fatal(err, attempts)
			}
		})
	}
}

func TestProviderRetryLimitAdviceAndCancellation(t *testing.T) {
	client, attempts, waits := New(), 0, 0
	client.HTTP.Transport = fixtureTransport(func(request *http.Request) (*http.Response, error) {
		attempts++
		_ = request.Body.Close()
		response := fixtureResponse(429, `{}`)
		response.Header.Set("Retry-After", "2")
		return response, nil
	})
	client.waitRetry = func(_ context.Context, delay time.Duration) error {
		waits++
		if delay < time.Second || delay > 2*time.Second {
			t.Fatal("Retry-After ignored", delay)
		}
		return nil
	}
	_, err := client.post(context.Background(), domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
	if err == nil || attempts != 3 || waits != 2 {
		t.Fatal(err, attempts, waits)
	}
	ctx, cancel := context.WithCancel(context.Background())
	attempts = 0
	client.waitRetry = func(ctx context.Context, delay time.Duration) error {
		cancel()
		return waitForProviderRetry(ctx, delay)
	}
	_, err = client.post(ctx, domain.Provider{BaseURL: "https://provider.example"}, "/inference", map[string]any{})
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatal(err, attempts)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if delay, ok := providerRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); !ok || delay != 5*time.Second {
		t.Fatal(delay, ok)
	}
}
