package huggingface

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/oauthflow"
)

type oauthApp struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

type oauthAccount struct {
	RefreshToken string    `json:"refreshToken,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

type oauthLogin struct {
	Public domain.HuggingFaceLogin `json:"public"`
	Flow   oauthflow.Pending       `json:"flow"`
}

// One encrypted record makes app/account/flow replacement atomic. Public
// metadata is derived on read; no private flow or token reaches the snapshot.
func (service *Service) saveAccount(value account) error {
	if value.Token == "" && value.App.ClientID == "" && value.Login == nil {
		return service.Store.DeleteCredential(credentialName)
	}

	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("could not encode Hugging Face credentials")
	}

	return service.Store.SetCredential(credentialName, encoded)
}

func connectionMetadata(value account) domain.HuggingFaceConnection {
	metadata := value.Public
	if metadata.Connected && metadata.AuthMode == "" {
		metadata.AuthMode = "token" // Existing token records need no migration.
	}

	metadata.OAuthClientID = value.App.ClientID
	metadata.OAuthConfigured = value.App.ClientID != ""
	metadata.OAuthConfidential = value.App.ClientSecret != ""
	metadata.Login = domain.HuggingFaceLogin{}
	if value.Login != nil {
		metadata.Login = value.Login.Public
	}

	return metadata
}

func (service *Service) sessionValid(owner string) bool {
	valid, err := service.Store.SessionValid(owner)
	return owner != "" && err == nil && valid
}

// The caller holds mu. A cancelled browser login does not interrupt downloads
// using the previously connected account.
func (service *Service) cancelLogin() {
	if service.loginCancel != nil {
		service.loginCancel()
		service.loginCancel = nil
	}
}

func (service *Service) ConfigureOAuth(config domain.HuggingFaceOAuthConfig) (domain.HuggingFaceConnection, error) {
	config.ClientID = strings.TrimSpace(config.ClientID)
	if len(config.ClientID) > 2048 || len(config.ClientSecret) > 4096 ||
		strings.IndexFunc(config.ClientID+config.ClientSecret, unicode.IsControl) >= 0 {
		return domain.HuggingFaceConnection{}, errors.New("invalid Hugging Face OAuth app configuration")
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.closed {
		return domain.HuggingFaceConnection{}, errors.New("Hugging Face connection service is closed")
	}

	value, err := service.account()
	if err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	app := oauthApp{ClientID: config.ClientID}
	if app.ClientID != "" && !config.PublicClient {
		app.ClientSecret = config.ClientSecret
		if app.ClientSecret == "" && app.ClientID == value.App.ClientID {
			app.ClientSecret = value.App.ClientSecret
		}

		if app.ClientSecret == "" {
			return domain.HuggingFaceConnection{}, errors.New("the confidential Hugging Face app requires a client secret")
		}
	}

	if app == value.App {
		return connectionMetadata(value), nil
	}

	disconnect := value.OAuth != nil
	if disconnect {
		value.Public = domain.HuggingFaceConnection{}
		value.Token = ""
		value.OAuth = nil
	}

	value.App = app
	value.Login = nil
	if err := service.saveAccount(value); err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	service.cancelLogin()
	if disconnect {
		service.invalidateRequests()
	}

	return connectionMetadata(value), nil
}

func (service *Service) BeginOAuth(ctx context.Context) (domain.HuggingFaceLogin, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	callback := oauthflow.CallbackFrom(ctx)
	if service.closed || !service.sessionValid(callback.Owner) {
		return domain.HuggingFaceLogin{}, errors.New("Hugging Face browser login requires an active application session")
	}

	value, err := service.account()
	if err != nil {
		return domain.HuggingFaceLogin{}, err
	}

	if value.App.ClientID == "" {
		return domain.HuggingFaceLogin{}, errors.New("configure a Hugging Face OAuth app first")
	}

	id := domain.NewID()
	flow, err := oauthflow.New("huggingface:"+id+":", callback, time.Now())
	if err != nil {
		return domain.HuggingFaceLogin{}, err
	}

	login := oauthLogin{
		Flow: flow,
		Public: domain.HuggingFaceLogin{
			ID:               id,
			AuthorizationURL: flow.AuthorizationURL(origin+"/oauth/authorize", value.App.ClientID, oauthScopes, nil),
			ExpiresAt:        flow.ExpiresAt,
		},
	}
	value.Login = &login
	if err := service.saveAccount(value); err != nil {
		return domain.HuggingFaceLogin{}, err
	}

	service.cancelLogin()
	return login.Public, nil
}

func (service *Service) EndOAuth(id, owner string) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account()
	if err != nil || value.Login == nil {
		return err
	}

	if value.Login.Public.ID != id || value.Login.Flow.Owner != owner {
		return errors.New("Hugging Face login is not current for this session")
	}

	value.Login = nil
	if err := service.saveAccount(value); err != nil {
		return err
	}

	service.cancelLogin()
	return nil
}

func (service *Service) CompleteOAuth(ctx context.Context, id, state, code, owner string) (domain.HuggingFaceConnection, error) {
	if code == "" || len(code) > 4096 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return domain.HuggingFaceConnection{}, errors.New("invalid Hugging Face authorization code")
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	service.mu.Lock()
	value, err := service.account()
	if err == nil {
		err = service.validateLogin(value, id, state, owner)
	}

	if err != nil {
		service.mu.Unlock()
		return domain.HuggingFaceConnection{}, err
	}

	flow := value.Login.Flow
	value.Login.Flow.Verifier = ""
	if err := service.saveAccount(value); err != nil {
		service.mu.Unlock()
		return domain.HuggingFaceConnection{}, err
	}

	epoch := service.epoch
	ctx, release := service.requestContext(ctx)
	service.loginCancel = cancel
	service.mu.Unlock()
	defer release()

	tokens, err := service.oauthTokens(ctx, value.App, authorizationGrant(flow, code))
	var username string
	if err == nil {
		username, err = service.oauthUsername(ctx, tokens.AccessToken)
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	current, loadErr := service.account()
	if loadErr != nil {
		return domain.HuggingFaceConnection{}, loadErr
	}

	if current.Login == nil || current.Login.Public.ID != id || service.epoch != epoch {
		return domain.HuggingFaceConnection{}, errors.New("Hugging Face connection changed; start again")
	}

	if err == nil && (service.closed || ctx.Err() != nil || !service.sessionValid(owner) ||
		!time.Now().Before(flow.ExpiresAt)) {
		err = errors.New("Hugging Face login expired or was cancelled; start again")
	}

	current.Login = nil
	if err == nil {
		current.Token = tokens.AccessToken
		current.OAuth = &oauthAccount{RefreshToken: tokens.RefreshToken, ExpiresAt: tokens.ExpiresAt}
		current.Public = domain.HuggingFaceConnection{
			Connected: true,
			UserName:  username,
			CheckedAt: time.Now().UTC(),
			AuthMode:  "oauth",
		}
	}

	if saveErr := service.saveAccount(current); saveErr != nil {
		return domain.HuggingFaceConnection{}, saveErr
	}

	service.cancelLogin()
	if err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	service.invalidateRequests()
	return connectionMetadata(current), nil
}

func (service *Service) validateLogin(value account, id, state, owner string) error {
	if service.closed || value.Login == nil || value.Login.Public.ID != id {
		return errors.New("Hugging Face login is no longer current; start again")
	}

	if err := value.Login.Flow.Validate(state, owner, time.Now()); err != nil {
		return err
	}

	if !service.sessionValid(owner) {
		return errors.New("Hugging Face login session expired; start again")
	}

	return nil
}
