package agent

import (
	"errors"
	"math"

	"github.com/lehlud/zentrale/domain"
)

// Some account catalogs omit context limits or prices. Explicit estimates belong
// to a discovered model, never to provider configuration or model availability.
func (engine *Engine) SaveModelMetadata(providerID, modelID string, metadata domain.ModelMetadata) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if metadata.ContextWindow < 1024 || metadata.ContextWindow > 10000000 {
		return errors.New("context window must contain 1024–10000000 tokens")
	}

	for _, price := range []float64{metadata.InputPrice, metadata.OutputPrice, metadata.CachedPrice} {
		if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
			return errors.New("token prices must be finite and non-negative")
		}
	}

	model, err := engine.validateModelChoice(domain.ModelChoice{ProviderID: providerID, ModelID: modelID})
	if err != nil {
		return err
	}

	applyModelMetadata(&model, metadata)
	id := modelResourceID(providerID, modelID)
	if err := engine.Store.Save("model_metadata", id, providerID, metadata); err != nil {
		return err
	}

	if err := engine.Store.Save("model", id, providerID, model); err != nil {
		return err
	}

	engine.Emit("model", "", model)

	return nil
}

func applyModelMetadata(model *domain.LanguageModel, metadata domain.ModelMetadata) {
	model.ContextWindow = metadata.ContextWindow
	model.InputPrice = metadata.InputPrice
	model.OutputPrice = metadata.OutputPrice
	model.CachedPrice = metadata.CachedPrice
	model.PricesKnown = metadata.PricesKnown
	model.MetadataEstimated = true
}
