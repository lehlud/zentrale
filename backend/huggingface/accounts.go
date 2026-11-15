// Package huggingface brokers read-only Hub access without exporting account
// credentials to agents or remote execution environments.
package huggingface

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

const origin = "https://huggingface.co"
const credentialName = "huggingface"

var tokenPattern = regexp.MustCompile(`^hf_[A-Za-z0-9]{8,512}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$`)

type account struct {
	Public domain.HuggingFaceConnection `json:"public"`
	Token  string                       `json:"token"`
	App    oauthApp                     `json:"app,omitempty"`
	OAuth  *oauthAccount                `json:"oauth,omitempty"`
	Login  *oauthLogin                  `json:"login,omitempty"`
}

type Service struct {
	Store  *datastore.Instance
	Client *http.Client

	mu          sync.Mutex
	refreshMu   sync.Mutex
	loginCancel context.CancelFunc
	closed      bool
	epoch       uint64
	nextRequest uint64
	active      map[uint64]context.CancelFunc
}

func New(store *datastore.Instance) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second

	return &Service{
		Store:  store,
		Client: &http.Client{Transport: transport, CheckRedirect: checkRedirect},
		active: make(map[uint64]context.CancelFunc),
	}
}

func (service *Service) account() (account, error) {
	encoded, err := service.Store.Credential(credentialName)
	if errors.Is(err, sql.ErrNoRows) {
		return account{}, nil
	}

	if err != nil {
		return account{}, err
	}

	var value account
	if json.Unmarshal(encoded, &value) != nil {
		return account{}, errors.New("invalid stored Hugging Face credential")
	}

	return value, nil
}

func (service *Service) Status() (domain.HuggingFaceConnection, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account()
	if err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	if value.Login != nil && (!time.Now().Before(value.Login.Flow.ExpiresAt) ||
		!service.sessionValid(value.Login.Flow.Owner)) {
		value.Login = nil
		if err := service.saveAccount(value); err != nil {
			return domain.HuggingFaceConnection{}, err
		}

		service.cancelLogin()
	}

	return connectionMetadata(value), nil
}

func (service *Service) Close() {
	service.mu.Lock()
	service.closed = true
	service.cancelLogin()
	service.invalidateRequests()
	service.mu.Unlock()

	service.Client.CloseIdleConnections()
}

func (service *Service) Connect(ctx context.Context, token string) (domain.HuggingFaceConnection, error) {
	token = strings.TrimSpace(token)
	if !tokenPattern.MatchString(token) {
		return domain.HuggingFaceConnection{}, errors.New("a Hugging Face user access token is required")
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return domain.HuggingFaceConnection{}, errors.New("Hugging Face connection service is closed")
	}

	epoch := service.epoch
	ctx, release := service.requestContext(ctx)
	service.mu.Unlock()
	defer release()

	response, err := service.get(ctx, origin+"/api/whoami-v2", token)
	if err != nil {
		return domain.HuggingFaceConnection{}, err
	}
	defer response.Body.Close()

	var identity struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := decodeResponse(response.Body, &identity, 64*1024); err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	if identity.Type != "user" || !namePattern.MatchString(identity.Name) {
		return domain.HuggingFaceConnection{}, errors.New("Hugging Face did not return a valid user account")
	}

	value := account{
		Public: domain.HuggingFaceConnection{
			Connected: true,
			UserName:  identity.Name,
			CheckedAt: time.Now().UTC(),
			AuthMode:  "token",
		},
		Token: token,
	}
	service.mu.Lock()
	defer service.mu.Unlock()

	if service.epoch != epoch || ctx.Err() != nil {
		return domain.HuggingFaceConnection{}, errors.New("Hugging Face connection changed; please connect again")
	}

	current, err := service.account()
	if err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	value.App = current.App
	if err := service.saveAccount(value); err != nil {
		return domain.HuggingFaceConnection{}, err
	}

	service.cancelLogin()
	service.invalidateRequests()
	return connectionMetadata(value), nil
}

func (service *Service) Disconnect() error {
	service.mu.Lock()
	defer service.mu.Unlock()

	value, err := service.account()
	if err != nil {
		return err
	}

	if err := service.saveAccount(account{App: value.App}); err != nil {
		return err
	}

	service.cancelLogin()
	service.invalidateRequests()
	return nil
}

// The caller holds mu. Cancellation never waits for network activity.
func (service *Service) invalidateRequests() {
	service.epoch++
	for _, cancel := range service.active {
		cancel()
	}
}

// The caller holds mu; release is idempotent and called without mu held.
func (service *Service) requestContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	service.nextRequest++
	id := service.nextRequest
	service.active[id] = cancel

	var once sync.Once

	release := func() {
		once.Do(func() {
			cancel()

			service.mu.Lock()
			delete(service.active, id)
			service.mu.Unlock()
		})
	}

	return ctx, release
}

func (service *Service) beginRequest(ctx context.Context) (context.Context, string, func(), error) {
	if err := service.refreshOAuth(ctx); err != nil {
		return nil, "", nil, err
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.closed {
		return nil, "", nil, errors.New("Hugging Face connection service is closed")
	}

	value, err := service.account()
	if err != nil {
		return nil, "", nil, err
	}

	ctx, release := service.requestContext(ctx)
	return ctx, value.Token, release, nil
}

func checkRedirect(request *http.Request, previous []*http.Request) error {
	host := request.URL.Hostname()
	allowed := host == "huggingface.co" || strings.HasSuffix(host, ".huggingface.co") ||
		host == "hf.co" || strings.HasSuffix(host, ".hf.co")
	if len(previous) >= 6 || !allowed || request.URL.Scheme != "https" ||
		request.URL.User != nil || (request.URL.Port() != "" && request.URL.Port() != "443") {
		return errors.New("Hugging Face redirect is not an approved HTTPS Hub/CDN address")
	}

	if request.URL.Host != "huggingface.co" {
		request.Header.Del("Authorization")
	}

	request.Header.Del("Cookie")
	request.Header.Del("Proxy-Authorization")
	return nil
}

func (service *Service) get(ctx context.Context, address, token string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errors.New("invalid Hugging Face request")
	}

	request.Header.Set("User-Agent", "Zentrale-HuggingFace/1")
	request.Header.Set("Accept-Encoding", "identity")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := service.Client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// URL errors can contain signed CDN queries. Bodies can echo tokens.
		return nil, errors.New("Hugging Face request failed; check connectivity and retry")
	}

	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return nil, fmt.Errorf("Hugging Face access unavailable (HTTP %d): check the account token, dataset/file/revision and granted private/gated permissions; accept required terms or request access on Hugging Face yourself", response.StatusCode)
	case http.StatusTooManyRequests:
		return nil, errors.New("Hugging Face rate limit reached; wait before retrying")
	default:
		return nil, fmt.Errorf("Hugging Face request failed (HTTP %d)", response.StatusCode)
	}
}

func decodeResponse(body io.Reader, target any, limit int64) error {
	encoded, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil || int64(len(encoded)) > limit || json.Unmarshal(encoded, target) != nil {
		return errors.New("invalid or oversized Hugging Face response")
	}

	return nil
}
