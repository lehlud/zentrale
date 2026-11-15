package gitforge

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/netguard"
	"github.com/lehlud/zentrale/oauthflow"
)

type account struct {
	Public domain.GitConnection `json:"public"`

	Secret       string    `json:"secret,omitempty"`
	AccessToken  string    `json:"accessToken,omitempty"`
	RefreshToken string    `json:"refreshToken,omitempty"`
	TokenExpiry  time.Time `json:"tokenExpiry"`
	Scopes       []string  `json:"scopes,omitempty"`

	Login *loginTransaction `json:"login,omitempty"`
}

type loginTransaction struct {
	Public      domain.GitLogin    `json:"public"`
	DeviceCode  string             `json:"deviceCode"`
	NextPollAt  time.Time          `json:"nextPollAt"`
	BrowserFlow *oauthflow.Pending `json:"browserFlow,omitempty"`
}

type repositoryCache struct {
	At           time.Time
	Repositories []domain.GitRepository
}

type Service struct {
	Store  *datastore.Instance
	Client *Client
	Now    func() time.Time

	mu    sync.Mutex
	cache map[string]repositoryCache
}

func New(store *datastore.Instance) *Service {
	return &Service{Store: store, Client: NewClient(), Now: time.Now, cache: map[string]repositoryCache{}}
}

func (service *Service) account(id string) (account, error) {
	value := account{Public: domain.GitConnection{ID: id, Kind: id, BaseURL: "https://" + id + ".com"}}
	err := service.loadAccount(id, &value)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return value, err
	}

	if errors.Is(err, sql.ErrNoRows) && id != "github" && id != "gitlab" {
		return account{}, errors.New("unknown Git connection")
	}

	prefix := strings.ToUpper(id)

	if value.Public.ClientID == "" {
		value.Public.ClientID = os.Getenv(prefix + "_CLIENT_ID")
	}

	if value.Secret == "" {
		value.Secret = os.Getenv(prefix + "_CLIENT_SECRET")
	}

	if id == "gitlab" && value.Public.BaseURL == "https://gitlab.com" && os.Getenv("GITLAB_BASE_URL") != "" {
		value.Public.BaseURL = strings.TrimRight(os.Getenv("GITLAB_BASE_URL"), "/")
	}

	value.Public.Configured = value.Public.ClientID != ""
	value.Public.Connected = value.AccessToken != "" &&
		value.Public.UserName != "" &&
		(value.TokenExpiry.IsZero() ||
			service.Now().Before(value.TokenExpiry) ||
			value.RefreshToken != "")
	value.Public.ReauthorizationRequired = id == "github" && value.AccessToken != "" &&
		!slices.Contains(value.Scopes, "workflow")

	return value, nil
}

func (service *Service) Connections() ([]domain.GitConnection, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	ids, err := service.connectionIDs()
	if err != nil {
		return nil, err
	}

	result := []domain.GitConnection{}
	for _, id := range ids {
		value, err := service.account(id)
		if err != nil {
			return nil, err
		}

		result = append(result, value.Public)
	}

	return result, nil
}

func (service *Service) Configure(input domain.GitConnectionConfig) (domain.GitConnection, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.accountForConfiguration(input)
	if err != nil {
		return domain.GitConnection{}, err
	}

	input.ClientID = strings.TrimSpace(input.ClientID)

	if input.ClientID == "" || strings.ContainsAny(input.ClientID, "\r\n\x00") {
		return value.Public, errors.New("OAuth client ID is required")
	}

	if input.BaseURL == "" && !isForge(input.Kind) {
		input.BaseURL = "https://" + input.Kind + ".com"
	}

	input.BaseURL = strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if isForge(input.Kind) {
		origin, err := netguard.Origin(input.BaseURL)
		if err != nil {
			return value.Public, err
		}

		input.BaseURL = origin
		if value.Public.Configured && value.Public.BaseURL != origin {
			return value.Public, errors.New("create a separate connection for another Git instance")
		}
	}

	address, err := url.Parse(input.BaseURL)
	local := address != nil &&
		(address.Hostname() == "localhost" ||
			address.Hostname() == "127.0.0.1" ||
			address.Hostname() == "::1")
	if err != nil ||
		address.Host == "" ||
		address.User != nil ||
		address.RawQuery != "" ||
		address.Fragment != "" ||
		(address.Scheme != "https" &&
			!(address.Scheme == "http" &&
				local)) ||
		(address.Path != "" &&
			address.Path != "/") {
		return value.Public, errors.New("Git service requires an HTTPS origin")
	}

	if input.Kind == "github" && input.BaseURL != "https://github.com" {
		return value.Public, errors.New("GitHub connection uses github.com")
	}

	if value.Public.ClientID != input.ClientID || value.Public.BaseURL != input.BaseURL {
		value.AccessToken, value.RefreshToken, value.Public.UserName, value.Secret = "", "", "", ""
		value.Public.Connected = false
		value.TokenExpiry, value.Login = time.Time{}, nil
	}

	value.Public.ClientID, value.Public.BaseURL = input.ClientID, input.BaseURL
	value.Public.Name = strings.TrimSpace(input.Name)
	value.Public.PrivateNetwork = isForge(input.Kind) && input.PrivateNetwork
	value.Login = nil

	if input.ClientSecret != "" {
		value.Secret = input.ClientSecret
	}

	value.Public.Configured = true
	delete(service.cache, value.Public.ID)

	return value.Public, service.save(value)
}

func (service *Service) Begin(ctx context.Context, id string) (domain.GitLogin, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account(id)
	if err != nil {
		return domain.GitLogin{}, err
	}

	if !value.Public.Configured {
		return domain.GitLogin{}, errors.New("configure the OAuth app client ID first")
	}

	if isForge(value.Public.Kind) {
		return service.beginBrowser(ctx, value)
	}

	path, scopes := "/login/device/code", "repo read:org workflow"

	if id == "gitlab" {
		path, scopes = "/oauth/authorize_device", "api"
	}

	var response map[string]any
	err = service.Client.request(
		ctx,
		"POST",
		service.Client.authRoot(value)+path,
		"",
		url.Values{
			"client_id": {
				value.Public.ClientID,
			},
			"scope": {
				scopes,
			},
		},
		true,
		&response,
	)
	if err != nil {
		return domain.GitLogin{}, err
	}

	if code := text(response["error"]); code != "" {
		return domain.GitLogin{}, fmt.Errorf("Git login failed: %s", code)
	}

	verification := text(response["verification_uri_complete"])
	if verification == "" {
		verification = text(response["verification_uri"])
	}

	address, err := url.Parse(verification)
	root, _ := url.Parse(service.Client.authRoot(value))
	if err != nil ||
		address.Host != root.Host ||
		address.Scheme != root.Scheme ||
		address.User != nil ||
		text(response["device_code"]) == "" ||
		number(response["expires_in"]) <= 0 {
		return domain.GitLogin{}, errors.New("Git provider returned invalid device authorization")
	}

	interval := max(5, number(response["interval"]))
	login := domain.GitLogin{
		ID:              domain.NewID(),
		ConnectionID:    id,
		Method:          "device",
		VerificationURL: verification,
		UserCode:        text(response["user_code"]),
		Status:          "pending",
		ExpiresAt:       service.Now().Add(time.Duration(number(response["expires_in"])) * time.Second),
		Interval:        interval,
	}

	value.Login = &loginTransaction{
		Public:     login,
		DeviceCode: text(response["device_code"]),
		NextPollAt: service.Now().Add(time.Duration(interval) * time.Second),
	}

	return login, service.save(value)
}

func (service *Service) Poll(ctx context.Context, id, loginID string) (domain.GitLogin, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account(id)
	if err != nil {
		return domain.GitLogin{}, err
	}

	if value.Login == nil || value.Login.Public.ID != loginID {
		return domain.GitLogin{}, errors.New("Git login is no longer current; start again")
	}

	transaction := value.Login
	if transaction.Public.Status != "pending" {
		return transaction.Public, nil
	}

	if !service.Now().Before(transaction.Public.ExpiresAt) {
		if transaction.BrowserFlow != nil && value.Public.UserName == "" {
			value.AccessToken = ""
			value.RefreshToken = ""
			value.TokenExpiry = time.Time{}
		}

		transaction.Public.Status, transaction.Public.Error = "expired", "Git login expired; start again"
		transaction.DeviceCode = ""
		transaction.BrowserFlow = nil
		return transaction.Public, service.save(value)
	}

	if transaction.BrowserFlow != nil {
		if err := service.browserSessionValid(transaction.BrowserFlow.Owner); err != nil {
			return service.rejectBrowser(&value)
		}

		if transaction.BrowserFlow.Verifier != "" {
			return transaction.Public, nil
		}

		if value.AccessToken == "" {
			return service.rejectBrowser(&value)
		}
	}

	if service.Now().Before(transaction.NextPollAt) {
		return transaction.Public, nil
	}

	if transaction.DeviceCode == "" && value.AccessToken != "" {
		transaction.NextPollAt = service.Now().Add(5 * time.Second)
		return service.finishLogin(ctx, &value)
	}

	response, err := service.Client.oauth(
		ctx,
		value,
		url.Values{
			"device_code": {
				transaction.DeviceCode,
			},
			"grant_type": {
				"urn:ietf:params:oauth:grant-type:device_code",
			},
		},
	)
	if err != nil {
		if ctx.Err() != nil {
			return transaction.Public, ctx.Err()
		}
		transaction.Public.Error = "Git login temporarily unavailable; retrying"
		if saveErr := service.save(value); saveErr != nil {
			return transaction.Public, saveErr
		}
		return transaction.Public, err
	}
	if ctx.Err() != nil {
		return transaction.Public, ctx.Err()
	}
	transaction.Public.Error = ""

	transaction.NextPollAt = service.Now().Add(time.Duration(transaction.Public.Interval) * time.Second)

	switch code := text(response["error"]); code {
	case "authorization_pending":
	case "slow_down":
		transaction.Public.Interval = max(transaction.Public.Interval+5, number(response["interval"]))
		transaction.NextPollAt = service.Now().Add(time.Duration(transaction.Public.Interval) * time.Second)
	case "":
		// A new authorization must describe its own grants. Only token refreshes
		// may retain scopes when the provider omits an unchanged scope response.
		value.Scopes = nil
		if err := service.acceptToken(&value, response); err != nil {
			return transaction.Public, err
		}

		// The device code is consumed now. Persist the token before loading the
		// profile so a temporary profile failure can be retried safely.
		transaction.DeviceCode, value.Public.UserName = "", ""

		if err := service.save(value); err != nil {
			return transaction.Public, err
		}

		return service.finishLogin(ctx, &value)
	default:
		transaction.Public.Status, transaction.Public.Error, transaction.DeviceCode = "failed", "Git login was rejected; start again", ""
	}

	return transaction.Public, service.save(value)
}

func (service *Service) finishLogin(ctx context.Context, value *account) (domain.GitLogin, error) {
	var user map[string]any
	if err := service.Client.accountRequest(
		ctx,
		*value,
		"GET",
		service.Client.apiRoot(*value)+"/user",
		nil,
		false,
		&user,
	); err != nil {
		if ctx.Err() == nil {
			value.Login.Public.Error = "Git account identity temporarily unavailable; retrying"
			if saveErr := service.save(*value); saveErr != nil {
				return value.Login.Public, saveErr
			}
		}
		return value.Login.Public, err
	}

	if ctx.Err() != nil {
		return value.Login.Public, ctx.Err()
	}

	if value.Login.BrowserFlow != nil {
		if err := service.browserSessionValid(value.Login.BrowserFlow.Owner); err != nil {
			return service.rejectBrowser(value)
		}
	}

	value.Public.UserName = text(user["login"])

	if value.Public.Kind == "gitlab" {
		value.Public.UserName = text(user["username"])
	}

	if value.Public.UserName == "" {
		return value.Login.Public, errors.New("Git provider returned no account identity")
	}

	if value.Public.Kind == "github" && !slices.Contains(value.Scopes, "workflow") {
		value.Login.Public.Status = "failed"
		value.Login.Public.Error = "GitHub workflow permission was not granted; sign in again and approve workflow access"
		return value.Login.Public, service.save(*value)
	}

	value.Public.Connected, value.Login.Public.Status = true, "connected"
	value.Login.Public.Error = ""
	value.Login.BrowserFlow = nil
	delete(service.cache, value.Public.ID)

	return value.Login.Public, service.save(*value)
}

// Read only persisted public state, without waiting behind remote OAuth I/O.
func (service *Service) PendingLogin(id string) (*domain.GitLogin, error) {
	var value account
	if err := service.Store.Load("git_account", id, &value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if value.Login == nil || value.Login.Public.Status != "pending" {
		return nil, nil
	}
	return &value.Login.Public, nil
}

func (service *Service) Login(id, loginID string) (*domain.GitLogin, error) {
	var value account
	if err := service.Store.Load("git_account", id, &value); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if value.Login == nil || value.Login.Public.ID != loginID {
		return nil, nil
	}
	return &value.Login.Public, nil
}

func (service *Service) EndLogin(id, loginID, status string) error {
	if status != "cancelled" && status != "expired" {
		return errors.New("invalid login final state")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.account(id)
	if err != nil {
		return err
	}
	if value.Login == nil || value.Login.Public.ID != loginID || value.Login.Public.Status != "pending" {
		return nil
	}
	if value.Login.DeviceCode == "" && value.Public.UserName == "" {
		value.AccessToken, value.RefreshToken, value.Scopes = "", "", nil
		value.TokenExpiry = time.Time{}
	}
	value.Login.DeviceCode = ""
	value.Login.BrowserFlow = nil
	value.Login.Public.Status = status
	value.Login.Public.Error = ""
	if status == "cancelled" {
		value.Login.Public.Error = "Git login cancelled; start again"
	}
	if status == "expired" {
		value.Login.Public.Error = "Git login expired; start again"
	}
	return service.save(value)
}

func (service *Service) acceptToken(value *account, response map[string]any) error {
	if text(response["access_token"]) == "" {
		return errors.New("Git provider returned no access token")
	}

	value.AccessToken = text(response["access_token"])
	if scopes, present := response["scope"]; present {
		value.Scopes = strings.FieldsFunc(text(scopes), func(character rune) bool {
			return character == ',' || unicode.IsSpace(character)
		})
		slices.Sort(value.Scopes)
		value.Scopes = slices.Compact(value.Scopes)
	}

	if token := text(response["refresh_token"]); token != "" {
		value.RefreshToken = token
	}

	value.TokenExpiry = time.Time{}

	if seconds := number(response["expires_in"]); seconds > 0 {
		value.TokenExpiry = service.Now().Add(time.Duration(seconds) * time.Second)
	}

	return nil
}

func (service *Service) authenticated(ctx context.Context, id string) (account, error) {
	value, err := service.account(id)
	if err != nil {
		return value, err
	}

	if value.AccessToken == "" || value.Public.UserName == "" {
		return value, errors.New("connect the Git account first")
	}

	if value.TokenExpiry.IsZero() || service.Now().Add(time.Minute).Before(value.TokenExpiry) {
		return value, nil
	}

	if value.RefreshToken == "" {
		return value, errors.New("Git account session expired; reconnect")
	}

	response, err := service.Client.oauth(
		ctx,
		value,
		url.Values{
			"grant_type": {
				"refresh_token",
			},
			"refresh_token": {
				value.RefreshToken,
			},
		},
	)
	if err != nil {
		return value, err
	}

	if text(response["error"]) != "" {
		if code := text(response["error"]); code == "invalid_grant" || code == "invalid_token" || code == "access_denied" {
			value.AccessToken = ""
			value.RefreshToken = ""
			value.Public.Connected = false
			delete(service.cache, id)
			if err := service.save(value); err != nil {
				return value, err
			}
		}

		return value, errors.New("Git account session could not be refreshed; reconnect")
	}

	if err := service.acceptToken(&value, response); err != nil {
		return value, err
	}

	return value, service.save(value)
}

func (service *Service) Token(ctx context.Context, id string) (string, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.authenticated(ctx, id)

	return value.AccessToken, err
}

func (service *Service) Disconnect(id string) error {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account(id)
	if err != nil {
		return err
	}

	value.AccessToken, value.RefreshToken, value.Public.UserName = "", "", ""
	value.Scopes = nil
	value.Public.Connected, value.Login = false, nil
	value.TokenExpiry = time.Time{}
	delete(service.cache, id)

	return service.save(value)
}
