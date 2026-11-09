package agent

import (
	"context"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

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
		"\nUpdate the cumulative summary using this fragment. Keep the result under %d UTF-8 bytes. "+
			"Preserve the goal, current plan, user corrections, denied actions, validated results, "+
			"pending checks and concrete next steps. Do not claim unfinished work is complete.",
		summaryLimit,
	)

	for remaining != "" {
		// ContextWindow is measured in tokens; fragment lengths are UTF-8 bytes.
		// Reserve space for the cumulative summary, instructions and the response.
		inputLimit := max(256, configuration.Model.ContextWindow*3-len(summary)-len(system)-256)
		fragment, rest := splitTranscript(remaining, inputLimit)
		requestMessages := []domain.Message{
			{
				Role: "user",
				Content: "Cumulative summary so far:\n" + summary +
					"\nNext sequential transcript fragment (it may start or end within a record):\n" + fragment,
			},
		}

		result, err := engine.Provider.Complete(ctx, providers.Request{
			Config: configuration.Provider,
			Model:  configuration.Model,

			System: system,

			Messages: requestMessages,
			CacheKey: agentRecord.ID + "-compact",
		})
		if err != nil {
			return "", err
		}

		if err := engine.recordUsage(agentRecord, configuration, result, requestMessages, estimatedPromptTokens(system, nil)); err != nil {
			return "", err
		}

		if result.Text == "" || len(result.Text) > summaryLimit {
			return "", errors.New("context summary is empty or exceeds its reserved budget; original history retained")
		}

		summary = result.Text
		remaining = rest
	}

	return summary, nil
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
