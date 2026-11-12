package agent

import (
	"context"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"testing"
)

func TestSelectableCodexReviewAndOtherApproverModelsKeepIndependentDecisionContract(t *testing.T) {
	for _, modelID := range []string{"codex-auto-review", "test"} {
		t.Run(modelID, func(t *testing.T) {
			calls := 0
			engine, existing := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				calls++
				if request.Model.ID != modelID {
					t.Fatalf("wrong reviewer: %s", request.Model.ID)
				}
				if modelID == "codex-auto-review" {
					if request.OutputSchema == nil || request.ReasoningLevel != "medium" {
						t.Fatal("review schema or user-default reasoning missing")
					}
				} else if request.OutputSchema != nil {
					t.Fatal("generic reviewer was forced into Codex mode")
				}
				return providers.Result{Text: `{"allowed":false,"reason":"Outside original instructions","blanket":false}`}, nil
			}))
			if modelID == "codex-auto-review" {
				model := domain.LanguageModel{ID: modelID, ProviderID: "approver-provider", Name: "Codex auto review", ContextWindow: 128000, ReasoningLevels: []string{"low", "medium", "high"}}
				if err := engine.Store.Save("model", modelResourceID(model.ProviderID, model.ID), model.ProviderID, model); err != nil {
					t.Fatal(err)
				}
			}
			defaults := domain.ModelDefaults{Main: domain.ModelChoice{ProviderID: "main-provider", ModelID: "test"}, Approver: domain.ModelChoice{ProviderID: "approver-provider", ModelID: modelID}}
			if err := engine.SaveModelDefaults(defaults); err != nil {
				t.Fatal(err)
			}
			stored, err := engine.ModelDefaults()
			if err != nil || stored.Approver.ModelID != modelID {
				t.Fatalf("reviewer choice not persisted: %+v %v", stored, err)
			}
			record, err := engine.CreateWithModels(existing.ProjectID, "Selected reviewer", domain.ModelChoice{}, domain.ModelChoice{}, "")
			if err != nil {
				t.Fatal(err)
			}
			allowed, err := engine.approve(context.Background(), record, interaction{Action: "write_file", Arguments: map[string]any{"path": "review-only.txt", "content": "not executed"}})
			if allowed || err == nil || calls != 1 {
				t.Fatalf("denial contract lost: allowed=%v err=%v calls=%d", allowed, err, calls)
			}
			if parseApprovalDecision(`{"outcome":"allow","risk_level":"low"}`).Allowed {
				t.Fatal("foreign risk assessment granted permission")
			}
		})
	}
}

func TestModelReasoningPersistsAndPropagates(t *testing.T) {
	engine, existing := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) { return providers.Result{}, nil }))
	for _, id := range []string{"main-provider", "approver-provider"} {
		model := domain.LanguageModel{ID: "gpt-5.4", ProviderID: id, Name: "Reasoning", ContextWindow: 128000}
		if err := engine.Store.Save("model", modelResourceID(id, model.ID), id, model); err != nil {
			t.Fatal(err)
		}
	}
	defaults := domain.ModelDefaults{
		Main:     domain.ModelChoice{ProviderID: "main-provider", ModelID: "gpt-5.4"},
		Approver: domain.ModelChoice{ProviderID: "approver-provider", ModelID: "gpt-5.4"},
	}
	if err := engine.SaveModelDefaults(defaults); err != nil {
		t.Fatal(err)
	}
	stored, err := engine.ModelDefaults()
	if err != nil {
		t.Fatal(err)
	}
	if stored.Main.ReasoningLevel != "xhigh" || stored.Approver.ReasoningLevel != "medium" {
		t.Fatalf("defaults: %#v", stored)
	}
	record, err := engine.CreateWithModels(existing.ProjectID, "Reasoning agent", domain.ModelChoice{}, domain.ModelChoice{}, "")
	if err != nil {
		t.Fatal(err)
	}
	var approver domain.Agent
	if err := engine.Store.Load("agent", record.ApproverID, &approver); err != nil {
		t.Fatal(err)
	}
	if record.ReasoningLevel != "xhigh" || approver.ReasoningLevel != "medium" {
		t.Fatalf("persisted agents: %#v / %#v", record, approver)
	}
	for _, agent := range []domain.Agent{record, approver} {
		configuration, err := engine.inferenceConfiguration(context.Background(), agent)
		if err != nil {
			t.Fatal(err)
		}
		if configuration.ReasoningLevel != agent.ReasoningLevel {
			t.Fatalf("inference lost reasoning: %#v", configuration)
		}
	}
	stored.Main.ReasoningLevel = "low"
	if err := engine.SaveModelDefaults(stored); err != nil {
		t.Fatal(err)
	}
	configuration, err := engine.inferenceConfiguration(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	if configuration.ReasoningLevel != "xhigh" {
		t.Fatal("changing defaults changed existing agent")
	}
	stored.Main.ReasoningLevel = "invalid"
	if err := engine.SaveModelDefaults(stored); err == nil {
		t.Fatal("invalid reasoning accepted")
	}
}

func TestExplicitReasoningForUnknownModelPersistsAndReachesInference(t *testing.T) {
	engine, existing := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) { return providers.Result{}, nil }))
	for _, id := range []string{"main-provider", "approver-provider"} {
		model := domain.LanguageModel{ID: "custom-reasoner", ProviderID: id, Name: "Custom", ContextWindow: 128000}
		if err := engine.Store.Save("model", modelResourceID(id, model.ID), id, model); err != nil {
			t.Fatal(err)
		}
	}
	defaults := domain.ModelDefaults{
		Main:     domain.ModelChoice{ProviderID: "main-provider", ModelID: "custom-reasoner", ReasoningLevel: "xhigh"},
		Approver: domain.ModelChoice{ProviderID: "approver-provider", ModelID: "custom-reasoner", ReasoningLevel: "medium"},
	}
	if err := engine.SaveModelDefaults(defaults); err != nil {
		t.Fatal(err)
	}
	stored, err := engine.ModelDefaults()
	if err != nil || stored != defaults {
		t.Fatalf("reasoning overrides lost on reload: %#v, %v", stored, err)
	}
	record, err := engine.CreateWithModels(existing.ProjectID, "Custom reasoning agent", domain.ModelChoice{}, domain.ModelChoice{}, "")
	if err != nil {
		t.Fatal(err)
	}
	var approver domain.Agent
	if err := engine.Store.Load("agent", record.ApproverID, &approver); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		record domain.Agent
		want   string
	}{{record, "xhigh"}, {approver, "medium"}} {
		configuration, err := engine.inferenceConfiguration(context.Background(), item.record)
		if err != nil || configuration.ReasoningLevel != item.want {
			t.Fatalf("inference lost explicit reasoning: %#v, %v", configuration, err)
		}
	}
}
