package agent

import (
	"encoding/json"
	"errors"

	"github.com/lehlud/zentrale/providerlogin"
)

func (engine *Engine) saveProviderLogin(transaction providerlogin.Transaction) error {
	session := transaction.Session
	if session.Method != "oauth" {
		return engine.Store.Save("provider_login", session.ID, session.ProviderID, transaction)
	}

	encoded, err := json.Marshal(transaction)
	if err != nil {
		return err
	}

	public := providerlogin.Transaction{Session: session}
	return engine.Store.SaveCredentialResource(
		"google_login/"+session.ID, encoded, "provider_login", session.ID, session.ProviderID, public,
	)
}

func (engine *Engine) loadProviderLogin(id string, transaction *providerlogin.Transaction) error {
	if err := engine.Store.Load("provider_login", id, transaction); err != nil {
		return err
	}

	if transaction.Session.Method != "oauth" {
		return nil
	}

	encoded, err := engine.Store.Credential("google_login/" + id)
	if err != nil {
		return err
	}

	return json.Unmarshal(encoded, transaction)
}

func (engine *Engine) clearProviderLogins(providerID string) error {
	for {
		rows, err := engine.Store.List("provider_login", &providerID, 100, 0)
		if err != nil {
			return err
		}

		if len(rows) == 0 {
			return nil
		}

		for _, row := range rows {
			session, _ := row["session"].(map[string]any)
			id, _ := session["id"].(string)
			if id == "" {
				return errors.New("provider login has no session identity")
			}

			if err := engine.Store.DeleteCredential("google_login/" + id); err != nil {
				return err
			}

			if err := engine.Store.Remove("provider_login", id); err != nil {
				return err
			}
		}
	}
}
