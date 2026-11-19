package agent

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/hosted"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) SaveProvider(configuration domain.Provider) (domain.Provider, error) {
	if configuration.ID == "" {
		configuration.ID = NewID()
	}
	accountUnlock := engine.accountGates.Lock(configuration.ID)
	defer accountUnlock()
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if !remote.ValidID(configuration.ID) || strings.TrimSpace(configuration.Name) == "" {
		return configuration, errors.New("provider id and name are required")
	}

	if err := providers.Validate(&configuration); err != nil {
		return configuration, err
	}

	var previous domain.Provider
	loadErr := engine.loadProvider(configuration.ID, &previous)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return configuration, loadErr
	}
	if previous.Kind == "fal" {
		_ = hosted.ValidateProvider(&previous)
	}

	preserveAuthorization := loadErr == nil &&
		configuration.Kind == previous.Kind &&
		configuration.AuthMode == previous.AuthMode &&
		configuration.OAuthClientID == previous.OAuthClientID &&
		configuration.OAuthProjectID == previous.OAuthProjectID &&
		(configuration.OAuthClientSecret == "" || configuration.OAuthClientSecret == previous.OAuthClientSecret) &&
		configuration.BaseURL == previous.BaseURL
	if preserveAuthorization {
		if configuration.Credential == "" {
			configuration.Credential = previous.Credential
		}

		configuration.OAuth = previous.OAuth
		if configuration.OAuthClientSecret == "" {
			configuration.OAuthClientSecret = previous.OAuthClientSecret
		}
		configuration.ModelCount = previous.ModelCount
		configuration.CatalogStatus = previous.CatalogStatus
	}

	if configuration.AuthMode == "oauth" && configuration.OAuthClientSecret == "" {
		return configuration, errors.New("Google web OAuth client secret is required")
	}
	isHosted := hosted.Kind(configuration.Kind)
	if configuration.Kind == "featherless" || isHosted {
		configuration.Credential = strings.TrimSpace(configuration.Credential)
		if configuration.Credential == "" {
			return configuration, errors.New("an API key is required for this provider")
		}
	}
	if isHosted {
		configuration.ModelCount = 0
		configuration.CatalogStatus = "external"
		configuration.CatalogError = ""
	}

	if !preserveAuthorization {
		configuration.OAuth = nil
		if err := engine.Store.ReplaceCollection("model", configuration.ID, nil); err != nil {
			return configuration, err
		}
	}

	if configuration.AuthMode == "oauth" {
		configuration.Credential = ""
	}

	if err := engine.clearProviderLogins(configuration.ID); err != nil {
		return configuration, err
	}

	if err := engine.saveProvider(configuration); err != nil {
		return configuration, err
	}

	engine.stopLoginPolling("provider:"+configuration.ID, "")
	if configuration.AuthMode != "oauth" && previous.AuthMode == "oauth" {
		if err := engine.Store.DeleteCredential("google_provider/" + configuration.ID); err != nil {
			return configuration, err
		}
	}

	engine.catalogRevisions[configuration.ID]++

	if !isHosted && (configuration.AuthMode == "api" || configuration.Credential != "" || configuration.OAuth != nil) {
		engine.scheduleModelRefresh(configuration.ID)
	}

	configuration.Connected = configuration.Credential != "" ||
		(configuration.OAuth != nil && configuration.OAuth.AccessToken != "") ||
		(configuration.AuthMode != "oauth" && configuration.CatalogStatus == "ready")
	configuration.AccountLabel = ""
	if configuration.OAuth != nil {
		configuration.AccountLabel = configuration.OAuth.AccountLabel
	}

	configuration.Credential = ""
	configuration.OAuth = nil
	configuration.OAuthClientSecret = ""
	engine.Emit("provider", "", map[string]string{"id": configuration.ID})

	return configuration, nil
}
