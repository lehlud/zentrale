package providers

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

type streamRequest struct {
	kind   string
	onText func(string)
}

func (request streamRequest) text(value string) {
	if request.onText != nil && value != "" {
		request.onText(value)
	}
}

type streamAccumulator interface {
	consume(map[string]any) (bool, error)
	result() (map[string]any, error)
}

func readResponseStream(body io.Reader) (map[string]any, error) {
	return readProviderStream(body, streamRequest{kind: "openai"})
}

// SSE framing is shared; each protocol assembles its original final envelope.
// Tool replay, signatures and usage accounting use the completed envelope.
func readProviderStream(body io.Reader, request streamRequest) (map[string]any, error) {
	var accumulator streamAccumulator

	switch request.kind {
	case "openai":
		accumulator = &responsesStream{request: request}
	case "anthropic":
		accumulator = newAnthropicStream(request)
	case "gemini":
		accumulator = &geminiStream{request: request}
	default:
		accumulator = newChatStream(request)
	}

	scanner := bufio.NewScanner(io.LimitReader(body, 32*1024*1024+1))
	scanner.Buffer(make([]byte, 64*1024), maximumJSONResponseBytes)

	var data []string
	var eventName string

	consume := func() (bool, error) {
		if len(data) == 0 {
			eventName = ""

			return false, nil
		}

		encoded := strings.Join(data, "\n")
		data = nil

		if encoded == "[DONE]" {
			return true, nil
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(encoded), &event); err != nil || event == nil {
			return false, errors.New("invalid provider stream event")
		}

		if asString(event["type"]) == "" && eventName != "" {
			event["type"] = eventName
		}

		eventName = ""

		if event["error"] != nil || event["type"] == "error" {
			return false, responseFailure(event)
		}

		return accumulator.consume(event)
	}

	for scanner.Scan() {
		line := strings.TrimPrefix(scanner.Text(), "\xef\xbb\xbf")
		if line == "" {
			done, err := consume()
			if err != nil {
				return nil, err
			}

			if done {
				return accumulator.result()
			}
		} else if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		} else if strings.HasPrefix(line, "event:") {
			eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if _, err := consume(); err != nil {
		return nil, err
	}

	return accumulator.result()
}

type responsesStream struct {
	request   streamRequest
	completed map[string]any
}

func (stream *responsesStream) consume(event map[string]any) (bool, error) {
	switch asString(event["type"]) {
	case "response.output_text.delta":
		stream.request.text(asString(event["delta"]))
	case "response.refusal.delta":
		stream.request.text(asString(event["delta"]))

	case "response.completed":
		completed, valid := event["response"].(map[string]any)
		if !valid {
			return false, errors.New("invalid completed provider response")
		}

		stream.completed = completed

		return true, nil

	case "response.failed", "response.incomplete":
		return false, responseFailure(event)
	}

	return false, nil
}

func (stream *responsesStream) result() (map[string]any, error) {
	if stream.completed == nil {
		return nil, errors.New("provider stream ended before completion")
	}

	return stream.completed, nil
}
