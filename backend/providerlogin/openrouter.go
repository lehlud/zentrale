package providerlogin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (client *Client) beginOpenRouter(providerID string) Transaction {
	verifier := randomCode()
	parameters := url.Values{
		"code_challenge":        {challenge(verifier)},
		"code_challenge_method": {"S256"},
		"key_label":             {"Zentrale"},
	}

	return Transaction{
		Session: domain.ProviderLogin{
			ProviderID:      providerID,
			Method:          "browser",
			Status:          "pending",
			VerificationURL: normalizeAuthRoot(client.OpenRouterURL) + "/auth?" + parameters.Encode(),
			ExpiresAt:       time.Now().Add(10 * time.Minute),
		},
		CodeVerifier: verifier,
	}
}

func (client *Client) CompleteBrowser(
	ctx context.Context,
	transaction Transaction,
	code string,
) (string, error) {
	if transaction.Session.Method != "browser" || transaction.CodeVerifier == "" {
		return "", errors.New("invalid browser login transaction")
	}

	if time.Now().After(transaction.Session.ExpiresAt) {
		return "", errors.New("provider login expired")
	}

	if code == "" {
		return "", errors.New("authorization code is required")
	}

	response, status, err := client.post(ctx, normalizeAuthRoot(client.OpenRouterURL)+"/api/v1/auth/keys", map[string]any{
		"code":                  code,
		"code_verifier":         transaction.CodeVerifier,
		"code_challenge_method": "S256",
	}, false)
	if err != nil {
		return "", err
	}

	if status != http.StatusOK {
		return "", tokenError(status)
	}

	credential := stringValue(response["key"])
	if credential == "" {
		return "", errors.New("provider returned no account credential")
	}

	return credential, nil
}
