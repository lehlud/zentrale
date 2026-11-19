package agent

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
	"github.com/lehlud/zentrale/providerlogin"
	"github.com/lehlud/zentrale/providers"
)

func (engine *Engine) RefreshProviderModels(ctx context.Context, providerID string) error {
	catalogUnlock, err := engine.catalogGates.LockContext(ctx, providerID)
	if err != nil {
		return err
	}
	defer catalogUnlock()

	engine.configMu.Lock()
	revision := engine.catalogRevisions[providerID]
	engine.configMu.Unlock()

	var public domain.Provider
	if err := engine.Store.Load("provider", providerID, &public); err != nil {
		return err
	}
	if hosted.Kind(public.Kind) {
		return errors.New("select a model ID or endpoint for this hosted inference provider")
	}

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
			resources[modelResourceID(providerID, model.ID)] = model
		}

		// Most catalog entries have no custom metadata. Read only the saved
		// overrides so large catalogs don't issue a database query per model.
		overrides, loadErr := engine.Store.ModelMetadataOverrides(providerID)
		for id, override := range overrides {
			if model, exists := resources[id].(domain.LanguageModel); exists {
				applyModelMetadata(&model, override)
				resources[id] = model
			}
		}
		err = loadErr
		if err == nil {
			err = engine.Store.ReplaceCollection("model", providerID, resources)
		}
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
	accountUnlock, err := engine.accountGates.LockContext(ctx, providerID)
	if err != nil {
		return domain.ProviderLogin{}, err
	}
	defer accountUnlock()

	var provider domain.Provider
	if err := engine.loadProvider(providerID, &provider); err != nil {
		return domain.ProviderLogin{}, err
	}

	transaction, err := engine.AccountLogin.Begin(ctx, provider)
	if err != nil {
		return domain.ProviderLogin{}, err
	}

	if transaction.Session.ID == "" {
		transaction.Session.ID = NewID()
	}

	if err := engine.clearProviderLogins(providerID); err != nil {
		return domain.ProviderLogin{}, err
	}

	if err := engine.saveProviderLogin(transaction); err != nil {
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
	accountUnlock := engine.accountGates.Lock(engine.loginProviderID(loginID))
	var transaction providerlogin.Transaction
	err := engine.loadProviderLogin(loginID, &transaction)
	if errors.Is(err, sql.ErrNoRows) {
		accountUnlock()
		return nil
	}
	if err != nil {
		accountUnlock()
		return err
	}
	if transaction.Session.Status != "pending" {
		accountUnlock()
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
	transaction.BrowserFlow = nil
	err = engine.saveProviderLogin(transaction)
	accountUnlock()
	if err == nil {
		engine.Emit("provider_login", "", map[string]string{"id": loginID})
	}
	return err
}

func (engine *Engine) PollProviderLogin(ctx context.Context, loginID, authorizationCode string) (domain.ProviderLogin, error) {
	// Serialize exchanges: a device grant and an authorization code are single-use.
	accountUnlock, err := engine.accountGates.LockContext(ctx, engine.loginProviderID(loginID))
	if err != nil {
		return domain.ProviderLogin{}, err
	}
	var transaction providerlogin.Transaction
	if err := engine.loadProviderLogin(loginID, &transaction); err != nil {
		accountUnlock()
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ProviderLogin{Status: "cancelled"}, nil
		}
		return domain.ProviderLogin{}, err
	}

	if transaction.Session.Status != "pending" {
		accountUnlock()
		return transaction.Session, nil
	}
	before := transaction.Session

	if time.Now().After(transaction.Session.ExpiresAt) {
		transaction.Session.Status = "expired"
		transaction.Session.Error = "provider login expired"
	} else if transaction.Session.Method == "device" {
		if time.Now().Before(transaction.NextPollAt) {
			accountUnlock()
			return transaction.Session, nil
		}

		transaction.NextPollAt = time.Now().Add(time.Duration(max(5, transaction.Session.Interval)) * time.Second)
		tokens, pending, err := engine.AccountLogin.Poll(ctx, transaction)
		if ctx.Err() != nil {
			accountUnlock()
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
			if saveErr := engine.saveProviderLogin(transaction); saveErr != nil {
				accountUnlock()
				return transaction.Session, saveErr
			}
			accountUnlock()
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
	} else if transaction.Session.Method == "browser" && authorizationCode != "" {
		credential, err := engine.AccountLogin.CompleteBrowser(ctx, transaction, authorizationCode)
		if ctx.Err() != nil {
			accountUnlock()
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
		transaction.BrowserFlow = nil
	}

	err = engine.saveProviderLogin(transaction)
	accountUnlock()

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
	accountUnlock := engine.accountGates.Lock(engine.loginProviderID(loginID))
	err := engine.Store.DeleteCredential("google_login/" + loginID)
	if err == nil {
		err = engine.Store.Remove("provider_login", loginID)
	}
	accountUnlock()
	if err == nil {
		engine.Emit("provider_login", "", map[string]string{"id": loginID})
	}
	return err
}

func (engine *Engine) DisconnectProvider(providerID string) error {
	engine.stopLoginPolling("provider:"+providerID, "")
	accountUnlock := engine.accountGates.Lock(providerID)
	defer accountUnlock()
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	engine.catalogRevisions[providerID]++

	if err := engine.clearProviderLogins(providerID); err != nil {
		return err
	}

	var provider domain.Provider
	if err := engine.loadProvider(providerID, &provider); err != nil {
		return err
	}

	if provider.AuthMode == "oauth" {
		provider.OAuth = nil
		provider.CatalogStatus = "disconnected"
		provider.CatalogError = ""
		provider.ModelCount = 0
		if err := engine.saveProvider(provider); err != nil {
			return err
		}
	}

	if err := engine.Store.Patch("provider", providerID, map[string]any{
		"credential":    "",
		"connected":     false,
		"oauth":         nil,
		"catalogStatus": "disconnected",
		"catalogError":  "",
		"modelCount":    0,
	}); err != nil {
		return err
	}
	if provider.ModalModelID != "" {
		engine.cancelModalDeployment(providerID)
		if err := engine.Store.DeleteCredential("modal_provider/" + providerID); err != nil {
			return err
		}
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

	result, err := engine.completeModel(ctx, "", "connection_test", providers.Request{
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

// Routing uses only public session data. Always reload the transaction under
// the provider gate before exchanging a single-use code or rotating tokens.
func (engine *Engine) loginProviderID(loginID string) string {
	var transaction providerlogin.Transaction
	if engine.Store.Load("provider_login", loginID, &transaction) == nil {
		return transaction.Session.ProviderID
	}
	return "missing-login:" + loginID
}
