// Package hosted invokes inference on user-configured serverless GPU accounts.
package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/netguard"
)

const ReplicateURL = "https://api.replicate.com/v1"
const FalURL = "https://fal.run"
const oldFalURL = "https://queue.fal.run"
const Timeout = 120 * time.Second

// Includes cold queue time and a Modal gateway result lookup. The GPU
// function's own execution timeout is configured separately on its runtime.
const ModalTimeout = 4 * time.Minute
const MaxTimeout = 10 * time.Minute
const MaxInput = 128 * 1024
const maxResponse = 512 * 1024

var slug = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
var versionID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var modalAttemptToken = regexp.MustCompile(`^[A-Za-z0-9_./+=-]+$`)
var errQueueConnection = errors.New("hosted inference connection failed")

type Client struct {
	http         *http.Client
	pollInterval time.Duration
	store        *datastore.Instance
	owner        string
	invokeModal  func(context.Context, domain.Provider, map[string]any) (json.RawMessage, error)
}

type statusError struct {
	provider string
	status   int
}

func (err statusError) Error() string {
	return fmt.Sprintf("%s inference returned HTTP %d", err.provider, err.status)
}

func New() *Client {
	client := netguard.HTTPClient(false)
	client.Timeout = 0
	// Run and cancel supply their own deadlines. Synchronous requests stay
	// open during scheduling, startup and inference.
	client.Transport.(*http.Transport).ResponseHeaderTimeout = 0
	return &Client{http: client, pollInterval: time.Second, invokeModal: runModalFunction}
}

func NewWithPredictionStore(store *datastore.Instance) *Client {
	client := New()
	client.store, client.owner = store, domain.NewID()
	return client
}

func (client *Client) Close() { client.http.CloseIdleConnections() }

func Kind(kind string) bool {
	return kind == "modal" || kind == "replicate" || kind == "fal"
}

func ValidateProvider(provider *domain.Provider) error {
	provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
	switch provider.Kind {
	case "replicate":
		if provider.BaseURL != ReplicateURL {
			return errors.New("Replicate requires its official API URL")
		}
	case "fal":
		if provider.BaseURL != FalURL && provider.BaseURL != oldFalURL {
			return errors.New("fal requires its official inference API URL")
		}
		provider.BaseURL = FalURL
	case "modal":
		if strings.HasPrefix(provider.BaseURL, "modal://") {
			_, _, _, err := modalDestination(provider.BaseURL)
			if err != nil {
				return err
			}
			if provider.Credential != "" {
				if _, _, ok := modalAPIToken(provider.Credential); !ok {
					return errors.New("a Modal API token needs an ak- token ID and an as- token secret")
				}
			}
			return nil
		}
		if strings.HasPrefix(strings.TrimSpace(provider.Credential), "ak-") {
			return errors.New("use a Modal workspace proxy token for Web Functions, or select an App and Function for API tokens")
		}
		address, err := url.Parse(provider.BaseURL)
		if err != nil || address.Scheme != "https" || address.Port() != "" || address.User != nil ||
			address.ForceQuery || address.RawQuery != "" || address.Fragment != "" ||
			!strings.HasSuffix(strings.ToLower(address.Hostname()), ".modal.run") ||
			(address.Path != "" && !route(address.EscapedPath()[1:], 1, 8)) {
			return errors.New("Modal provider URL must be a Modal HTTPS Web Function endpoint (.modal.run)")
		}
	}
	return nil
}

func route(value string, minimum, maximum int) bool {
	parts := strings.Split(value, "/")
	if len(parts) < minimum || len(parts) > maximum {
		return false
	}
	for _, part := range parts {
		if !slug.MatchString(part) {
			return false
		}
	}
	return true
}

func ValidateTarget(kind, target string) error {
	switch kind {
	case "modal":
		if target == "" {
			return nil // Only the endpoint saved by the user is callable.
		}
	case "fal":
		if route(target, 2, 8) {
			return nil
		}
	case "replicate":
		if deployment, ok := strings.CutPrefix(target, "deployment:"); ok {
			if route(deployment, 2, 2) {
				return nil
			}
		} else {
			model, version, pinned := strings.Cut(target, ":")
			if route(model, 2, 2) && (!pinned || versionID.MatchString(version)) {
				return nil
			}
		}
	}
	return errors.New("invalid model target for the configured inference provider")
}

// Run submits once. All identifiers returned by the provider are validated
// before use. Poll and cancel addresses stay on the original provider.
func (client *Client) Run(ctx context.Context, provider domain.Provider, target string, input map[string]any, timeout time.Duration) (json.RawMessage, error) {
	if err := ValidateProvider(&provider); err != nil {
		return nil, err
	}
	if err := ValidateTarget(provider.Kind, target); err != nil {
		return nil, err
	}
	if timeout < 5*time.Second || timeout > MaxTimeout || (provider.Kind == "modal" && timeout > ModalTimeout) {
		return nil, errors.New("invalid hosted inference timeout for this provider")
	}
	if strings.TrimSpace(provider.Credential) == "" {
		return nil, errors.New("connect the inference provider with its API key first")
	}
	body, err := json.Marshal(input)
	if err != nil || input == nil || len(body) > MaxInput {
		return nil, errors.New("inference input must be a JSON object of at most 128 KiB")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var result json.RawMessage
	switch provider.Kind {
	case "modal":
		if strings.HasPrefix(provider.BaseURL, "modal://") {
			result, err = client.invokeModal(ctx, provider, input)
		} else {
			deadline, _ := ctx.Deadline()
			err = client.request(ctx, provider, http.MethodPost, "", body, map[string]string{
				"X-Zentrale-Inference-Deadline": fmt.Sprint(deadline.Unix()),
			}, &result)
		}
	case "replicate":
		result, err = client.replicate(ctx, provider, target, body)
	case "fal":
		// fal propagates a disconnected synchronous request to its runner.
		// A queue submission would survive a Zentrale crash, including a crash
		// before the provider's job ID reaches this process.
		err = client.request(ctx, provider, http.MethodPost, "/"+target, body, map[string]string{
			"X-Fal-Request-Start-Timeout": fmt.Sprint(min(180, int(timeout.Seconds()))),
			"X-Fal-Store-IO":              "0",
			"X-App-Fal-Disable-Fallback":  "true",
		}, &result)
	}
	if err != nil {
		return nil, err
	}
	// A custom endpoint may accidentally echo the authorization header.
	credential, _ := json.Marshal(provider.Credential)
	result = bytes.ReplaceAll(result, credential[1:len(credential)-1], []byte("[redacted]"))
	if provider.Kind == "modal" {
		if id, secret, found := strings.Cut(provider.Credential, "."); found && id != "" && secret != "" {
			for _, part := range []string{id, secret} {
				encoded, _ := json.Marshal(part)
				result = bytes.ReplaceAll(result, encoded[1:len(encoded)-1], []byte("[redacted]"))
			}
		}
	}
	if !json.Valid(result) {
		return nil, errors.New("inference provider returned no valid result")
	}
	return result, nil
}

func (client *Client) request(ctx context.Context, provider domain.Provider, method, path string, body []byte, headers map[string]string, result any) error {
	request, err := http.NewRequestWithContext(ctx, method, provider.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("could not build hosted inference request")
	}
	scheme := "Bearer"
	if provider.Kind == "fal" {
		scheme = "Key"
	}
	request.Header.Set("Authorization", scheme+" "+provider.Credential)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	httpClient := client.http
	if provider.Kind == "modal" {
		modalClient := *httpClient
		modalClient.CheckRedirect = modalResultRedirect
		httpClient = &modalClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s: %w", provider.Kind, errQueueConnection)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return statusError{provider.Kind, response.StatusCode}
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || json.Unmarshal(data, result) != nil {
		return fmt.Errorf("invalid or oversized %s inference response", provider.Kind)
	}
	return nil
}

func (client *Client) getQueue(ctx context.Context, provider domain.Provider, path string, result any) error {
	for attempt := 1; ; attempt++ {
		err := client.request(ctx, provider, http.MethodGet, path, nil, nil, result)
		var status statusError
		retry := errors.Is(err, errQueueConnection) || (errors.As(err, &status) &&
			(status.status == http.StatusBadGateway || status.status == http.StatusServiceUnavailable || status.status == http.StatusGatewayTimeout))
		if !retry || attempt == 3 {
			return err
		}
		if err := client.wait(ctx); err != nil {
			return err
		}
	}
}

func modalResultRedirect(next *http.Request, previous []*http.Request) error {
	// A delayed Web Function returns a 303 for the existing result. Never
	// resubmit the POST, change endpoint, or forward the proxy token elsewhere.
	if len(previous) != 1 || previous[0].Method != http.MethodPost || next.Method != http.MethodGet ||
		next.Response == nil || next.Response.StatusCode != http.StatusSeeOther {
		return http.ErrUseLastResponse
	}
	start := previous[0].URL
	if next.URL.Scheme != start.Scheme || next.URL.Host != start.Host || next.URL.User != nil ||
		next.URL.Fragment != "" || strings.TrimSuffix(next.URL.EscapedPath(), "/") != strings.TrimSuffix(start.EscapedPath(), "/") {
		return http.ErrUseLastResponse
	}
	query, err := url.ParseQuery(next.URL.RawQuery)
	receipt := query["__modal_attempt_token"]
	if err != nil || len(query) != 1 || len(receipt) != 1 || len(receipt[0]) > 4096 || !modalAttemptToken.MatchString(receipt[0]) {
		return http.ErrUseLastResponse
	}
	return nil
}

func (client *Client) wait(ctx context.Context) error {
	timer := time.NewTimer(client.pollInterval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
