package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func TestAccountResponsesRoutingSurvivesContinuationsAndResetsAtTurnBoundaries(t *testing.T) {
	client := New()
	request := Request{
		Config: domain.Provider{
			ID: "provider", Kind: "openai", AuthMode: "device", BaseURL: "https://chatgpt.com/backend-api/codex",
			OAuth: &domain.OAuthTokens{AccessToken: "access", AccountID: "account"},
		},
		Model: domain.LanguageModel{ID: "gpt-6.1-sol"}, CacheKey: "main-thread",
	}
	seen := []string{}
	client.HTTP.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Header.Get("x-codex-turn-state"))
		if r.Header.Get("session-id") != request.CacheKey || r.Header.Get("thread-id") != request.CacheKey || r.Header.Get("originator") != "zentrale" {
			t.Error("thread identity lost or client identity changed", r.Header)
		}

		response := fixtureResponse(200, `{"status":"completed","output":[]}`)
		response.Header.Set("x-codex-turn-state", "route-"+string(rune('A'+len(seen)-1)))
		return response, nil
	})

	ctx := WithResponsesTurn(context.Background())
	for range 3 {
		if _, err := client.Complete(ctx, request); err != nil {
			t.Fatal(err)
		}
	}

	ctx = WithResponsesTurn(ctx)
	if _, err := client.Complete(ctx, request); err != nil {
		t.Fatal(err)
	}

	if strings.Join(seen, ",") != ",route-A,route-A," {
		t.Fatal("routing rotated within a turn or leaked into a new turn", seen)
	}
}

func TestResponsesRoutingDoesNotCrossAccountsModelsThreadsOrEndpoints(t *testing.T) {
	client := New()
	configuration := domain.Provider{
		ID: "provider", Kind: "openai", AuthMode: "device", BaseURL: "https://chatgpt.com/backend-api/codex",
		OAuth: &domain.OAuthTokens{AccessToken: "access", AccountID: "one"},
	}
	ctx := WithResponsesTurn(context.Background())
	seen := []string{}
	client.HTTP.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		seen = append(seen, r.Header.Get("x-codex-turn-state"))
		response := fixtureResponse(200, `{"status":"completed","output":[]}`)
		response.Header.Set("x-codex-turn-state", "opaque")
		return response, nil
	})
	request := Request{Config: configuration, Model: domain.LanguageModel{ID: "one"}, CacheKey: "main"}
	for index := range 6 {
		if index == 1 {
			request.Config.OAuth = &domain.OAuthTokens{AccountID: "two", AccessToken: "other"}
		} else if index == 2 {
			request.Model.ID = "two"
		} else if index == 3 {
			request.CacheKey = "reviewer"
		} else if index == 4 {
			request.Config.BaseURL = "https://other.example/v1"
		}

		if _, err := client.Complete(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(seen, ",") != ",,,,,opaque" {
		t.Fatal("opaque routing state crossed its owner", seen)
	}
}

func TestResponsesRoutingIsReplayedOnRetryAfterStreamingReadFailure(t *testing.T) {
	client := New()
	client.waitRetry = func(context.Context, time.Duration) error { return nil }
	attempts := 0
	client.HTTP.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		attempts++
		response := fixtureResponse(200, `{"status":"completed","output":[]}`)
		if attempts == 1 {
			response.Header.Set("x-codex-turn-state", "retry-route")
			response.Header.Set("Content-Type", "text/event-stream")
			response.Body = io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\"}\n\n"))
		} else if r.Header.Get("x-codex-turn-state") != "retry-route" {
			t.Error("retry lost the server routing token")
		}

		return response, nil
	})

	_, err := client.Complete(WithResponsesTurn(context.Background()), Request{
		Config: domain.Provider{Kind: "openai", AuthMode: "device", BaseURL: "https://chatgpt.com/backend-api/codex", OAuth: &domain.OAuthTokens{AccountID: "account"}},
		Model:  domain.LanguageModel{ID: "test"}, CacheKey: "main",
	})
	if err != nil || attempts != 2 {
		t.Fatal("stream retry failed", err, attempts)
	}
}

func TestResponsesRoutingRejectsForeignProtocolsAndInvalidTokens(t *testing.T) {
	for _, test := range []struct{ kind, mode, endpoint, token string }{
		{"openai", "api", "/responses", "route"},
		{"anthropic", "device", "/messages", "route"},
		{"openai", "device", "/responses/compact", "route"},
		{"openai", "device", "/responses", strings.Repeat("x", 8193)},
		{"openai", "device", "/responses", "invalid\nheader"},
	} {
		client := New()
		ctx := WithResponsesTurn(context.Background())
		client.HTTP.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("x-codex-turn-state") != "" {
				t.Error("unsupported routing state was replayed")
			}
			response := fixtureResponse(200, `{"ok":true}`)
			response.Header.Set("x-codex-turn-state", test.token)
			return response, nil
		})
		for range 2 {
			_, err := client.post(ctx, domain.Provider{Kind: test.kind, AuthMode: test.mode, BaseURL: "https://provider.example", OAuth: &domain.OAuthTokens{AccountID: "account"}}, test.endpoint, map[string]any{"model": "model", "prompt_cache_key": "key"})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestLateOrFailedResponsesCannotSeedANewOwnersRoutingState(t *testing.T) {
	ctx := WithResponsesTurn(context.Background())
	configuration := domain.Provider{
		Kind: "openai", AuthMode: "device", BaseURL: "https://chatgpt.com/backend-api/codex",
		OAuth: &domain.OAuthTokens{AccountID: "first"},
	}
	request, _ := http.NewRequestWithContext(ctx, "POST", configuration.BaseURL+"/responses", nil)
	old := bindResponsesRoute(request, configuration, map[string]any{"model": "model", "prompt_cache_key": "main"})
	configuration.OAuth = &domain.OAuthTokens{AccountID: "second"}
	current := bindResponsesRoute(request.Clone(ctx), configuration, map[string]any{"model": "model", "prompt_cache_key": "main"})
	response := fixtureResponse(200, `{}`)
	response.Header.Set("x-codex-turn-state", "old-owner")
	captureResponsesRoute(old, response)
	response.StatusCode = 503
	response.Header.Set("x-codex-turn-state", "failed-route")
	captureResponsesRoute(current, response)
	applyResponsesRoute(current)
	if current.Header.Get("x-codex-turn-state") != "" {
		t.Fatal("late/failed response contaminated the new account's routing")
	}

	response.StatusCode = 200
	response.Header.Set("x-codex-turn-state", "current-route")
	captureResponsesRoute(current, response)
	applyResponsesRoute(current)
	if current.Header.Get("x-codex-turn-state") != "current-route" {
		t.Fatal("new account could not establish its own routing state")
	}

	configuration.OAuth = &domain.OAuthTokens{AccountID: "first"}
	restored := bindResponsesRoute(request.Clone(ctx), configuration, map[string]any{"model": "model", "prompt_cache_key": "main"})
	captureResponsesRoute(old, response)
	applyResponsesRoute(restored)
	if restored.Header.Get("x-codex-turn-state") != "" {
		t.Fatal("late response from an older incarnation of the same owner was replayed")
	}
}

func TestResponsesRouteBindingCannotBeInheritedByAnotherProtocol(t *testing.T) {
	ctx := WithResponsesTurn(context.Background())
	configuration := domain.Provider{Kind: "openai", AuthMode: "device", BaseURL: "https://chatgpt.com/backend-api/codex", OAuth: &domain.OAuthTokens{AccountID: "account"}}
	request, _ := http.NewRequestWithContext(ctx, "POST", configuration.BaseURL+"/responses", nil)
	bound := bindResponsesRoute(request, configuration, map[string]any{"model": "model", "prompt_cache_key": "main"})
	response := fixtureResponse(200, `{}`)
	response.Header.Set("x-codex-turn-state", "private-route")
	captureResponsesRoute(bound, response)

	client := New()
	client.HTTP.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("x-codex-turn-state") != "" {
			t.Error("routing leaked through an inherited HTTP context")
		}
		return fixtureResponse(200, `{"ok":true}`), nil
	})
	for _, body := range []any{map[string]any{"model": "other"}, []any{"non-object request"}} {
		if _, err := client.post(bound.Context(), domain.Provider{Kind: "anthropic", BaseURL: "https://api.anthropic.com/v1"}, "/messages", body); err != nil {
			t.Fatal(err)
		}
	}
}
