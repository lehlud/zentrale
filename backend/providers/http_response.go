package providers

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

const maximumJSONResponseBytes = 16 * 1024 * 1024

type HTTPError struct {
	Status int
	Code   string
}

func (failure *HTTPError) Error() string {
	return fmt.Sprintf("provider HTTP %d (check model, credentials and endpoint)", failure.Status)
}

func decodeProviderResponse(response *http.Response, streaming ...streamRequest) (map[string]any, error) {
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code string `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&envelope)
		if envelope.Error.Code == "" {
			envelope.Error.Code = envelope.Error.Type
		}
		return nil, &HTTPError{Status: response.StatusCode, Code: envelope.Error.Code}
	}
	body := bufio.NewReader(response.Body)
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	// Some account endpoints and gateways return SSE with a missing or incorrect
	// Content-Type. Recognize the framing before attempting to decode plain JSON.
	if strings.EqualFold(mediaType, "text/event-stream") || hasEventStreamPrefix(body) {
		stream := streamRequest{kind: "openai"}
		if len(streaming) > 0 {
			stream = streaming[0]
		}
		return readProviderStream(body, stream)
	}
	data, err := io.ReadAll(io.LimitReader(body, maximumJSONResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maximumJSONResponseBytes {
		return nil, errors.New("provider response too large")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("invalid provider JSON: %w", err)
	}
	return result, nil
}

func hasEventStreamPrefix(body *bufio.Reader) bool {
	if prefix, _ := body.Peek(3); string(prefix) == "\xef\xbb\xbf" {
		_, _ = body.Discard(3)
	}
	for range 1024 {
		prefix, _ := body.Peek(1)
		if len(prefix) == 0 || !strings.ContainsRune(" \t\r\n", rune(prefix[0])) {
			break
		}
		_, _ = body.Discard(1)
	}
	prefix, _ := body.Peek(6)
	text := string(prefix)
	return strings.HasPrefix(text, "event:") || strings.HasPrefix(text, "data:") || strings.HasPrefix(text, ":")
}
