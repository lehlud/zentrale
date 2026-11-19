package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
	"github.com/lehlud/zentrale/providers"
)

type stubHostedInference func(context.Context, domain.Provider, string, map[string]any, time.Duration) (json.RawMessage, error)

func (invoke stubHostedInference) Run(ctx context.Context, provider domain.Provider, target string, input map[string]any, timeout time.Duration) (json.RawMessage, error) {
	return invoke(ctx, provider, target, input, timeout)
}

func (stubHostedInference) Close() {}

func TestHostedHumanizerSendsTheEntireApprovedBatchInOneJob(t *testing.T) {
	reviews, jobs := 0, 0
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		reviews++
		if request.Config.ID != "approver-provider" || !strings.Contains(JSON(request.Messages), "second draft") {
			t.Fatal("the Auto-Approver did not receive the entire batch")
		}
		return providers.Result{Text: `{"allowed":true}`}, nil
	}))
	account := domain.Provider{ID: "modal", Name: "Modal", Kind: "modal", BaseURL: "https://zentrale--rewrite.modal.run", Credential: "example-secret"}
	if err := engine.Store.Save("provider", account.ID, "", account); err != nil {
		t.Fatal(err)
	}
	settings := domain.HumanizerSettings{Hosted: domain.HostedHumanizer{ProviderID: account.ID, ModelID: "jialinyyzz/humanizer"}}
	if err := engine.SaveHumanizer(settings); err != nil {
		t.Fatal(err)
	}
	engine.hosted.Close()
	engine.hosted = stubHostedInference(func(_ context.Context, provider domain.Provider, target string, input map[string]any, timeout time.Duration) (json.RawMessage, error) {
		jobs++
		if reviews != 1 || provider.ID != account.ID || target != "" || timeout != hosted.ModalTimeout ||
			input["model"] != settings.Hosted.ModelID || !reflect.DeepEqual(input["texts"], []string{"first draft", "second draft"}) {
			t.Fatalf("unexpected hosted inference batch: %v", input)
		}
		return json.RawMessage(`{"texts":["first edited","second edited"]}`), nil
	})
	call := providers.Call{Name: humanizerAction, Arguments: map[string]any{
		"texts": []any{"first draft", "second draft"}, "network": true,
	}}
	result, err := engine.dispatch(context.Background(), record, call)
	want := map[string][]string{"texts": {"first edited", "second edited"}}
	if err != nil || !reflect.DeepEqual(result, want) || reviews != 1 || jobs != 1 {
		t.Fatalf("result = %v, reviews = %d, jobs = %d, error = %v", result, reviews, jobs, err)
	}
}

func TestHumanizerRequiresFreshApprovalAndSendsOnlyReviewedText(t *testing.T) {
	var engine *Engine
	var phases []string
	decision := "deny"
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if request.Config.ID == "approver-provider" {
			phases = append(phases, "approval")
			if !strings.Contains(JSON(request.Messages), "original text") {
				t.Fatal("approval did not receive the text")
			}
			if decision == "disable" {
				if err := engine.SaveHumanizer(domain.HumanizerSettings{}); err != nil {
					t.Fatal(err)
				}
			}
			if decision == "deny" {
				return providers.Result{Text: `{"allowed":false,"reason":"not requested"}`}, nil
			}
			return providers.Result{Text: `{"allowed":true,"blanket":true}`}, nil
		}
		phases = append(phases, "rewrite")
		if request.Config.ID != "main-provider" || request.Model.ID != "test" ||
			!request.SingleAttempt || request.MaxOutputTokens != maxHumanizerOutputTokens ||
			len(request.Tools) != 0 || len(request.Messages) != 1 || request.Messages[0].Content != "original text" {
			t.Fatalf("unexpected Humanizer request: %+v", request)
		}
		return providers.Result{Text: "rewritten text", UsageKnown: true, InputTokens: 54, OutputTokens: 20}, nil
	}))
	call := providers.Call{Name: humanizerAction, Arguments: map[string]any{
		"text": "original text", "network": true, "model": "unapproved-model", "destination": "https://wrong.example",
	}}
	ctx := context.Background()
	model := domain.LanguageModel{ContextWindow: 4096}
	advertised := func() bool {
		return slices.ContainsFunc(engine.agentToolsForModel(model), func(tool providers.Tool) bool { return tool.Name == humanizerAction })
	}
	if _, err := engine.dispatch(ctx, record, call); err == nil || advertised() || len(phases) != 0 {
		t.Fatalf("disabled Humanizer: phases %v, error %v", phases, err)
	}
	settings := domain.HumanizerSettings{Model: domain.ModelChoice{ProviderID: "main-provider", ModelID: "test"}}
	if err := engine.SaveHumanizer(settings); err != nil || !advertised() {
		t.Fatalf("could not enable Humanizer: %v", err)
	}
	if _, err := engine.dispatch(ctx, record, call); err == nil || !strings.Contains(err.Error(), "not requested") {
		t.Fatalf("expected approval denial, got %v", err)
	}
	decision = "allow"
	for range 2 {
		result, err := engine.dispatch(ctx, record, call)
		if !reflect.DeepEqual(result, map[string]string{"text": "rewritten text"}) || err != nil {
			t.Fatalf("result = %v, error = %v", result, err)
		}
	}
	decision = "disable"
	if _, err := engine.dispatch(ctx, record, call); err == nil || !strings.Contains(err.Error(), "selection changed") {
		t.Fatalf("expected stale approval to fail, got %v", err)
	}
	if !slices.Equal(phases, []string{"approval", "approval", "rewrite", "approval", "rewrite", "approval"}) {
		t.Fatalf("phases = %v", phases)
	}
	usage, err := engine.Store.UsageSummary(record.ID)
	if err != nil || usage.RequestCount != 2 || usage.InputTokens != 108 || usage.OutputTokens != 40 {
		t.Fatalf("usage = %+v, error = %v", usage, err)
	}
	approvals, err := engine.listAll("approval", &record.ID)
	if err != nil || len(approvals) != 4 || strings.Contains(JSON(approvals), "wrong.example") {
		t.Fatalf("reviewed wrong destination or skipped review: %v (%v)", approvals, err)
	}
	for _, approval := range approvals {
		if approval["blanket"] != false {
			t.Fatalf("unexpected blanket approval: %v", approval)
		}
	}
}
