package agent

import (
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

const tokensPerMillion = 1_000_000

func estimatedMessageTokens(messages []domain.Message) int {
	count := 0

	for _, message := range messages {
		payload := message.Content
		if len(message.Attachments) != 0 {
			payload += JSON(message.Attachments)
		}

		if message.Wire != "" {
			payload = message.Wire
			if tokens, ok := providers.EstimatedResponsesTokens(message.Wire); ok {
				count += tokens
				continue
			}
		}

		count += (len(payload) + 3) / 4
	}

	return count
}

func estimatedPromptTokens(system string, tools []providers.Tool) int {
	return (len(system) + len(JSON(tools)) + 3) / 4
}

func (engine *Engine) recordUsage(
	agentRecord domain.Agent,
	configuration inferenceConfig,
	result providers.Result,
	messages []domain.Message,
	promptTokens ...int,
) error {
	return engine.recordInferenceUsage(agentRecord, configuration, result, messages, true, promptTokens...)
}

// Internal summaries are billable, but their input/output is not the active
// conversation window. A failed summary must not replace its token estimate.
func (engine *Engine) recordAuxiliaryUsage(
	agentRecord domain.Agent,
	configuration inferenceConfig,
	result providers.Result,
	messages []domain.Message,
	promptTokens ...int,
) error {
	return engine.recordInferenceUsage(agentRecord, configuration, result, messages, false, promptTokens...)
}

func (engine *Engine) recordInferenceUsage(
	agentRecord domain.Agent,
	configuration inferenceConfig,
	result providers.Result,
	messages []domain.Message,
	updateContext bool,
	promptTokens ...int,
) error {
	// Traced inference is settled before its reservation is released. The
	// active conversation caller still updates the context estimate here.
	if result.TraceID != "" && !updateContext {
		return nil
	}
	usage := inferenceUsage(agentRecord, configuration, result, messages, promptTokens...)
	if result.TraceID == "" {
		if err := engine.persistInferenceUsage(agentRecord, usage); err != nil {
			return err
		}
	}
	if updateContext && agentRecord.ID != "" {
		return engine.Store.Patch("agent", agentRecord.ID, map[string]any{"contextTokens": usage.InputTokens + usage.OutputTokens})
	}
	return nil
}

func inferenceUsage(agentRecord domain.Agent, configuration inferenceConfig, result providers.Result, messages []domain.Message, promptTokens ...int) domain.Usage {
	inputTokens, outputTokens := result.InputTokens, result.OutputTokens

	if !result.UsageKnown {
		inputTokens = estimatedMessageTokens(messages)

		if len(promptTokens) > 0 {
			inputTokens += promptTokens[0]
		}

		outputTokens = estimatedMessageTokens([]domain.Message{{Content: result.Text, Wire: result.Wire}})
	}

	inputTokens, outputTokens = max(inputTokens, 0), max(outputTokens, 0)
	cachedTokens, writeTokens, longWriteTokens := 0, 0, 0

	if result.UsageKnown {
		cachedTokens = min(max(result.CachedTokens, 0), inputTokens)
		writeTokens = min(max(result.CacheWriteTokens, 0), inputTokens-cachedTokens)
		longWriteTokens = min(max(result.CacheWriteLongTokens, 0), writeTokens)
	}

	usage := domain.Usage{
		TraceID: result.TraceID, Purpose: result.Purpose,
		ID:                   NewID(),
		AgentID:              agentRecord.ID,
		ProviderID:           configuration.Provider.ID,
		ModelID:              configuration.Model.ID,
		InputTokens:          inputTokens,
		OutputTokens:         outputTokens,
		CachedTokens:         cachedTokens,
		TokensKnown:          result.UsageKnown,
		CacheReadKnown:       result.UsageKnown && result.CacheReadKnown,
		CacheWriteTokens:     writeTokens,
		CacheWriteLongTokens: longWriteTokens,
		CacheWriteKnown:      result.UsageKnown && result.CacheWriteKnown,
		CostKnown:            configuration.Model.PricesKnown,
		Estimated:            true,
	}

	inputCost := float64(inputTokens-cachedTokens) * configuration.Model.InputPrice

	if configuration.Provider.Kind == "openai" {
		// Responses uses the default 30m write price (1.25x), or 2.5x for
		// explicitly reported 12h writes. InputTokens already includes writes.
		inputCost += (float64(writeTokens)*0.25 + float64(longWriteTokens)*1.25) * configuration.Model.InputPrice
	} else if writeTokens > 0 {
		// Other providers have their own write pricing. Retain the counts but
		// do not present the ordinary-input approximation as a known cost.
		usage.CostKnown = false
	}

	cacheCost := float64(cachedTokens) * configuration.Model.CachedPrice
	outputCost := float64(outputTokens) * configuration.Model.OutputPrice
	equivalentCost := (inputCost + cacheCost + outputCost) / tokensPerMillion

	if !usesRealInferenceCost(configuration.Provider) {
		usage.FictionalCost = equivalentCost
	} else {
		usage.RealCost = equivalentCost

		if result.Cost != nil {
			usage.RealCost = *result.Cost
			usage.CostKnown = true
			usage.Estimated = false
		}
	}

	return usage
}

func (engine *Engine) persistInferenceUsage(agentRecord domain.Agent, usage domain.Usage) error {
	if err := engine.Store.SaveUsage(usage, agentRecord); err != nil {
		return err
	}
	engine.Emit("usage", agentRecord.ID, usage)

	return nil
}
