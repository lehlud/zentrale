package modelcatalog

import (
	"math"
	"strconv"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

// Account discovery determines availability. Metadata never introduces an ID that
// was absent from the account's response.
func decodeModel(provider domain.Provider, record map[string]any) (domain.LanguageModel, bool) {
	id := text(record["id"])
	name := text(record["name"])
	if provider.Kind == "gemini" {
		id = strings.TrimPrefix(name, "models/")
		name = text(record["displayName"])
		supportsGeneration := false
		for _, method := range array(record["supportedGenerationMethods"]) {
			supportsGeneration = supportsGeneration || method == "generateContent"
		}
		if !supportsGeneration {
			return domain.LanguageModel{}, false
		}
	}
	if provider.Kind == "openai" && provider.AuthMode == "device" {
		id = text(record["slug"])
		name = text(record["display_name"])
		if record["visibility"] == "hidden" {
			return domain.LanguageModel{}, false
		}
	}
	if provider.Kind == "openai" && provider.AuthMode != "device" && !isTextModel(id) {
		return domain.LanguageModel{}, false
	}
	modalities := object(record["architecture"])["output_modalities"]
	if outputs := array(modalities); len(outputs) > 0 {
		textOutput := false
		for _, output := range outputs {
			textOutput = textOutput || output == "text"
		}
		if !textOutput {
			return domain.LanguageModel{}, false
		}
	}
	if id == "" {
		return domain.LanguageModel{}, false
	}
	if name == "" {
		name = text(record["display_name"])
	}
	if name == "" {
		name = id
	}
	if record["lifecycle"] == "retired" {
		return domain.LanguageModel{}, false
	}
	capabilities := object(record["capabilities"])
	if chat, known := capabilities["completion_chat"].(bool); known && !chat {
		return domain.LanguageModel{}, false
	}
	model := domain.LanguageModel{ID: id, ProviderID: provider.ID, Name: name}
	for _, field := range []string{"context_length", "context_window", "max_context_length", "max_input_tokens", "inputTokenLimit"} {
		if limit := int(number(record[field])); limit > 0 {
			model.ContextWindow = limit
			break
		}
	}
	if model.ContextWindow == 0 {
		model.ContextWindow = 32000
		model.MetadataEstimated = true
	}
	pricing := object(record["pricing"])
	inputPrice, inputKnown := pricePerMillion(pricing["prompt"])
	outputPrice, outputKnown := pricePerMillion(pricing["completion"])
	if inputKnown && outputKnown {
		model.InputPrice = inputPrice
		model.OutputPrice = outputPrice
		model.CachedPrice = inputPrice
		if cached, known := pricePerMillion(pricing["input_cache_read"]); known {
			model.CachedPrice = cached
		}
		model.PricesKnown = true
	}
	return model, true
}

func isTextModel(id string) bool {
	for _, marker := range []string{"embedding", "whisper", "tts", "dall-e", "sora", "image", "moderation", "realtime", "transcribe", "audio", "babbage", "davinci"} {
		if strings.Contains(strings.ToLower(id), marker) {
			return false
		}
	}
	return true
}

// Catalogs can mark dynamically routed or unknown rates with a negative value.
// Such markers must never turn into negative costs or apparent free inference.
func pricePerMillion(value any) (float64, bool) {
	var rate float64
	switch typed := value.(type) {
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
		rate = parsed
	case float64:
		rate = typed
	default:
		return 0, false
	}
	price := rate * 1000000
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 || price > 1000000000 {
		return 0, false
	}
	return price, true
}
