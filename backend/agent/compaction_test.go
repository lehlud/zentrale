package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestCompactionRepairsInvalidSummariesFromTheOriginalFragment(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		result providers.Result
	}{
		{"empty", providers.Result{}},
		{"whitespace", providers.Result{Text: " \n\t"}},
		{"oversized UTF-8", providers.Result{Text: strings.Repeat("ä", 1100)}},
		{"unexpected tool call", providers.Result{Text: "A plausible summary", Calls: []providers.Call{{Name: "exec"}}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			requests := []providers.Request{}
			engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				requests = append(requests, request)
				if len(requests) == 1 {
					return fixture.result, nil
				}
				return providers.Result{Text: "Keep the existing checkpoint; validation is still pending.", UsageKnown: true}, nil
			}))
			record.Summary = "Earlier work: checkpoint 42 exists."
			configuration := inferenceConfig{
				Provider:       domain.Provider{Kind: "openai"},
				Model:          domain.LanguageModel{ID: "gpt-5.4", ContextWindow: 4096},
				ReasoningLevel: "xhigh",
			}
			history := []domain.Message{{Role: "user", Content: "Do not delete the existing checkpoint. Verify the result."}}
			updated, err := engine.compactContext(context.Background(), record, configuration, history, 1)
			if err != nil || len(requests) != 2 || !strings.Contains(updated.Summary, "validation is still pending") || updated.ContextStart != 1 {
				t.Fatal(updated, err, len(requests))
			}

			original := requests[0].Messages[0].Content
			if !strings.Contains(original, record.Summary) || !strings.Contains(original, history[0].Content) ||
				!strings.HasPrefix(requests[1].Messages[0].Content, original) ||
				!strings.Contains(requests[1].Messages[0].Content, "Harness validation:") ||
				requests[0].CacheKey != requests[1].CacheKey || requests[1].ReasoningLevel != "medium" {
				t.Fatal("repair lost original evidence or repeated the rejected request unchanged", requests)
			}
		})
	}
}

func TestCompactionRepairsEachFragmentBeforeAdvancing(t *testing.T) {
	requests, fragments, stage := 0, 0, 0
	previous, rejected := "", ""
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		requests++
		content := request.Messages[0].Content
		if !strings.HasPrefix(content, "Cumulative summary so far:\n"+previous+"\nNext sequential") {
			t.Fatal("repair used a rejected summary or lost accumulated context", content)
		}
		if estimatedPromptTokens(request.System, nil)+estimatedMessageTokens(request.Messages)+128 > request.Model.ContextWindow {
			t.Fatal("repair exceeded the input/output budget")
		}
		if request.ReasoningLevel != "high" {
			t.Fatal("explicit reasoning for an unknown model was changed")
		}

		switch stage {
		case 0:
			rejected = content
			stage = 1
			return providers.Result{Text: strings.Repeat("ä", 600), UsageKnown: true}, nil
		case 1:
			if !strings.HasPrefix(content, rejected) || !strings.Contains(content, "1200 UTF-8 bytes (limit 512)") {
				t.Fatal("oversize feedback or original fragment missing", content)
			}
			stage = 2
			return providers.Result{Text: " ", UsageKnown: true}, nil
		default:
			if !strings.HasPrefix(content, rejected) || !strings.Contains(content, "empty summary") {
				t.Fatal("empty-summary repair skipped the original fragment", content)
			}
			stage = 0
			fragments++
			previous = "Preserve original constraints. Continue from checkpoint " + JSON(fragments)
			return providers.Result{Text: previous, UsageKnown: true}, nil
		}
	}))
	configuration := inferenceConfig{
		Model:          domain.LanguageModel{ID: "custom-reasoner", ContextWindow: 1024},
		ReasoningLevel: "high",
	}
	summary, err := engine.summarizeContext(context.Background(), record, configuration, []domain.Message{{Role: "user", Content: strings.Repeat("ä", 3000)}})
	if err != nil || summary != previous || fragments < 2 || requests != 3*fragments {
		t.Fatal(summary, err, requests, fragments)
	}
}

func TestFailedCompactionPreservesHistoryCheckpointAndTokenEstimate(t *testing.T) {
	for _, completeFirstFragment := range []bool{false, true} {
		t.Run(JSON(completeFirstFragment), func(t *testing.T) {
			requests := 0
			engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				requests++
				if completeFirstFragment && requests == 1 {
					return providers.Result{Text: "An intermediate summary that must not be committed.", UsageKnown: true}, nil
				}
				return providers.Result{
					Text: strings.Repeat("x", 3000), InputTokens: 700, OutputTokens: 800, UsageKnown: true,
				}, nil
			}))
			record.Summary, record.ContextStart, record.ContextTokens = "Keep the original summary.", 1, 12345
			if err := engine.saveAgent(record); err != nil {
				t.Fatal(err)
			}
			for _, role := range []string{"user", "assistant"} {
				content := "Original " + role
				if completeFirstFragment {
					content = strings.Repeat(content, 2000)
				}
				if err := engine.appendMessage(record.ID, role, content, "", "", ""); err != nil {
					t.Fatal(err)
				}
			}
			original, _ := engine.loadHistory(record.ID)
			configuration := inferenceConfig{Model: domain.LanguageModel{ContextWindow: 4096}}
			_, err := engine.compactContext(context.Background(), record, configuration, original, len(original))
			expected := summaryAttempts
			if completeFirstFragment {
				expected++
			}
			if err == nil || requests != expected || !strings.Contains(err.Error(), "3000 UTF-8 bytes (limit 2048)") {
				t.Fatal("persistent invalid responses were not bounded or diagnosed", err, requests)
			}

			var stored domain.Agent
			if err := engine.Store.Load("agent", record.ID, &stored); err != nil {
				t.Fatal(err)
			}
			history, _ := engine.loadHistory(record.ID)
			usage, usageErr := engine.listAll("usage", &record.ID)
			if !reflect.DeepEqual(history, original) || stored.Summary != record.Summary ||
				stored.ContextStart != record.ContextStart || stored.ContextTokens != record.ContextTokens {
				t.Fatal("failed compaction changed the active checkpoint or history", stored, history)
			}
			if usageErr != nil || len(usage) != expected {
				t.Fatal("rejected summaries were omitted from billing", usage, usageErr)
			}
		})
	}
}

func TestCompactionDoesNotRetryCancellationOrProviderErrors(t *testing.T) {
	for _, cancelAfterResponse := range []bool{false, true} {
		t.Run(JSON(cancelAfterResponse), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("invalid provider credentials")
			requests := 0
			engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
				requests++
				if cancelAfterResponse {
					cancel()
					return providers.Result{Text: "", UsageKnown: true}, nil
				}
				return providers.Result{}, failure
			}))
			if cancelAfterResponse {
				failure = context.Canceled
			}
			_, err := engine.summarizeContext(ctx, record, inferenceConfig{Model: domain.LanguageModel{ContextWindow: 4096}}, []domain.Message{{Role: "user", Content: "Keep working."}})
			if !errors.Is(err, failure) || requests != 1 {
				t.Fatal(err, requests)
			}
		})
	}
}
