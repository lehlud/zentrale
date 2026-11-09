package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
	"github.com/lehlud/zentrale/providers"
)

func (engine *Engine) RefreshProviderModels(ctx context.Context, providerID string) error {
	engine.catalogMu.Lock()
	defer engine.catalogMu.Unlock()
	engine.configMu.Lock()
	revision := engine.catalogRevisions[providerID]
	engine.configMu.Unlock()
	if err := engine.Store.Patch("provider", providerID, map[string]any{
		"catalogStatus": "loading", "catalogError": "",
	}); err != nil {
		return err
	}
	engine.Emit("provider", "", map[string]string{"id": providerID})
	provider, err := engine.providerWithCredentials(ctx, providerID)
	var models []domain.LanguageModel
	if err == nil {
		models, err = engine.Catalog.List(ctx, provider)
	}

	// An account can be edited or disconnected while discovery is in flight.
	// Publish only results for the configuration with which the request began.
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if engine.catalogRevisions[providerID] != revision {
		return errors.New("provider changed during discovery; refresh models again")
	}
	if err == nil {
		resources := make(map[string]any, len(models))
		for _, model := range models {
			var metadata domain.ModelMetadata
			if engine.Store.Load("model_metadata", modelResourceID(providerID, model.ID), &metadata) == nil {
				applyModelMetadata(&model, metadata)
			}
			resources[modelResourceID(providerID, model.ID)] = model
		}
		err = engine.Store.ReplaceCollection("model", providerID, resources)
		if err == nil {
			err = engine.Store.Patch("provider", providerID, map[string]any{
				"catalogStatus": "ready", "catalogError": "", "modelCount": len(models),
			})
		}
	}
	if err != nil {
		_ = engine.Store.Patch("provider", providerID, map[string]any{
			"catalogStatus": "error", "catalogError": err.Error(),
		})
	}
	engine.Emit("provider", "", map[string]string{"id": providerID})
	return err
}

func (engine *Engine) scheduleModelRefresh(providerID string) {
	engine.workers.Add(1)
	go func() {
		defer engine.workers.Done()
		_ = engine.RefreshProviderModels(engine.ctx, providerID)
	}()
}

func (engine *Engine) BeginProviderLogin(ctx context.Context, providerID string) (domain.ProviderLogin, error) {
	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()
	var provider domain.Provider
	if err := engine.Store.Load("provider", providerID, &provider); err != nil {
		return domain.ProviderLogin{}, err
	}
	transaction, err := engine.AccountLogin.Begin(ctx, provider)
	if err != nil {
		return domain.ProviderLogin{}, err
	}
	transaction.Session.ID = NewID()
	if err := engine.Store.Save("provider_login", transaction.Session.ID, providerID, transaction); err != nil {
		return domain.ProviderLogin{}, err
	}
	return transaction.Session, nil
}

func (engine *Engine) PollProviderLogin(ctx context.Context, loginID, authorizationCode string) (domain.ProviderLogin, error) {
	// Serialize exchanges: a device grant and an authorization code are single-use.
	engine.accountMu.Lock()
	var transaction providerlogin.Transaction
	if err := engine.Store.Load("provider_login", loginID, &transaction); err != nil {
		engine.accountMu.Unlock()
		return domain.ProviderLogin{}, err
	}
	if transaction.Session.Status != "pending" {
		engine.accountMu.Unlock()
		return transaction.Session, nil
	}
	if time.Now().After(transaction.Session.ExpiresAt) {
		transaction.Session.Status = "expired"
		transaction.Session.Error = "provider login expired"
	} else if transaction.Session.Method == "device" {
		if time.Now().Before(transaction.NextPollAt) {
			engine.accountMu.Unlock()
			return transaction.Session, nil
		}
		transaction.NextPollAt = time.Now().Add(time.Duration(max(5, transaction.Session.Interval)) * time.Second)
		tokens, pending, err := engine.AccountLogin.Poll(ctx, transaction)
		switch {
		case err != nil:
			transaction.Session.Status = "error"
			transaction.Session.Error = err.Error()
		case !pending:
			err = engine.Store.Patch("provider", transaction.Session.ProviderID, map[string]any{"oauth": tokens})
			if err == nil {
				transaction.Session.Status = "connected"
			} else {
				transaction.Session.Status = "error"
				transaction.Session.Error = err.Error()
			}
		}
	} else if authorizationCode != "" {
		credential, err := engine.AccountLogin.CompleteBrowser(ctx, transaction, authorizationCode)
		if err == nil {
			err = engine.Store.Patch("provider", transaction.Session.ProviderID, map[string]any{"credential": credential})
		}
		if err == nil {
			transaction.Session.Status = "connected"
		} else {
			// An incorrect pasted code can be corrected without discarding the verifier.
			transaction.Session.Error = err.Error()
		}
	}
	if transaction.Session.Status == "connected" {
		transaction.CodeVerifier = ""
		transaction.DeviceAuthorizationID = ""
	}
	err := engine.Store.Save("provider_login", loginID, transaction.Session.ProviderID, transaction)
	engine.accountMu.Unlock()
	if err != nil {
		return transaction.Session, err
	}
	if transaction.Session.Status == "connected" {
		engine.configMu.Lock()
		engine.catalogRevisions[transaction.Session.ProviderID]++
		engine.configMu.Unlock()
		engine.scheduleModelRefresh(transaction.Session.ProviderID)
	}
	return transaction.Session, nil
}

func (engine *Engine) CancelProviderLogin(loginID string) error {
	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()
	return engine.Store.Remove("provider_login", loginID)
}

func (engine *Engine) DisconnectProvider(providerID string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()
	engine.catalogRevisions[providerID]++
	if err := engine.Store.ReplaceCollection("provider_login", providerID, nil); err != nil {
		return err
	}
	if err := engine.Store.Patch("provider", providerID, map[string]any{
		"credential": "", "oauth": nil, "catalogStatus": "disconnected", "catalogError": "", "modelCount": 0,
	}); err != nil {
		return err
	}
	if err := engine.Store.ReplaceCollection("model", providerID, nil); err != nil {
		return err
	}
	engine.Emit("provider", "", map[string]string{"id": providerID})
	return nil
}

func (engine *Engine) TestProvider(ctx context.Context, providerID, modelID string) (bool, error) {
	if modelID == "" {
		return false, errors.New("choose a discovered model to test inference")
	}
	configuration, err := engine.inferenceConfiguration(ctx, domain.Agent{ProviderID: providerID, ModelID: modelID})
	if err != nil {
		return false, err
	}
	result, err := engine.Provider.Complete(ctx, providers.Request{
		Config:   configuration.Provider,
		Model:    configuration.Model,
		System:   "Respond with OK.",
		Messages: []domain.Message{{Role: "user", Content: "Connection test"}},
		CacheKey: "connection-test",
	})
	if err != nil {
		return false, err
	}
	if err := engine.recordUsage(domain.Agent{}, configuration, result, nil); err != nil {
		return false, err
	}
	return result.Text != "", nil
}
