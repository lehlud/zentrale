package gitforge

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
)

func (service *Service) beginBrowser(ctx context.Context, value account) (domain.GitLogin, error) {
	if value.Public.Kind == "gitea" {
		if err := service.checkGiteaVersion(ctx, value); err != nil {
			return domain.GitLogin{}, err
		}
	}

	id := domain.NewID()
	prefix := "git:" + value.Public.ID + ":" + id + ":"
	pending, err := oauthflow.New(prefix, oauthflow.CallbackFrom(ctx), service.Now())
	if err != nil {
		return domain.GitLogin{}, err
	}

	// Gitea's organization route requires organization write scope when creating
	// an organization repository. No admin, package, issue or user-write access
	// is requested. Forgejo currently does not implement OAuth scope restriction.
	var scopes []string
	if value.Public.Kind == "gitea" {
		scopes = []string{"read:user", "write:repository", "write:organization"}
	}

	login := domain.GitLogin{
		ID:              id,
		ConnectionID:    value.Public.ID,
		Method:          "browser",
		Status:          "pending",
		Interval:        5,
		ExpiresAt:       pending.ExpiresAt,
		VerificationURL: pending.AuthorizationURL(value.Public.BaseURL+"/login/oauth/authorize", value.Public.ClientID, scopes, nil),
	}
	value.Login = &loginTransaction{Public: login, BrowserFlow: &pending}

	return login, service.save(value)
}

// CompleteBrowser consumes the PKCE verifier before its single token exchange.
// Lost responses are not replayed. Once a token is stored, an interrupted
// profile read can be retried without resubmitting the authorization code.
func (service *Service) CompleteBrowser(ctx context.Context, connectionID, loginID, state, code, owner string) (domain.GitLogin, error) {
	unlock, err := service.gates.LockContext(ctx, connectionID)
	if err != nil {
		return domain.GitLogin{}, err
	}
	defer unlock()

	value, err := service.account(connectionID)
	if err != nil {
		return domain.GitLogin{}, err
	}

	if value.Login == nil || value.Login.Public.ID != loginID ||
		value.Login.Public.Status != "pending" || value.Login.BrowserFlow == nil {
		return domain.GitLogin{}, errors.New("Git login is no longer current; start again")
	}

	pending := *value.Login.BrowserFlow
	if err := pending.Validate(state, owner, service.Now()); err != nil {
		return value.Login.Public, err
	}

	if err := service.browserSessionValid(owner); err != nil {
		return value.Login.Public, err
	}

	value.Login.BrowserFlow.Verifier = ""
	if err := service.save(value); err != nil {
		return value.Login.Public, err
	}

	if code == "" || len(code) > 4096 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return service.rejectBrowser(&value)
	}

	response, err := service.Client.oauth(ctx, value, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {pending.RedirectURI},
		"code_verifier": {pending.Verifier},
	})
	if err != nil || text(response["error"]) != "" || ctx.Err() != nil {
		return service.rejectBrowser(&value)
	}

	if err := service.browserSessionValid(owner); err != nil {
		return service.rejectBrowser(&value)
	}

	value.Scopes = nil
	value.RefreshToken = ""
	if err := service.acceptToken(&value, response); err != nil {
		return service.rejectBrowser(&value)
	}

	value.Public.UserName = ""
	value.Login.NextPollAt = service.Now().Add(5 * time.Second)
	if err := service.save(value); err != nil {
		return value.Login.Public, err
	}

	return service.finishLogin(ctx, &value)
}

func (service *Service) browserSessionValid(owner string) error {
	valid, err := service.Store.UserSessionValid(owner)
	if err != nil || !valid {
		return errors.New("initiating application session expired; start the Git login again")
	}

	return nil
}

func (service *Service) rejectBrowser(value *account) (domain.GitLogin, error) {
	value.AccessToken = ""
	value.RefreshToken = ""
	value.Scopes = nil
	value.TokenExpiry = time.Time{}
	value.Public.UserName = ""
	value.Public.Connected = false

	value.Login.Public.Status = "failed"
	value.Login.Public.Error = "Git browser login could not be completed; start again"
	value.Login.BrowserFlow = nil

	return value.Login.Public, service.save(*value)
}

// Older Gitea versions may ignore unknown scope names and grant full access.
// Do not silently turn a restricted app registration into an unrestricted one.
func (service *Service) checkGiteaVersion(ctx context.Context, value account) error {
	var response struct {
		Version string `json:"version"`
	}

	err := service.Client.accountRequest(ctx, value, "GET", service.Client.apiRoot(value)+"/version", nil, false, &response)
	if err != nil {
		return errors.New("could not verify Gitea version; Gitea 1.23 or newer is required")
	}

	parts := strings.Split(strings.TrimPrefix(response.Version, "v"), ".")
	if len(parts) < 2 {
		return errors.New("unrecognized Gitea version; Gitea 1.23 or newer is required")
	}

	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 1 || (major == 1 && minor < 23) {
		return errors.New("Gitea 1.23 or newer is required for restricted OAuth scopes")
	}

	return nil
}
