package agent

import (
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
)

type googleCredentials struct {
	ClientSecret string              `json:"clientSecret"`
	Tokens       *domain.OAuthTokens `json:"tokens,omitempty"`
}

// New Google app and account credentials never enter the ordinary resource
// JSON. Only the in-memory provider used by authentication/inference receives
// them; the public snapshot and encrypted record commit together.
func (engine *Engine) loadProvider(id string, provider *domain.Provider) error {
	if err := engine.Store.Load("provider", id, provider); err != nil {
		return err
	}

	if provider.AuthMode != "oauth" {
		return nil
	}

	encoded, err := engine.Store.Credential("google_provider/" + id)
	if err != nil {
		return err
	}

	var credentials googleCredentials
	if err := json.Unmarshal(encoded, &credentials); err != nil {
		return err
	}

	provider.OAuthClientSecret = credentials.ClientSecret
	provider.OAuth = credentials.Tokens

	return nil
}

func (engine *Engine) saveProvider(provider domain.Provider) error {
	if provider.AuthMode != "oauth" {
		provider.OAuthClientSecret = ""
		return engine.Store.Save("provider", provider.ID, "", provider)
	}

	credentials := googleCredentials{ClientSecret: provider.OAuthClientSecret, Tokens: provider.OAuth}
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return err
	}

	provider.Connected = provider.OAuth != nil && provider.OAuth.AccessToken != ""
	provider.AccountLabel = ""
	if provider.Connected {
		provider.AccountLabel = provider.OAuthProjectID
	}

	provider.Credential = ""
	provider.OAuthClientSecret = ""
	provider.OAuth = nil

	return engine.Store.SaveCredentialResource(
		"google_provider/"+provider.ID, encoded, "provider", provider.ID, "", provider,
	)
}
