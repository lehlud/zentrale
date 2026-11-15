package providerlogin

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
)

var ErrGoogleReauthorization = errors.New("Google API authorization expired or was revoked; sign in again")

func (client *Client) beginGoogle(ctx context.Context, provider domain.Provider) (Transaction, error) {
	if provider.OAuthClientID == "" || provider.OAuthClientSecret == "" || provider.OAuthProjectID == "" {
		return Transaction{}, errors.New("configure a Google web OAuth app and API billing project first")
	}

	id := domain.NewID()
	pending, err := oauthflow.New("provider:"+id+":", oauthflow.CallbackFrom(ctx), time.Now())
	if err != nil {
		return Transaction{}, err
	}

	scopes := []string{
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/generative-language.retriever",
	}
	parameters := url.Values{"access_type": {"offline"}, "prompt": {"consent"}}

	return Transaction{
		Session: domain.ProviderLogin{
			ID:              id,
			ProviderID:      provider.ID,
			Method:          "oauth",
			Status:          "pending",
			VerificationURL: pending.AuthorizationURL(client.GoogleAuthURL, provider.OAuthClientID, scopes, parameters),
			ExpiresAt:       pending.ExpiresAt,
		},
		BrowserFlow: &pending,
	}, nil
}

// The caller validates and consumes the persisted flow before this single
// request. It must never retry an authorization code after a lost response.
func (client *Client) ExchangeGoogle(ctx context.Context, provider domain.Provider, pending oauthflow.Pending, code string) (*domain.OAuthTokens, error) {
	if code == "" || len(code) > 4096 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid Google authorization code")
	}

	return client.googleTokens(ctx, provider, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {pending.RedirectURI},
		"code_verifier": {pending.Verifier},
	})
}

func (client *Client) RefreshGoogle(ctx context.Context, provider domain.Provider) (*domain.OAuthTokens, error) {
	if provider.OAuth == nil || provider.OAuth.RefreshToken == "" {
		return nil, ErrGoogleReauthorization
	}

	tokens, err := client.googleTokens(ctx, provider, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {provider.OAuth.RefreshToken},
	})
	if err != nil {
		return nil, err
	}

	if tokens.RefreshToken == "" {
		tokens.RefreshToken = provider.OAuth.RefreshToken
	}

	return tokens, nil
}

func (client *Client) googleTokens(ctx context.Context, provider domain.Provider, values url.Values) (*domain.OAuthTokens, error) {
	values.Set("client_id", provider.OAuthClientID)
	values.Set("client_secret", provider.OAuthClientSecret)

	// In particular, 307/308 must not forward an app secret or refresh token to
	// another origin. Keep the caller's transport, but disable all redirects.
	httpClient := *client.HTTP
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	isolated := *client
	isolated.HTTP = &httpClient

	response, status, err := isolated.post(ctx, client.GoogleTokenURL, values, true)
	if err != nil {
		return nil, errors.New("Google API token request failed")
	}

	if status == http.StatusBadRequest && stringValue(response["error"]) == "invalid_grant" {
		return nil, ErrGoogleReauthorization
	}

	accessToken := stringValue(response["access_token"])
	if status != http.StatusOK || accessToken == "" ||
		!strings.EqualFold(stringValue(response["token_type"]), "Bearer") {
		return nil, errors.New("Google API authorization failed; check app configuration and consent")
	}

	return &domain.OAuthTokens{
		AccessToken:  accessToken,
		RefreshToken: stringValue(response["refresh_token"]),
		ExpiresAt:    time.Now().Add(time.Duration(seconds(response["expires_in"], 3600)) * time.Second),
		AccountLabel: provider.OAuthProjectID,
	}, nil
}
