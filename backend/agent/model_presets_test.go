package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestRevokedModelPresetStopsInference(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Error("a revoked choice reached the provider")
		return providers.Result{}, nil
	}))
	choice := agentModelChoice(record)
	if err := engine.SaveModelPresetPolicy(ProjectModels, ModelPresetPolicy{Presets: []domain.ModelChoice{}}); err != nil {
		t.Fatal(err)
	}
	request := providers.Request{
		Config: domain.Provider{ID: choice.ProviderID}, Model: domain.LanguageModel{ID: choice.ModelID},
		ReasoningLevel: choice.ReasoningLevel,
	}
	if _, err := engine.completeModel(context.Background(), record.ID, "agent", request); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("revoked preset reached inference: %v", err)
	}
}
