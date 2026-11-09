package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type inferenceConfig struct {
	Provider domain.Provider
	Model    domain.LanguageModel
}

func modelResourceID(providerID, modelID string) string {
	return providerID + ":" + modelID
}

func (engine *Engine) ModelDefaults() (domain.ModelDefaults, error) {
	encoded, err := engine.Store.Setting("model_defaults")
	if err != nil {
		return domain.ModelDefaults{}, err
	}

	if encoded == "" {
		return domain.ModelDefaults{}, nil
	}

	var defaults domain.ModelDefaults
	err = decode(encoded, &defaults)

	return defaults, err
}

func (engine *Engine) SaveModelDefaults(defaults domain.ModelDefaults) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	for _, choice := range []domain.ModelChoice{defaults.Main, defaults.Approver} {
		if _, err := engine.validateModelChoice(choice); err != nil {
			return err
		}
	}

	if err := engine.Store.SetSetting("model_defaults", JSON(defaults)); err != nil {
		return err
	}

	engine.Emit("model_defaults", "", defaults)

	return nil
}

func (engine *Engine) validateModelChoice(choice domain.ModelChoice) (domain.LanguageModel, error) {
	if choice.ProviderID == "" || choice.ModelID == "" {
		return domain.LanguageModel{}, errors.New("select a provider and one of its discovered models")
	}

	var provider domain.Provider
	if err := engine.Store.Load("provider", choice.ProviderID, &provider); err != nil {
		return domain.LanguageModel{}, err
	}

	if (provider.AuthMode == "device" ||
		provider.AuthMode == "browser") &&
		provider.Credential == "" &&
		provider.OAuth == nil {
		return domain.LanguageModel{}, errors.New("sign in to this provider first")
	}

	var model domain.LanguageModel
	if err := engine.Store.Load("model", modelResourceID(choice.ProviderID, choice.ModelID), &model); err != nil {
		return model, errors.New("selected model is not in the provider catalog; refresh available models")
	}

	return model, nil
}

func (engine *Engine) resolveChoice(choice domain.ModelChoice, role string) (domain.ModelChoice, error) {
	if choice.ProviderID != "" && choice.ModelID != "" {
		_, err := engine.validateModelChoice(choice)
		return choice, err
	}

	defaults, err := engine.ModelDefaults()
	if err != nil {
		return choice, err
	}

	configured := defaults.Main

	if role == "approver" {
		configured = defaults.Approver
	}

	if choice.ProviderID != "" && choice.ProviderID != configured.ProviderID {
		return choice, errors.New("select an explicit model for this provider")
	}

	if _, err := engine.validateModelChoice(configured); err != nil {
		return choice, errors.New("configure global main and approver model defaults first")
	}

	return configured, nil
}

func (engine *Engine) inferenceConfiguration(ctx context.Context, agentRecord domain.Agent) (inferenceConfig, error) {
	choice, err := engine.resolveChoice(
		domain.ModelChoice{
			ProviderID: agentRecord.ProviderID,
			ModelID:    agentRecord.ModelID,
		},
		agentRecord.Role,
	)
	if err != nil {
		return inferenceConfig{}, err
	}

	provider, err := engine.providerWithCredentials(ctx, choice.ProviderID)
	if err != nil {
		return inferenceConfig{}, err
	}

	model, err := engine.validateModelChoice(choice)

	return inferenceConfig{Provider: provider, Model: model}, err
}

func (engine *Engine) providerWithCredentials(ctx context.Context, providerID string) (domain.Provider, error) {
	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()

	var provider domain.Provider
	if err := engine.Store.Load("provider", providerID, &provider); err != nil {
		return provider, err
	}

	if provider.AuthMode == "device" {
		if provider.OAuth == nil {
			return provider, errors.New("sign in to this provider first")
		}

		if time.Until(provider.OAuth.ExpiresAt) < time.Minute {
			refreshed, err := engine.AccountLogin.Refresh(ctx, *provider.OAuth)
			if err != nil {
				return provider, err
			}

			if err := engine.Store.Patch("provider", providerID, map[string]any{
				"oauth": refreshed,
			}); err != nil {
				return provider, err
			}

			provider.OAuth = refreshed
		}
	}

	return provider, nil
}
