package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

func (engine *Engine) CompleteGoogleLogin(ctx context.Context, loginID, stateValue, code, owner string) (domain.ProviderLogin, error) {
	accountUnlock, err := engine.accountGates.LockContext(ctx, engine.loginProviderID(loginID))
	if err != nil {
		return domain.ProviderLogin{}, err
	}
	login, err := engine.completeGoogleLogin(ctx, loginID, stateValue, code, owner)
	accountUnlock()

	if login.ID != "" {
		engine.Emit("provider_login", "", map[string]string{"id": login.ID})
	}

	if err == nil && login.Status == "connected" {
		engine.stopLoginPolling("provider:"+login.ProviderID, login.ID)
		engine.configMu.Lock()
		engine.catalogRevisions[login.ProviderID]++
		engine.scheduleModelRefresh(login.ProviderID)
		engine.configMu.Unlock()
	}

	return login, err
}

func (engine *Engine) completeGoogleLogin(ctx context.Context, loginID, stateValue, code, owner string) (domain.ProviderLogin, error) {
	var transaction providerlogin.Transaction
	if err := engine.loadProviderLogin(loginID, &transaction); err != nil {
		return domain.ProviderLogin{}, errors.New("provider login is no longer current; start again")
	}

	if transaction.Session.Method != "oauth" || transaction.Session.Status != "pending" || transaction.BrowserFlow == nil {
		return transaction.Session, errors.New("provider login is no longer current; start again")
	}

	pending := *transaction.BrowserFlow
	if err := pending.Validate(stateValue, owner, time.Now()); err != nil {
		return transaction.Session, err
	}

	if err := engine.googleLoginSessionValid(owner); err != nil {
		return transaction.Session, err
	}

	var provider domain.Provider
	if err := engine.loadProvider(transaction.Session.ProviderID, &provider); err != nil {
		return transaction.Session, err
	}

	if provider.Kind != "gemini" || provider.AuthMode != "oauth" {
		return transaction.Session, errors.New("provider configuration changed; start again")
	}

	// Persist consumption before sending the single-use code. Even a cancelled
	// request or a lost token response cannot cause an automatic second exchange.
	transaction.BrowserFlow.Verifier = ""
	if err := engine.saveProviderLogin(transaction); err != nil {
		return transaction.Session, err
	}

	tokens, err := engine.AccountLogin.ExchangeGoogle(ctx, provider, pending, code)
	if err == nil {
		err = ctx.Err()
	}

	if err == nil {
		err = engine.googleLoginSessionValid(owner)
	}

	if err == nil {
		provider.OAuth = tokens
		provider.CatalogStatus = "connected"
		provider.CatalogError = ""
		err = engine.saveProvider(provider)
	}

	transaction.BrowserFlow = nil
	transaction.Session.Status = "connected"
	transaction.Session.Error = ""
	if err != nil {
		transaction.Session.Status = "error"
		transaction.Session.Error = "Google API login could not be completed; start again"
	}

	if saveErr := engine.saveProviderLogin(transaction); saveErr != nil {
		return transaction.Session, saveErr
	}

	return transaction.Session, err
}

func (engine *Engine) googleLoginSessionValid(owner string) error {
	valid, err := engine.Store.SessionValid(owner)
	if err != nil || !valid {
		return errors.New("initiating application session expired; start the provider login again")
	}

	return nil
}
