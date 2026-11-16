package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type turnTestTransport func(*http.Request) (*http.Response, error)

func (transport turnTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestWorkerRoutingContinuesWithinATurnAndResetsForNewHumanInput(t *testing.T) {
	for _, steerDuringRequest := range []bool{false, true} {
		t.Run(fmt.Sprint("steering-", steerDuringRequest), func(t *testing.T) {
			testWorkerRouting(t, steerDuringRequest)
		})
	}
}

func testWorkerRouting(t *testing.T, steerDuringRequest bool) {
	t.Helper()
	client := providers.New()
	var mutex sync.Mutex
	seen := []string{}
	client.HTTP.Transport = turnTestTransport(func(request *http.Request) (*http.Response, error) {
		mutex.Lock()
		defer mutex.Unlock()

		seen = append(seen, request.Header.Get("x-codex-turn-state"))
		phase := "final_answer"
		if len(seen)%2 == 1 {
			phase = "commentary"
		}
		body := JSON(map[string]any{
			"status": "completed", "usage": map[string]any{"input_tokens": 100, "output_tokens": 10},
			"output": []any{map[string]any{
				"type": "message", "role": "assistant", "phase": phase,
				"content": []any{map[string]any{"type": "output_text", "text": "Progress"}},
			}},
		})
		return &http.Response{
			StatusCode: 200, Header: http.Header{
				"Content-Type": []string{"application/json"}, "X-Codex-Turn-State": []string{fmt.Sprint("route-", len(seen))},
			}, Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	var engine *Engine
	instance, record := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		request.Config.Kind, request.Config.AuthMode = "openai", "device"
		request.Config.BaseURL = "https://chatgpt.com/backend-api/codex"
		request.Config.OAuth = &domain.OAuthTokens{AccessToken: "fixture", AccountID: "fixture-account"}
		result, err := client.Complete(ctx, request)
		mutex.Lock()
		steer := steerDuringRequest && len(seen) == 1
		mutex.Unlock()
		if steer {
			if inputErr := engine.Prompt(request.CacheKey, "Continue the same task with this new user correction."); inputErr != nil {
				t.Error(inputErr)
			}
		}
		return result, err
	}))
	engine = instance

	prompts := []string{"First task", "Second task"}
	want := ",route-1,,route-3"
	if steerDuringRequest {
		prompts, want = prompts[:1], ","
	}
	for _, prompt := range prompts {
		if err := engine.Prompt(record.ID, prompt); err != nil {
			t.Fatal(err)
		}
		if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
			t.Fatal("worker did not finish the fixture turn", final)
		}
	}

	mutex.Lock()
	defer mutex.Unlock()
	if strings.Join(seen, ",") != want {
		t.Fatal("worker lifecycle reused routing outside its human turn", seen)
	}
}

func TestIndependentApprovalsHaveSeparateRoutingAndShareItWithTheirOwnInspections(t *testing.T) {
	client := providers.New()
	seen := []string{}
	client.HTTP.Transport = turnTestTransport(func(request *http.Request) (*http.Response, error) {
		seen = append(seen, request.Header.Get("x-codex-turn-state"))
		output := []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": `{"allowed":false,"reason":"fixture denial","blanket":false}`}},
		}}
		if len(seen)%2 == 1 {
			output = []any{map[string]any{
				"type": "function_call", "call_id": fmt.Sprint("inspect-", len(seen)), "name": "inspect",
				"arguments": `{"action":"note_list","arguments":"{}"}`,
			}}
		}

		body := JSON(map[string]any{"status": "completed", "output": output, "usage": map[string]any{"input_tokens": 100, "output_tokens": 10}})
		return &http.Response{
			StatusCode: 200, Header: http.Header{
				"Content-Type": []string{"application/json"}, "X-Codex-Turn-State": []string{fmt.Sprint("route-", len(seen))},
			}, Body: io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	engine, record := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		request.Config.Kind, request.Config.AuthMode = "openai", "device"
		request.Config.BaseURL = "https://chatgpt.com/backend-api/codex"
		request.Config.OAuth = &domain.OAuthTokens{AccountID: "fixture-account", AccessToken: "fixture"}
		return client.Complete(ctx, request)
	}))
	ctx := providers.WithResponsesTurn(context.Background())
	for range 2 {
		_, err := engine.approve(ctx, record, interaction{Action: "exec", Arguments: map[string]any{"command": "true", "timeout_ms": 1000}})
		if err == nil || !strings.Contains(err.Error(), "fixture denial") {
			t.Fatal("routing reuse changed the independent verdict", err)
		}
	}
	if strings.Join(seen, ",") != ",route-1,,route-3" {
		t.Fatal("routing escaped an independent review or was lost between its inspections", seen)
	}
}
