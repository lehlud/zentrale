// Package providers translates a small, provider-independent inference interface.
// No provider gets direct access to the filesystem, network tools or credentials.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providerlogin"
)

type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
}

type Call struct {
	ID        string
	Name      string
	Arguments map[string]any
}

type Result struct {
	Text  string
	Wire  string
	Phase string
	Calls []Call

	InputTokens          int
	OutputTokens         int
	CachedTokens         int
	CacheWriteTokens     int
	CacheWriteLongTokens int

	CacheReadKnown  bool
	CacheWriteKnown bool
	Cost            *float64
	UsageKnown      bool
}

type Request struct {
	Config domain.Provider
	Model  domain.LanguageModel

	System   string
	Messages []domain.Message
	Tools    []Tool
	CacheKey string

	// OnText publishes visible assistant text in provider order.
	// Reasoning and partial tool arguments stay within the provider adapter.
	OnText func(string)
}

type Client interface {
	Complete(context.Context, Request) (Result, error)
}

type HTTPClient struct {
	HTTP      *http.Client
	waitRetry func(context.Context, time.Duration) error
}

func New() *HTTPClient {
	return &HTTPClient{
		HTTP: &http.Client{Timeout: 3 * time.Minute},
	}
}

var defaults = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"anthropic":  "https://api.anthropic.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"opencode":   "https://opencode.ai/zen/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"gemini":     "https://generativelanguage.googleapis.com/v1beta",
}

func Validate(configuration *domain.Provider) error {
	switch configuration.Kind {
	case "openai", "anthropic", "openrouter", "opencode", "mistral", "litellm", "gemini":
	default:
		return errors.New("unsupported provider")
	}

	if configuration.AuthMode == "" {
		configuration.AuthMode = "api"
	}

	supported := false

	for _, method := range providerlogin.SupportedMethods(configuration.Kind) {
		supported = supported || method == configuration.AuthMode
	}

	if !supported {
		return errors.New("unsupported provider authentication method")
	}

	if configuration.BaseURL == "" {
		if configuration.AuthMode == "device" && configuration.Kind == "openai" {
			configuration.BaseURL = "https://chatgpt.com/backend-api/codex"
		}

		if configuration.BaseURL == "" {
			configuration.BaseURL = defaults[configuration.Kind]
		}
	}

	if configuration.BillingMode == "" {
		configuration.BillingMode = "metered"

		if configuration.AuthMode == "device" {
			configuration.BillingMode = "fixed"
		}
	}

	if configuration.BillingMode != "metered" && configuration.BillingMode != "fixed" {
		return errors.New("billing mode must be metered or fixed")
	}

	u, err := url.Parse(configuration.BaseURL)
	if err != nil ||
		u.Host == "" ||
		u.User != nil ||
		u.RawQuery != "" ||
		u.Fragment != "" ||
		(u.Scheme != "https" &&
			u.Scheme != "http") {
		return errors.New("invalid provider URL")
	}

	if u.Scheme == "http" &&
		u.Hostname() != "localhost" &&
		u.Hostname() != "127.0.0.1" &&
		u.Hostname() != "::1" {
		return errors.New("remote provider URL requires HTTPS")
	}

	return nil
}

func (client *HTTPClient) post(
	ctx context.Context,
	configuration domain.Provider,
	endpoint string,
	body any,
	textCallbacks ...func(string),
) (map[string]any, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		"POST",
		strings.TrimRight(configuration.BaseURL, "/")+endpoint,
		bytes.NewReader(encoded),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	object, isObject := body.(map[string]any)
	if (isObject && object["stream"] == true) || strings.HasSuffix(endpoint, "?alt=sse") {
		req.Header.Set("Accept", "text/event-stream")
	}

	credential := configuration.Credential

	if configuration.OAuth != nil {
		credential = configuration.OAuth.AccessToken
	}

	if configuration.Kind == "anthropic" {
		req.Header.Set("x-api-key", credential)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if configuration.Kind == "gemini" {
		req.Header.Set("x-goog-api-key", credential)
	} else if credential != "" {
		req.Header.Set("Authorization", "Bearer "+credential)
	}

	if configuration.Kind == "openai" && configuration.OAuth != nil {
		req.Header.Set("originator", "zentrale")

		if configuration.OAuth.AccountID != "" {
			req.Header.Set("ChatGPT-Account-Id", configuration.OAuth.AccountID)
		}
	}
	defer req.Body.Close()

	stream := streamRequest{kind: configuration.Kind}

	if len(textCallbacks) > 0 {
		stream.onText = textCallbacks[0]
	}

	result, failure := client.postWithRetry(req, stream)

	return result, sanitizeResponseFailure(failure, configuration)
}

func sanitizeResponseFailure(failure error, configuration domain.Provider) error {
	var responseError *ResponseError
	if errors.As(failure, &responseError) {
		secrets := []string{configuration.Credential}

		if configuration.OAuth != nil {
			secrets = append(secrets, configuration.OAuth.AccessToken, configuration.OAuth.RefreshToken)
		}

		redact := func(value string) string {
			for _, secret := range secrets {
				if secret != "" {
					value = strings.ReplaceAll(value, secret, "[redacted]")
				}
			}

			return value
		}

		// Redact before shortening diagnostics, including secrets crossing the
		// display limit. Truncating first could expose a credential prefix.
		responseError.Code = diagnosticField(redact(responseError.Code), 128)
		responseError.Message = diagnosticField(redact(responseError.Message), 2000)
		responseError.Param = diagnosticField(redact(responseError.Param), 256)
		responseError.Reason = diagnosticField(redact(responseError.Reason), 128)
	}

	return failure
}

func (client *HTTPClient) Complete(ctx context.Context, request Request) (result Result, err error) {
	defer func() { err = sanitizeResponseFailure(err, request.Config) }()

	if request.Model.ID == "" || strings.ContainsAny(request.Model.ID, "?#\r\n") {
		return Result{}, errors.New("select an available language model")
	}

	if err := Validate(&request.Config); err != nil {
		return Result{}, err
	}

	switch request.Config.Kind {
	case "openai":
		return client.responses(ctx, request)
	case "anthropic":
		return client.anthropic(ctx, request)
	case "gemini":
		return client.gemini(ctx, request)
	default:
		return client.chat(ctx, request)
	}
}

func asObject(item any) map[string]any {
	message, _ := item.(map[string]any)
	if message == nil {
		return map[string]any{}
	}

	return message
}

func asArray(item any) []any {
	s, _ := item.([]any)

	return s
}

func asString(item any) string {
	s, _ := item.(string)

	return s
}

func asInt(item any) int {
	f, _ := item.(float64)

	return int(f)
}

func encodeWire(item any) string {
	b, _ := json.Marshal(item)

	return string(b)
}

func parseToolArguments(item any) map[string]any {
	if s, ok := item.(string); ok {
		var message map[string]any
		if json.Unmarshal([]byte(s), &message) == nil {
			return message
		}

		return map[string]any{"_invalid": s}
	}

	return asObject(item)
}

// Harness events are explicitly identified as system-generated content, while
// using the conversation roles accepted by each provider's inference protocol.
func conversationRole(role string) string {
	if role == "event" || role == "context" {
		return "user"
	}

	return role
}
