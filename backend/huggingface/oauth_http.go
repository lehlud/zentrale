package huggingface

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lehlud/zentrale/oauthflow"
)

// No write, inference, billing, email or request-gated-repos permission is
// needed. Gated access still requires the user's own prior approval/acceptance.
var oauthScopes = []string{"openid", "profile", "read-repos", "gated-repos"}
var bearerPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/-]+=*$`)
var errReauthorize = errors.New("Hugging Face OAuth access expired or was revoked; sign in again")

type oauthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	Scope        string    `json:"scope"`
	Error        string    `json:"error"`
	ExpiresAt    time.Time `json:"-"`
}

func authorizationGrant(flow oauthflow.Pending, code string) url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {flow.RedirectURI}, "code_verifier": {flow.Verifier},
	}
}

func (service *Service) oauthTokens(ctx context.Context, app oauthApp, parameters url.Values) (oauthTokens, error) {
	parameters.Set("client_id", app.ClientID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, origin+"/oauth/token", strings.NewReader(parameters.Encode()))
	if err != nil {
		return oauthTokens{}, errors.New("invalid Hugging Face OAuth request")
	}

	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if app.ClientSecret != "" {
		request.SetBasicAuth(app.ClientID, app.ClientSecret)
	}

	response, err := service.oauthHTTP(request)
	if err != nil {
		return oauthTokens{}, err
	}
	defer response.Body.Close()

	var tokens oauthTokens
	if err := decodeResponse(response.Body, &tokens, 64*1024); err != nil {
		return oauthTokens{}, errors.New("invalid Hugging Face OAuth response")
	}

	if tokens.Error == "invalid_grant" || tokens.Error == "invalid_token" || tokens.Error == "access_denied" {
		return oauthTokens{}, errReauthorize
	}

	if response.StatusCode != http.StatusOK || tokens.Error != "" {
		return oauthTokens{}, errors.New("Hugging Face OAuth request rejected; check app configuration and consent")
	}

	if !strings.EqualFold(tokens.TokenType, "bearer") || !validBearer(tokens.AccessToken) ||
		(tokens.RefreshToken != "" && !validBearer(tokens.RefreshToken)) ||
		tokens.ExpiresIn < 1 || tokens.ExpiresIn > 366*24*60*60 {
		return oauthTokens{}, errors.New("Hugging Face returned invalid OAuth credentials")
	}

	if tokens.Scope != "" {
		granted := " " + strings.Join(strings.Fields(tokens.Scope), " ") + " "
		for _, scope := range oauthScopes {
			if !strings.Contains(granted, " "+scope+" ") {
				return oauthTokens{}, errors.New("Hugging Face did not grant the required read/profile/gated scopes")
			}
		}
	}

	tokens.ExpiresAt = time.Now().UTC().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	return tokens, nil
}

func validBearer(token string) bool {
	return len(token) > 0 && len(token) <= 16384 && bearerPattern.MatchString(token)
}

func (service *Service) oauthUsername(ctx context.Context, token string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/oauth/userinfo", nil)
	if err != nil {
		return "", errors.New("invalid Hugging Face userinfo request")
	}
	request.Header.Set("Authorization", "Bearer "+token)

	response, err := service.oauthHTTP(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	var identity struct {
		Subject  string `json:"sub"`
		Username string `json:"preferred_username"`
	}
	if response.StatusCode != http.StatusOK || decodeResponse(response.Body, &identity, 64*1024) != nil ||
		identity.Subject == "" || !namePattern.MatchString(identity.Username) {
		return "", errors.New("Hugging Face did not return a valid OAuth user profile; start sign-in again")
	}

	return identity.Username, nil
}

// Credentials and codes must never follow redirects, even to Hub/CDN hosts
// which are intentionally allowed for dataset downloads.
func (service *Service) oauthHTTP(request *http.Request) (*http.Response, error) {
	client := *service.Client
	client.Timeout = 20 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("Hugging Face OAuth connection failed; start sign-in again")
	}

	return response, nil
}
