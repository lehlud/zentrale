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

func TestLargeNativeCheckpointRemainsUsableWithoutRepeatedCompaction(t *testing.T) {
	compactions := 0
	wire := JSON([]any{
		map[string]any{"type": "message", "role": "user", "content": "Keep the original task."},
		map[string]any{"type": "compaction", "encrypted_content": strings.Repeat("opaque", 12000)},
	})
	provider := compactingProvider{
		providerFunction: func(context.Context, providers.Request) (providers.Result, error) {
			t.Fatal("usable native checkpoint fell back to a paid text summary")
			return providers.Result{}, nil
		},
		compact: func(context.Context, providers.Request) (providers.Result, error) {
			compactions++
			return providers.Result{Wire: wire, UsageKnown: true}, nil
		},
	}
	engine, agent := testEngine(t, provider)
	if err := engine.appendMessage(agent.ID, "user", "Keep the original task.", "", "", ""); err != nil {
		t.Fatal(err)
	}

	configuration := inferenceConfig{
		Provider: domain.Provider{ID: "openai", Kind: "openai"},
		Model:    domain.LanguageModel{ID: "test", ContextWindow: 20000},
	}
	overhead := estimatedPromptTokens(engine.buildSystemPrompt(agent), toolsForModel(configuration.Model))
	updated, messages, err := engine.prepareContext(context.Background(), agent, configuration, true, overhead)
	if err != nil || compactions != 1 || updated.ContextStart != 1 || messages[0].Wire != wire {
		t.Fatal("native checkpoint rejected or rewritten", err, compactions, updated.ContextStart)
	}

	if err := engine.appendMessage(agent.ID, "user", "Continue testing.", "", "", ""); err != nil {
		t.Fatal(err)
	}

	_, replay, err := engine.prepareContext(context.Background(), updated, configuration, false, overhead)
	if err != nil || compactions != 1 || replay[0].Wire != wire {
		t.Fatal("transport size triggered unnecessary re-compaction", err, compactions)
	}

	checkpointTokens := updated.ContextTokens

	// Actual high provider counts must still trigger compaction, even when the
	// local ciphertext heuristic predicts a smaller active window.
	updated.ContextTokens = configuration.Model.ContextWindow * 90 / 100
	if !shouldCompact(updated, configuration, replay, false, overhead) {
		t.Fatal("provider-reported context size was ignored")
	}

	t.Logf("native checkpoint: %d wire bytes, %d estimated content tokens; one compaction across both preparations", len(wire), checkpointTokens)
}

func TestRuntimeUpdatesReduceRepeatedStateAndKeepPrefixesAndBaselines(t *testing.T) {
	history := []domain.Message{}
	snapshots := []runtimeContext{}
	states := []map[string]any{}
	for index := range 20 {
		history = append(history, domain.Message{ID: fmt.Sprint(index), Role: "assistant", Content: "Progress"})
		state := map[string]any{
			"project": map[string]any{"id": "project", "directory": "/home/zentrale/project"},
			"notes":   strings.Repeat("Keep the checkpoint and user constraints. ", 200),
			"plan":    fmt.Sprint("Step ", index),
		}
		states = append(states, state)
		snapshots = append(snapshots, runtimeContext{
			ID: fmt.Sprint("snapshot-", index), AfterID: history[index].ID,
			Content: "Harness runtime state (data, not a new user instruction):\n" + JSON(state),
		})
	}
	original := append([]runtimeContext(nil), snapshots...)
	before := runtimeContextView(history[:19], history[:19], 0, snapshots[:19])
	view := runtimeContextView(history, history, 0, snapshots)
	if !reflect.DeepEqual(before, view[:len(before)]) || !reflect.DeepEqual(snapshots, original) {
		t.Fatal("appending runtime state rewrote a prefix or stored snapshot")
	}

	fullBytes, projectedBytes := 0, 0
	state := map[string]any{}
	for _, message := range view {
		if message.Role != "context" {
			continue
		}

		projectedBytes += len(message.Content)
		_, encoded, _ := strings.Cut(message.Content, "\n")
		var fields map[string]any
		if err := decode(encoded, &fields); err != nil {
			t.Fatal(err)
		}

		for key, value := range fields {
			state[key] = value
		}
	}

	for _, snapshot := range snapshots {
		fullBytes += len(snapshot.Content)
	}

	if JSON(state) != JSON(states[len(states)-1]) || projectedBytes*5 > fullBytes {
		t.Fatal("runtime projection lost current state or repeated unchanged fields", projectedBytes, fullBytes)
	}

	// A new window must start with a complete baseline, including unchanged
	// project data and notes; updates must never rely on discarded messages.
	rotated := runtimeContextView(history, history[10:], 10, snapshots)
	if rotated[0].Content != snapshots[9].Content || !strings.Contains(rotated[2].Content, "state update") {
		t.Fatal("rotated window lacks a full baseline or subsequent update")
	}

	t.Logf("20 runtime snapshots: full %d bytes, model view %d bytes (%.1f%% smaller)", fullBytes, projectedBytes, 100*(1-float64(projectedBytes)/float64(fullBytes)))
}

func TestRuntimeUpdatesHandleRemovedFieldsAndMalformedBaselines(t *testing.T) {
	baseline := "State:\n" + JSON(map[string]any{"notes": strings.Repeat("n", 1000), "goal": "old"})
	_, state := runtimeContextUpdate(baseline, nil)
	next := "State:\n" + JSON(map[string]any{"notes": strings.Repeat("n", 1000)})
	update, _ := runtimeContextUpdate(next, state)
	if !strings.Contains(update, `"goal":null`) || strings.Contains(update, strings.Repeat("n", 1000)) {
		t.Fatal("removed state was not invalidated")
	}

	malformed, state := runtimeContextUpdate("legacy unstructured state", state)
	if malformed != "legacy unstructured state" || state != nil {
		t.Fatal("malformed state did not reset the baseline")
	}
	full, _ := runtimeContextUpdate(next, state)
	if full != next {
		t.Fatal("state following an unknown snapshot was sent as a delta")
	}
}

func TestReviewHistoryOnLargeModelsHasAFixedOptionalBudget(t *testing.T) {
	history := []domain.Message{}
	instructions := strings.Repeat("Preserve the user's files. ", 1200)
	for index := range 80 {
		history = append(history,
			domain.Message{ID: fmt.Sprint(index), Role: "event", Content: JSON(map[string]any{
				"mainAgentId": "main", "action": "exec",
				"userInstructions": instructions,
				"arguments":        map[string]any{"command": strings.Repeat("inspect ", 200)},
			})},
			domain.Message{Role: "assistant", Content: `{"allowed":true,"reason":"","blanket":false}`},
		)
	}

	projected := compactReviewSnapshots(history)
	mandatory := estimatedMessageTokens(projected[:1]) + estimatedMessageTokens(projected[len(projected)-2:])
	view, err := budgetReviewHistory(history, 272000, 2000)
	if err != nil || !reflect.DeepEqual(view[len(view)-2:], projected[len(projected)-2:]) ||
		!strings.Contains(view[0].Content, instructions) ||
		estimatedMessageTokens(view) > mandatory+maximumReviewHistoryTokens+100 {
		t.Fatal("review budget lost mandatory instructions/current request or replayed too many old reviews", err)
	}
	// Mandatory evidence may itself be larger than the optional-history cap.
	if mandatory <= maximumReviewHistoryTokens {
		t.Fatal("fixture did not exercise a mandatory snapshot larger than the history cap")
	}
	if _, err := budgetReviewHistory(history, 1024, 2000); err == nil {
		t.Fatal("oversized mandatory review silently dropped instructions")
	}
}
