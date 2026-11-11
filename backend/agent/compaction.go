package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

const summaryAttempts = 3

// Summarize sequential fragments cumulatively. Instructions and structured
// calls can still exceed one summary request even after tool output is bounded.
// Preserve the original persisted history until every fragment has succeeded.
func (engine *Engine) summarizeContext(
	ctx context.Context,
	agentRecord domain.Agent,
	configuration inferenceConfig,
	messages []domain.Message,
) (string, error) {
	remaining := compactionTranscript(agentRecord.Summary, messages)
	summary := ""
	summaryLimit := max(128, min(32000, configuration.Model.ContextWindow/2))
	system := compactionInstructions + fmt.Sprintf(
		"\nUpdate the cumulative summary using this fragment. Aim for at most %d UTF-8 bytes; the hard limit is %d bytes. "+
			"Preserve the goal, current plan, user corrections, denied actions, validated results, "+
			"pending checks and concrete next steps. Do not claim unfinished work is complete. "+
			"Return only the updated summary, without a preamble or tool calls.",
		summaryLimit/2, summaryLimit,
	)

	for remaining != "" {
		// ContextWindow is measured in tokens; fragment lengths are UTF-8 bytes.
		// Reserve space for the cumulative summary, instructions and the response.
		// Leave room for feedback if the model needs to repair its summary.
		inputLimit := max(256, configuration.Model.ContextWindow*3-len(summary)-len(system)-1024)
		fragment, rest := splitTranscript(remaining, inputLimit)

		next, err := engine.summarizeFragment(
			ctx, agentRecord, configuration,
			system, summary, fragment, summaryLimit,
		)
		if err != nil {
			return "", err
		}

		summary = next
		remaining = rest
	}

	return summary, nil
}

// Repair invalid summaries from the same original fragment. Never truncate a
// summary or advance to the next fragment until the replacement is usable.
func (engine *Engine) summarizeFragment(
	ctx context.Context,
	agentRecord domain.Agent,
	configuration inferenceConfig,
	system, previous, fragment string,
	limit int,
) (string, error) {
	content := "Cumulative summary so far:\n" + previous +
		"\nNext sequential transcript fragment (it may start or end within a record):\n" + fragment
	feedback, problem := "", ""
	reasoning := configuration.ReasoningLevel

	// Summarization does not need the main task's highest reasoning effort.
	// Retain explicit settings for models with unknown capabilities.
	if len(domain.ReasoningLevels(configuration.Provider.Kind, configuration.Model.ID)) > 0 {
		reasoning = domain.ResolveReasoningLevel(configuration.Provider.Kind, configuration.Model.ID, "medium", "main")
	}

	for attempt := range summaryAttempts {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		request := providers.Request{
			Config:         configuration.Provider,
			Model:          configuration.Model,
			ReasoningLevel: reasoning,
			System:         system,
			Messages:       []domain.Message{{Role: "user", Content: content + feedback}},
			CacheKey:       agentRecord.ID + "-compact",
		}

		result, err := engine.Provider.Complete(ctx, request)
		if err != nil {
			return "", err
		}

		if err := engine.recordAuxiliaryUsage(agentRecord, configuration, result, request.Messages, estimatedPromptTokens(system, nil)); err != nil {
			return "", err
		}

		text := strings.TrimSpace(result.Text)
		switch {
		case len(result.Calls) > 0:
			problem = "the model returned tool calls instead of a summary"
		case text == "":
			problem = "the model returned an empty summary"
		case len(text) > limit:
			problem = fmt.Sprintf("the summary contains %d UTF-8 bytes (limit %d)", len(text), limit)
		default:
			return text, nil
		}

		slog.Warn("invalid context summary", "agent", agentRecord.ID,
			"attempt", attempt+1, "max_attempts", summaryAttempts,
			"bytes", len(text), "limit_bytes", limit, "reason", problem)

		feedback = fmt.Sprintf(
			"\n\nHarness validation: %s. Regenerate the summary from the same original data above. "+
				"Return only a nonempty summary of at most %d UTF-8 bytes. "+
				"Consolidate repeated details; preserve instructions, decisions, denials and pending work. Do not call tools.",
			problem, max(64, limit/(2<<attempt)),
		)
	}

	return "", fmt.Errorf("context summarization failed after %d attempts: %s; original history retained", summaryAttempts, problem)
}

func splitTranscript(transcript string, limit int) (string, string) {
	if len(transcript) <= limit {
		return transcript, ""
	}

	boundary := limit

	for boundary > 0 && !utf8.RuneStart(transcript[boundary]) {
		boundary--
	}

	return transcript[:boundary], transcript[boundary:]
}
