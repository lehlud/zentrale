package agent

import (
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
	"github.com/lehlud/zentrale/remote"
)

func (engine *Engine) SaveProvider(configuration domain.Provider) (domain.Provider, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	engine.accountMu.Lock()
	defer engine.accountMu.Unlock()

	if configuration.ID == "" {
		configuration.ID = NewID()
	}

	if !remote.ValidID(configuration.ID) || strings.TrimSpace(configuration.Name) == "" {
		return configuration, errors.New("provider id and name are required")
	}

	if err := providers.Validate(&configuration); err != nil {
		return configuration, err
	}

	var previous domain.Provider
	if engine.Store.Load("provider", configuration.ID, &previous) == nil &&
		configuration.Kind == previous.Kind &&
		configuration.AuthMode == previous.AuthMode &&
		configuration.BaseURL == previous.BaseURL {
		if configuration.Credential == "" {
			configuration.Credential = previous.Credential
		}

		configuration.OAuth = previous.OAuth
		configuration.ModelCount = previous.ModelCount
		configuration.CatalogStatus = previous.CatalogStatus
	} else {
		if err := engine.Store.ReplaceCollection("model", configuration.ID, nil); err != nil {
			return configuration, err
		}
	}

	if err := engine.Store.ReplaceCollection("provider_login", configuration.ID, nil); err != nil {
		return configuration, err
	}

	if err := engine.Store.Save("provider", configuration.ID, "", configuration); err != nil {
		return configuration, err
	}

	engine.catalogRevisions[configuration.ID]++

	if configuration.AuthMode == "api" || configuration.Credential != "" || configuration.OAuth != nil {
		engine.scheduleModelRefresh(configuration.ID)
	}

	configuration.Credential = ""
	configuration.OAuth = nil
	engine.Emit("provider", "", map[string]string{"id": configuration.ID})

	return configuration, nil
}
