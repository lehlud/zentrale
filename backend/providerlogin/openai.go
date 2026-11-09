package providerlogin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (client *Client) beginOpenAI(ctx context.Context, providerID string) (Transaction, error) {
	address := normalizeAuthRoot(client.OpenAIAuthURL) + "/api/accounts/deviceauth/usercode"
	response, status, err := client.post(ctx, address, map[string]any{"client_id": openAIClientID}, false)
	if err != nil {
		return Transaction{}, err
	}

	if status != http.StatusOK {
		return Transaction{}, tokenError(status)
	}

	interval := seconds(response["interval"], 5)
	deviceID := stringValue(response["device_auth_id"])
	userCode := stringValue(response["user_code"])
	if deviceID == "" || userCode == "" {
		return Transaction{}, errors.New("provider returned an incomplete device login")
	}

	return Transaction{
		Session: domain.ProviderLogin{
			ProviderID:      providerID,
			Method:          "device",
			Status:          "pending",
			VerificationURL: normalizeAuthRoot(client.OpenAIAuthURL) + "/codex/device",
			UserCode:        userCode,
			Interval:        interval,
			ExpiresAt:       time.Now().Add(15 * time.Minute),
		},
		DeviceAuthorizationID: deviceID,
	}, nil
}

// Poll performs one step. The web client schedules subsequent steps using the
// provider's interval; no blocking polling loop occupies an HTTP request.
func (client *Client) Poll(ctx context.Context, transaction Transaction) (*domain.OAuthTokens, bool, error) {
	if time.Now().After(transaction.Session.ExpiresAt) {
		return nil, false, errors.New("provider login expired")
	}

	address := normalizeAuthRoot(client.OpenAIAuthURL) + "/api/accounts/deviceauth/token"
	response, status, err := client.post(ctx, address, map[string]any{
		"device_auth_id": transaction.DeviceAuthorizationID,
		"user_code":      transaction.Session.UserCode,
	}, false)
	if err != nil {
		return nil, false, err
	}

	if status == http.StatusForbidden || status == http.StatusNotFound {
		return nil, true, nil
	}

	if status != http.StatusOK {
		switch stringValue(response["error"]) {
		case "authorization_pending", "slow_down":
			return nil, true, nil
		default:
			return nil, false, tokenError(status)
		}
	}

	authorizationCode := stringValue(response["authorization_code"])
	verifier := stringValue(response["code_verifier"])
	if authorizationCode == "" || verifier == "" {
		return nil, false, errors.New("provider returned an incomplete authorization code")
	}

	tokens, err := client.exchangeOpenAI(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openAIClientID},
		"code":          {authorizationCode},
		"code_verifier": {verifier},
		"redirect_uri":  {normalizeAuthRoot(client.OpenAIAuthURL) + "/deviceauth/callback"},
	})

	return tokens, false, err
}

func (client *Client) Refresh(ctx context.Context, previous domain.OAuthTokens) (*domain.OAuthTokens, error) {
	if previous.RefreshToken == "" {
		return nil, errors.New("provider session expired; sign in again")
	}

	refreshed, err := client.exchangeOpenAI(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {openAIClientID},
		"refresh_token": {previous.RefreshToken},
	})
	if err != nil {
		return nil, err
	}

	if refreshed.RefreshToken == "" {
		refreshed.RefreshToken = previous.RefreshToken
	}

	if refreshed.AccountID == "" {
		refreshed.AccountID = previous.AccountID
	}

	if refreshed.AccountLabel == "" {
		refreshed.AccountLabel = previous.AccountLabel
	}

	return refreshed, nil
}

func (client *Client) exchangeOpenAI(ctx context.Context, values url.Values) (*domain.OAuthTokens, error) {
	address := normalizeAuthRoot(client.OpenAIAuthURL) + "/oauth/token"
	response, status, err := client.post(ctx, address, values, true)
	if err != nil {
		return nil, err
	}

	if status != http.StatusOK {
		return nil, tokenError(status)
	}

	accessToken := stringValue(response["access_token"])
	if accessToken == "" {
		return nil, errors.New("provider returned no access token")
	}

	accountID, accountLabel := accountClaims(stringValue(response["id_token"]))
	if accountID == "" {
		accountID, accountLabel = accountClaims(accessToken)
	}

	return &domain.OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: stringValue(response["refresh_token"]),
		ExpiresAt:    time.Now().Add(time.Duration(seconds(response["expires_in"], 3600)) * time.Second),
		AccountID:    accountID,
		AccountLabel: accountLabel,
	}, nil
}

// Claims are used only as display/routing metadata. Provider servers validate
// the signed access token; these unverified claims never grant local access.
func accountClaims(token string) (string, string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}

	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return "", ""
	}

	identity, _ := claims["https://api.openai.com/auth"].(map[string]any)

	return stringValue(identity["chatgpt_account_id"]), stringValue(claims["email"])
}

func seconds(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case string:
		if number, err := strconv.Atoi(typed); err == nil && number > 0 {
			return number
		}
	}

	return fallback
}
