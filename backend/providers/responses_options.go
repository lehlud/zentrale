package providers

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func responsesOwner(request Request) [32]byte {
	account := request.Config.Credential
	if request.Config.OAuth != nil {
		account = request.Config.OAuth.AccountID + ":" + request.Config.OAuth.AccessToken
	}
	encoded, _ := json.Marshal([]string{request.Config.ID, request.Config.BaseURL, request.Config.AuthMode, account, request.Model.ID})
	return sha256.Sum256(encoded)
}

func officialResponses(request Request) bool {
	address, _ := url.Parse(request.Config.BaseURL)
	return address != nil && address.Scheme == "https" && (address.Host == "api.openai.com" || address.Host == "chatgpt.com")
}

func modernResponsesModel(id string) bool {
	return strings.HasPrefix(id, "gpt-6") || strings.HasPrefix(id, "gpt-5.6")
}

func (client *HTTPClient) featureDisabled(request Request, feature string) bool {
	client.featureMu.Lock()
	defer client.featureMu.Unlock()
	return time.Now().Before(client.disabledFeatures[responsesOwner(request)][feature])
}

func (client *HTTPClient) disableFeature(request Request, feature string) {
	client.featureMu.Lock()
	defer client.featureMu.Unlock()
	if client.disabledFeatures == nil || len(client.disabledFeatures) >= 256 {
		client.disabledFeatures = map[[32]byte]map[string]time.Time{}
	}
	owner := responsesOwner(request)
	if client.disabledFeatures[owner] == nil {
		client.disabledFeatures[owner] = map[string]time.Time{}
	}
	client.disabledFeatures[owner][feature] = time.Now().Add(15 * time.Minute)
}

func (client *HTTPClient) useResponsesSocket(request Request) bool {
	// A custom RoundTripper may provide isolation, replay or test routing. A
	// socket must never silently bypass that transport's network boundary.
	if client.HTTP.Transport != nil {
		if _, standard := client.HTTP.Transport.(*http.Transport); !standard {
			return false
		}
	}
	mode := request.Config.ResponsesTransport
	return mode != "http" && (mode == "websocket" || officialResponses(request)) && !client.featureDisabled(request, "websocket")
}

func (client *HTTPClient) useResponsesBreakpoints(request Request) bool {
	mode := request.Config.PromptCacheMode
	public := request.Config.AuthMode == "api" && officialResponses(request) && strings.Contains(request.Config.BaseURL, "api.openai.com")
	return mode != "off" && (mode == "explicit" || public && modernResponsesModel(request.Model.ID)) && !client.featureDisabled(request, "cache")
}

// Only the first two eligible messages are marked, so appending conversation
// never removes an old marker and rewrites the prefix. Implicit caching remains
// enabled for the changing tail. Native reasoning/checkpoints stay untouched.
func addResponsesBreakpoints(body map[string]any) {
	marked := 0
	for _, raw := range asArray(body["input"]) {
		item := asObject(raw)
		role := asString(item["role"])
		if role != "user" && role != "developer" {
			continue
		}
		if text, ok := item["content"].(string); ok {
			item["content"] = []any{map[string]any{"type": "input_text", "text": text}}
		}
		for _, rawPart := range asArray(item["content"]) {
			part := asObject(rawPart)
			if asString(part["type"]) != "input_text" {
				continue
			}
			part["prompt_cache_breakpoint"] = map[string]any{"mode": "explicit"}
			marked++
			break
		}
		if marked == 2 {
			break
		}
	}
	if marked > 0 {
		body["prompt_cache_options"] = map[string]any{"mode": "implicit"}
	}
}

func rejectedCacheOption(failure error) bool {
	var httpFailure *HTTPError
	var responseFailure *ResponseError
	param := ""
	if errors.As(failure, &httpFailure) && (httpFailure.Status == 400 || httpFailure.Status == 422) {
		switch httpFailure.Code {
		case "", "unsupported_parameter", "invalid_request_error", "unsupported_value", "unknown_parameter":
			param = httpFailure.Param
		}
	}
	if errors.As(failure, &responseFailure) {
		switch responseFailure.Code {
		case "", "unsupported_parameter", "invalid_request_error", "unsupported_value", "unknown_parameter":
			param = responseFailure.Param
		}
	}
	return strings.Contains(param, "prompt_cache_options") || strings.Contains(param, "prompt_cache_breakpoint")
}
