// Package providerlogin implements native provider consent and token refresh.
package providerlogin

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

const openAIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

type Client struct {
	HTTP          *http.Client
	OpenAIAuthURL string
	OpenRouterURL string
}

type Transaction struct {
	Session               domain.ProviderLogin `json:"session"`
	NextPollAt            time.Time            `json:"nextPollAt"`
	DeviceAuthorizationID string               `json:"deviceAuthorizationId,omitempty"`
	CodeVerifier          string               `json:"codeVerifier,omitempty"`
}

func New() *Client {
	return &Client{
		HTTP:          &http.Client{Timeout: 30 * time.Second},
		OpenAIAuthURL: "https://auth.openai.com",
		OpenRouterURL: "https://openrouter.ai",
	}
}

func SupportedMethods(kind string) []string {
	switch kind {
	case "openai":
		return []string{"api", "device"}
	case "openrouter":
		return []string{"api", "browser"}
	default:
		return []string{"api"}
	}
}

func (client *Client) Begin(ctx context.Context, provider domain.Provider) (Transaction, error) {
	switch {
	case provider.Kind == "openai" && provider.AuthMode == "device":
		return client.beginOpenAI(ctx, provider.ID)
	case provider.Kind == "openrouter" && provider.AuthMode == "browser":
		return client.beginOpenRouter(provider.ID), nil
	default:
		return Transaction{}, errors.New("this provider does not offer the selected native login method")
	}
}

func (client *Client) post(ctx context.Context, address string, body any, form bool) (map[string]any, int, error) {
	var encoded []byte
	contentType := "application/json"
	if form {
		values, valid := body.(url.Values)
		if !valid {
			return nil, 0, errors.New("OAuth form encoding requires url.Values")
		}
		encoded = []byte(values.Encode())
		contentType = "application/x-www-form-urlencoded"
	} else {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(data) > 1024*1024 {
		return nil, response.StatusCode, errors.New("OAuth response is too large")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return map[string]any{}, response.StatusCode, nil
		}
		return nil, response.StatusCode, errors.New("provider returned invalid OAuth JSON")
	}
	return result, response.StatusCode, nil
}

func randomCode() string {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data[:])
}

func challenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func tokenError(status int) error {
	return fmt.Errorf("provider login returned HTTP %d; restart login or check account access", status)
}

func normalizeAuthRoot(address string) string {
	return strings.TrimRight(address, "/")
}
