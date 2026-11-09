package agent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestRuntimeChangesPreserveRequestPrefixAndAuditHistory(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("context preparation unexpectedly called a model")
		return providers.Result{}, nil
	}))
	_ = engine.appendMessage(agent.ID, "user", "Keep existing work.", "", "", "")
	configuration := inferenceConfig{Model: domain.LanguageModel{ContextWindow: 128000}}
	_, first, err := engine.prepareContext(context.Background(), agent, configuration, false)
	if err != nil {
		t.Fatal(err)
	}
	system := engine.buildSystemPrompt(agent)
	_, err = engine.updatePlan(agent, map[string]any{"plan": []any{map[string]any{"step": "Verify the change", "status": "in_progress"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := engine.prepareContext(context.Background(), agent, configuration, false)
	if err != nil || engine.buildSystemPrompt(agent) != system || len(second) != len(first)+1 || !reflect.DeepEqual(first, second[:len(first)]) {
		t.Fatal("runtime change rewrote a previous request prefix", err)
	}
	_, again, err := engine.prepareContext(context.Background(), agent, configuration, false)
	if err != nil || !reflect.DeepEqual(second, again) {
		t.Fatal("unchanged state created duplicate snapshots", err)
	}
	history, _ := engine.loadHistory(agent.ID)
	if len(history) != 1 || history[0].Role != "user" {
		t.Fatal("runtime metadata leaked into the visible transcript")
	}
}

func TestRetainedInstructionsKeepQuestionAnswerPairsWhole(t *testing.T) {
	history := []domain.Message{{ID: "original", Role: "user", Content: "Initial request"}}
	pair := "Question: Delete production? Answer: NO. " + strings.Repeat("preserve production; ", 100)
	history = append(history, domain.Message{ID: "answer", Role: "user", Content: pair})
	for index := range 4 {
		history = append(history, domain.Message{ID: fmt.Sprint(index), Role: "user", Content: "New correction"})
	}
	view := retainedInstructions(history, "main", len(history), 30)
	if strings.Contains(JSON(view), "Question:") || !strings.Contains(JSON(view), "outside this context budget") {
		t.Fatal("oversized original was cut into a partial grant", view)
	}
	view = retainedInstructions(history, "main", len(history), 4000)
	if !strings.Contains(JSON(view), pair) {
		t.Fatal("complete question and answer were not retained")
	}
}

func TestReviewerHistoryUsesAnIndependentBudgetWithoutSummaryCalls(t *testing.T) {
	messages := []domain.Message{}
	for index := range 30 {
		messages = append(messages,
			domain.Message{ID: fmt.Sprint(index), Role: "event", Content: JSON(map[string]any{
				"action": "exec", "mainAgentId": "main", "arguments": map[string]any{"command": "ls"},
				"userInstructions": strings.Repeat("Do not delete files. ", 30),
			})},
			domain.Message{Role: "assistant", Content: `{"allowed":true,"reason":"","blanket":false}`},
		)
	}
	view, err := budgetReviewHistory(messages, 2048, 500)
	if err != nil || len(view) >= len(messages) || view[len(view)-2].ID != messages[len(messages)-2].ID || !strings.Contains(view[0].Content, strings.Repeat("Do not delete files. ", 30)) {
		t.Fatal("current review lost or older reviews unbounded", len(view), err)
	}
	if estimatedMessageTokens(view)+500 > 2048*70/100+100 {
		t.Fatal("review budget exceeded")
	}
	if len(messages) != 60 {
		t.Fatal("audit history changed")
	}
	if _, err := budgetReviewHistory(messages, 512, 500); err == nil {
		t.Fatal("oversized mandatory instructions silently dropped")
	}
}

type compactingProvider struct {
	providerFunction
	compact func(context.Context, providers.Request) (providers.Result, error)
}

func (provider compactingProvider) Compact(ctx context.Context, request providers.Request) (providers.Result, error) {
	return provider.compact(ctx, request)
}

func TestNativeCheckpointReplaySwitchAndUnsupportedFallback(t *testing.T) {
	compactions := 0
	provider := compactingProvider{
		providerFunction: func(context.Context, providers.Request) (providers.Result, error) {
			return providers.Result{Text: "Portable state: preserve the checkpoint and finish testing.", UsageKnown: true}, nil
		},
		compact: func(context.Context, providers.Request) (providers.Result, error) {
			compactions++
			return providers.Result{Wire: `[{"type":"message","role":"user","content":"kept original"},{"type":"compaction","encrypted_content":"opaque-native-state"}]`, UsageKnown: true}, nil
		},
	}
	engine, agent := testEngine(t, provider)
	_ = engine.appendMessage(agent.ID, "user", "Do not delete the checkpoint.", "", "", "")
	_ = engine.appendMessage(agent.ID, "assistant", strings.Repeat("Old inspection. ", 1200), "", "", "")
	configuration := inferenceConfig{Provider: domain.Provider{ID: "one", Kind: "openai"}, Model: domain.LanguageModel{ID: "test", ContextWindow: 12000}}
	compacted, messages, err := engine.prepareContext(context.Background(), agent, configuration, true)
	if err != nil || compactions != 1 || compacted.ContextStart != 2 || messages[0].Wire == "" {
		t.Fatal(compacted, err)
	}
	wire := messages[0].Wire
	_, replay, err := engine.prepareContext(context.Background(), compacted, configuration, false)
	if err != nil || replay[0].Wire != wire || compactions != 1 {
		t.Fatal("canonical checkpoint was rewritten", err)
	}
	configuration.Provider = domain.Provider{ID: "two", Kind: "anthropic"}
	engine.Provider = provider.providerFunction
	updated, portable, err := engine.prepareContext(context.Background(), compacted, configuration, true)
	if err != nil || updated.Summary == "" || strings.Contains(JSON(portable), "opaque-native-state") || !strings.Contains(JSON(portable), "Do not delete") {
		t.Fatal("cross-provider recovery lost originals or leaked opaque state", updated, err)
	}
	engine.Provider = compactingProvider{providerFunction: provider.providerFunction, compact: func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{}, providers.ErrCompactionUnsupported
	}}
	if _, _, err := engine.prepareContext(context.Background(), updated, configuration, true); err != nil {
		t.Fatal("unsupported compaction blocked portable recovery", err)
	}
}

func TestProviderSwitchPreservesAnExplicitlyClearedContext(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if strings.Contains(JSON(request.Messages), "forgotten original") {
			t.Fatal("cleared history entered the destination compactor")
		}
		return providers.Result{Text: "Continue the new task.", UsageKnown: true}, nil
	}))
	_ = engine.appendMessage(agent.ID, "user", "forgotten original", "", "", "")
	configuration := inferenceConfig{Provider: domain.Provider{ID: "one", Kind: "openai"}, Model: domain.LanguageModel{ID: "model", ContextWindow: 16000}}
	if _, _, err := engine.prepareContext(context.Background(), agent, configuration, false); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.manageContext(context.Background(), agent, "context_clear", nil); err != nil {
		t.Fatal(err)
	}
	_ = engine.appendMessage(agent.ID, "user", "new task", "", "", "")
	_ = engine.Store.Load("agent", agent.ID, &agent)
	configuration.Provider = domain.Provider{ID: "two", Kind: "gemini"}
	_, view, err := engine.prepareContext(context.Background(), agent, configuration, false)
	if err != nil || strings.Contains(JSON(view), "forgotten original") || !strings.Contains(JSON(view), "new task") {
		t.Fatal("cleared instructions reappeared", view, err)
	}
}

func TestRuntimeStateSurvivesAnOmittedHistoryAnchor(t *testing.T) {
	history := []domain.Message{{ID: "one"}, {ID: "omitted"}, {ID: "current"}}
	snapshots := []runtimeContext{{ID: "initial", Content: "initial notes"}, {ID: "latest", AfterID: "omitted", Content: "current notes"}}
	view := runtimeContextView(history, []domain.Message{history[0], history[2]}, 0, snapshots)
	if !strings.Contains(JSON(view), "current notes") {
		t.Fatal("latest state disappeared with its omitted anchor")
	}
}

func TestPrivateInstructionRecoveryDoesNotReadOtherAgents(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("private instruction recovery contacted a model")
		return providers.Result{}, nil
	}))
	for _, id := range []string{agent.ID, "another-agent"} {
		instruction := domain.Message{ID: id, AgentID: id, Role: "user", Content: id + " instruction"}
		if err := engine.Store.Save("instruction", id, agent.ProjectID, instruction); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := engine.ownInstructions(agent)
	if err != nil || len(rows) != 1 || stringArg(rows[0], "agentId") != agent.ID {
		t.Fatal("private tool crossed agent scope", rows, err)
	}
}
