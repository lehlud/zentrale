// Package modelcatalog discovers models from the configured provider account.
package modelcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

const maximumCatalogBytes = 16 * 1024 * 1024

type Client struct {
	HTTP *http.Client
}

func New() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (client *Client) List(ctx context.Context, provider domain.Provider) ([]domain.LanguageModel, error) {
	models := make(map[string]domain.LanguageModel)
	endpoint := "/models"

	if provider.Kind == "anthropic" {
		endpoint += "?limit=1000"
	}

	if provider.AuthMode == "device" && provider.Kind == "openai" {
		endpoint += "?client_version=1.0.0"
	}

	for page := 0; page < 100; page++ {
		response, err := client.get(ctx, provider, endpoint)
		if err != nil {
			return nil, err
		}

		entries := array(response["data"])

		if provider.Kind == "gemini" {
			entries = array(response["models"])
		}

		if provider.Kind == "openai" && provider.AuthMode == "device" {
			entries = array(response["models"])
		}

		for _, entry := range entries {
			model, include := decodeModel(provider, object(entry))
			if include {
				models[model.ID] = model
			}
		}

		endpoint = nextPage(provider, response)
		if endpoint == "" {
			break
		}

		if page == 99 {
			return nil, errors.New("model catalog exceeded its pagination limit")
		}
	}

	result := make([]domain.LanguageModel, 0, len(models))

	for _, model := range models {
		result = append(result, model)
	}

	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})

	if len(result) == 0 {
		return nil, errors.New("provider returned no language models")
	}

	return result, nil
}

func (client *Client) get(
	ctx context.Context,
	provider domain.Provider,
	endpoint string,
) (map[string]any, error) {
	address := strings.TrimRight(provider.BaseURL, "/") + endpoint
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}

	credential := provider.Credential

	if provider.OAuth != nil {
		credential = provider.OAuth.AccessToken
	}

	switch provider.Kind {
	case "anthropic":
		request.Header.Set("x-api-key", credential)
		request.Header.Set("anthropic-version", "2023-06-01")
	case "gemini":
		request.Header.Set("x-goog-api-key", credential)
	default:
		if credential != "" {
			request.Header.Set("Authorization", "Bearer "+credential)
		}
	}

	if provider.Kind == "openai" && provider.OAuth != nil {
		request.Header.Set("originator", "zentrale")

		if provider.OAuth.AccountID != "" {
			request.Header.Set("ChatGPT-Account-Id", provider.OAuth.AccountID)
		}
	}

	response, err := client.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("model discovery returned HTTP %d", response.StatusCode)
	}

	encoded, err := io.ReadAll(io.LimitReader(response.Body, maximumCatalogBytes+1))
	if err != nil {
		return nil, err
	}

	if len(encoded) > maximumCatalogBytes {
		return nil, errors.New("model catalog is too large")
	}

	var result map[string]any
	if err := json.Unmarshal(encoded, &result); err != nil {
		return nil, err
	}

	return result, nil
}

func nextPage(provider domain.Provider, response map[string]any) string {
	if provider.Kind == "anthropic" && response["has_more"] == true {
		cursor := text(response["last_id"])
		if cursor != "" {
			return "/models?limit=1000&after_id=" + url.QueryEscape(cursor)
		}
	}

	if provider.Kind == "gemini" {
		cursor := text(response["nextPageToken"])
		if cursor != "" {
			return "/models?pageToken=" + url.QueryEscape(cursor)
		}
	}

	return ""
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func array(value any) []any {
	result, _ := value.([]any)
	return result
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}
