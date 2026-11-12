package agent

import (
	"context"
	"database/sql"
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
	// Callers hold configMu, serializing worker registration with Close.
	if engine.ctx.Err() != nil {
		return
	}
	engine.workers.Add(1)
	go func() {
		defer engine.workers.Done()

		_ = engine.RefreshProviderModels(engine.ctx, providerID)
	}()
}

func (engine *Engine) BeginProviderLogin(ctx context.Context, providerID string) (domain.ProviderLogin, error) {
	engine.stopLoginPolling("provider:"+providerID, "")
	login, err := engine.beginProviderLogin(ctx, providerID)
	if err != nil {
		return login, err
	}
	err = engine.startProviderLogin(login)
	if err != nil {
		_ = engine.CancelProviderLogin(login.ID)
		return login, err
	}
	engine.Emit("provider_login", "", map[string]string{"id": login.ID})
	return login, nil
}

func (engine *Engine) startProviderLogin(login domain.ProviderLogin) error {
	var poll func(context.Context) (loginPollState, error)
	if login.Method == "device" {
		poll = func(ctx context.Context) (loginPollState, error) {
			value, err := engine.PollProviderLogin(ctx, login.ID, "")
			return loginPollState{status: value.Status, interval: value.Interval}, err
		}
	}
	return engine.startLoginPolling("provider:"+login.ProviderID, login.ID, login.ExpiresAt, login.Interval, poll,
		func(reason string) { _ = engine.endProviderLogin(login.ID, reason) })
}

func (engine *Engine) beginProviderLogin(ctx context.Context, providerID string) (domain.ProviderLogin, error) {
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

// Public state is read without accountMu: a remote OAuth request must not block
// read-only snapshots, and only the public Session is ever returned.
func (engine *Engine) ProviderLogin(loginID string) (*domain.ProviderLogin, error) {
	var transaction providerlogin.Transaction
	if err := engine.Store.Load("provider_login", loginID, &transaction); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &transaction.Session, nil
}

func (engine *Engine) endProviderLogin(loginID, status string) error {
	engine.accountMu.Lock()
	var transaction providerlogin.Transaction
	err := engine.Store.Load("provider_login", loginID, &transaction)
	if errors.Is(err, sql.ErrNoRows) {
		engine.accountMu.Unlock()
		return nil
	}
	if err != nil {
		engine.accountMu.Unlock()
		return err
	}
	if transaction.Session.Status != "pending" {
		engine.accountMu.Unlock()
		return nil
	}
	transaction.Session.Status = status
	if status == "cancelled" {
		transaction.Session.Error = "provider login cancelled; start again"
	}
	if status == "expired" {
		transaction.Session.Error = "provider login expired"
	}
	transaction.CodeVerifier, transaction.DeviceAuthorizationID = "", ""
	err = engine.Store.Save("provider_login", loginID, transaction.Session.ProviderID, transaction)
	engine.accountMu.Unlock()
	if err == nil {
		engine.Emit("provider_login", "", map[string]string{"id": loginID})
	}
	return err
}

func (engine *Engine) PollProviderLogin(ctx context.Context, loginID, authorizationCode string) (domain.ProviderLogin, error) {
	// Serialize exchanges: a device grant and an authorization code are single-use.
	engine.accountMu.Lock()
	var transaction providerlogin.Transaction
	if err := engine.Store.Load("provider_login", loginID, &transaction); err != nil {
		engine.accountMu.Unlock()
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ProviderLogin{Status: "cancelled"}, nil
		}
		return domain.ProviderLogin{}, err
	}

	if transaction.Session.Status != "pending" {
		engine.accountMu.Unlock()
		return transaction.Session, nil
	}
	before := transaction.Session

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
		if ctx.Err() != nil {
			engine.accountMu.Unlock()
			return transaction.Session, ctx.Err()
		}
		var retryable *providerlogin.RetryablePollError
		var slower *providerlogin.SlowDownPollError

		switch {
		case errors.As(err, &slower):
			transaction.Session.Interval = max(5, transaction.Session.Interval) + 5
			transaction.NextPollAt = time.Now().Add(time.Duration(transaction.Session.Interval) * time.Second)
		case errors.As(err, &retryable):
			transaction.Session.Error = "provider login temporarily unavailable; retrying"
			if saveErr := engine.Store.Save("provider_login", loginID, transaction.Session.ProviderID, transaction); saveErr != nil {
				engine.accountMu.Unlock()
				return transaction.Session, saveErr
			}
			engine.accountMu.Unlock()
			if before != transaction.Session {
				engine.Emit("provider_login", "", map[string]string{"id": loginID})
			}
			return transaction.Session, err
		case err != nil:
			transaction.Session.Status = "error"
			transaction.Session.Error = err.Error()
		case !pending:
			err = engine.Store.Patch("provider", transaction.Session.ProviderID, map[string]any{
				"oauth": tokens,
			})
			if err == nil {
				transaction.Session.Status = "connected"
			} else {
				transaction.Session.Status = "error"
				transaction.Session.Error = err.Error()
			}
		default:
			transaction.Session.Error = ""
		}
	} else if authorizationCode != "" {
		credential, err := engine.AccountLogin.CompleteBrowser(ctx, transaction, authorizationCode)
		if ctx.Err() != nil {
			engine.accountMu.Unlock()
			return transaction.Session, ctx.Err()
		}
		if err == nil {
			err = engine.Store.Patch("provider", transaction.Session.ProviderID, map[string]any{
				"credential": credential,
			})
		}

		if err == nil {
			transaction.Session.Status = "connected"
		} else {
			// An incorrect pasted code can be corrected without discarding the verifier.
			transaction.Session.Error = err.Error()
		}
	}

	if transaction.Session.Status != "pending" {
		transaction.CodeVerifier = ""
		transaction.DeviceAuthorizationID = ""
	}

	err := engine.Store.Save("provider_login", loginID, transaction.Session.ProviderID, transaction)
	engine.accountMu.Unlock()

	if err != nil {
		return transaction.Session, err
	}
	if before != transaction.Session {
		engine.Emit("provider_login", "", map[string]string{"id": loginID})
	}

	if transaction.Session.Status == "connected" {
		engine.stopLoginPolling("provider:"+transaction.Session.ProviderID, loginID)
		engine.configMu.Lock()
		engine.catalogRevisions[transaction.Session.ProviderID]++
		engine.scheduleModelRefresh(transaction.Session.ProviderID)
		engine.configMu.Unlock()
	}

	return transaction.Session, nil
}

func (engine *Engine) CancelProviderLogin(loginID string) error {
	engine.stopLoginPolling("", loginID)
	engine.accountMu.Lock()
	err := engine.Store.Remove("provider_login", loginID)
	engine.accountMu.Unlock()
	if err == nil {
		engine.Emit("provider_login", "", map[string]string{"id": loginID})
	}
	return err
}

func (engine *Engine) DisconnectProvider(providerID string) error {
	engine.stopLoginPolling("provider:"+providerID, "")
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()

	engine.catalogRevisions[providerID]++

	if err := engine.Store.ReplaceCollection("provider_login", providerID, nil); err != nil {
		return err
	}

	if err := engine.Store.Patch("provider", providerID, map[string]any{
		"credential":    "",
		"oauth":         nil,
		"catalogStatus": "disconnected",
		"catalogError":  "",
		"modelCount":    0,
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

	configuration, err := engine.inferenceConfiguration(ctx, domain.Agent{
		ProviderID: providerID,
		ModelID:    modelID,
	})
	if err != nil {
		return false, err
	}

	result, err := engine.Provider.Complete(ctx, providers.Request{
		Config:         configuration.Provider,
		Model:          configuration.Model,
		ReasoningLevel: configuration.ReasoningLevel,
		System:         "Respond with OK.",
		Messages:       []domain.Message{{Role: "user", Content: "Connection test"}},
		CacheKey:       "connection-test",
	})
	if err != nil {
		return false, err
	}

	if err := engine.recordUsage(domain.Agent{}, configuration, result, nil); err != nil {
		return false, err
	}

	return result.Text != "", nil
}
