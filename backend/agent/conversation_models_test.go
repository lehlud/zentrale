package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestConversationModelsChangeDuringAnActiveTurn(t *testing.T) {
	started, continueTurn := make(chan struct{}), make(chan struct{})
	var mainCalls atomic.Int32
	requests := make(chan domain.ModelChoice, 8)
	engine, record := restartTestEngine(t, nil)
	engine.Provider = providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		if strings.HasPrefix(request.System, compactionInstructions) {
			return providers.Result{Text: "Retained context", UsageKnown: true}, nil
		}
		select {
		case requests <- domain.ModelChoice{ProviderID: request.Config.ID, ModelID: request.Model.ID, ReasoningLevel: request.ReasoningLevel}:
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		}
		if request.System == goalReviewInstructions {
			return goalVerdict("complete", "Finished", "Test output", "", ""), nil
		}
		if mainCalls.Add(1) == 1 {
			close(started)
			select {
			case <-ctx.Done():
				return providers.Result{}, ctx.Err()
			case <-continueTurn:
				return providers.Result{Text: "Working", Phase: "commentary", UsageKnown: true}, nil
			}
		}
		return providers.Result{Text: "Done", UsageKnown: true}, nil
	})
	child, err := engine.CreateWithModels(record.ProjectID, "Child", domain.ModelChoice{}, domain.ModelChoice{}, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := engine.ModelDefaults()
	if err != nil {
		t.Fatal(err)
	}
	main := domain.ModelChoice{ProviderID: "approver-provider", ModelID: "updated-main", ReasoningLevel: "high"}
	approver := domain.ModelChoice{ProviderID: "main-provider", ModelID: "updated-reviewer", ReasoningLevel: "low"}
	for _, choice := range []domain.ModelChoice{main, approver} {
		model := domain.LanguageModel{ID: choice.ModelID, ProviderID: choice.ProviderID, ContextWindow: 128000, ReasoningLevels: []string{"low", "medium", "high", "xhigh"}}
		if err := engine.Store.Save("model", modelResourceID(choice.ProviderID, choice.ModelID), choice.ProviderID, model); err != nil {
			t.Fatal(err)
		}
	}

	if err := engine.Prompt(record.ID, "Finish the current task"); err != nil {
		t.Fatal(err)
	}
	waitForRestartProvider(t, engine, record.ID, started)
	if err := engine.SetConversationModel(child.ID, "main", main); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetConversationModel(record.ID, "approver", approver); err != nil {
		t.Fatal(err)
	}
	// A compaction finishing with an older snapshot only saves its context.
	record.Summary = "Saved context"
	if err := engine.saveAgentContext(record); err != nil {
		t.Fatal(err)
	}
	if choices, err := engine.ConversationModels(child.ID); err != nil || choices.Main != main || choices.Approver != approver {
		t.Fatal("expected the current main and shared approver settings", choices, err)
	}
	close(continueTurn)
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" || agentModelChoice(final) != main {
		t.Fatal(final)
	}
	if len(requests) != 3 {
		t.Fatalf("expected the original request, next main request and goal review, got %d", len(requests))
	}
	for _, want := range []domain.ModelChoice{defaults.Main, main, approver} {
		if got := <-requests; got != want {
			t.Fatalf("request used %+v, want %+v", got, want)
		}
	}
	if after, err := engine.ModelDefaults(); err != nil || after != defaults {
		t.Fatal("per-conversation update changed global defaults", after, err)
	}
	var savedChild domain.Agent
	if err := engine.Store.Load("agent", child.ID, &savedChild); err != nil || agentModelChoice(savedChild) != agentModelChoice(child) {
		t.Fatal("updating the main agent changed the subagent's model", savedChild, err)
	}
}
