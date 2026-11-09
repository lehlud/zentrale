package providers

import (
	"encoding/json"
	"errors"
)

type anthropicStream struct {
	request streamRequest

	message map[string]any
	blocks  map[int]map[string]any
	inputs  map[int]string
	open    map[int]bool

	stopped bool
}

func newAnthropicStream(request streamRequest) *anthropicStream {
	return &anthropicStream{
		request: request,
		message: map[string]any{},
		blocks:  map[int]map[string]any{},

		inputs: map[int]string{},
		open:   map[int]bool{},
	}
}

func (stream *anthropicStream) consume(event map[string]any) (bool, error) {
	index := asInt(event["index"])

	switch asString(event["type"]) {
	case "message_start":
		stream.message = asObject(event["message"])

	case "content_block_start":
		stream.blocks[index] = asObject(event["content_block"])
		stream.open[index] = true

		if stream.blocks[index]["type"] == "text" {
			stream.request.text(asString(stream.blocks[index]["text"]))
		}

	case "content_block_delta":
		block := stream.blocks[index]
		if block == nil {
			return false, errors.New("provider streamed a missing content block")
		}

		delta := asObject(event["delta"])

		switch asString(delta["type"]) {
		case "text_delta":
			text := asString(delta["text"])
			block["text"] = asString(block["text"]) + text
			stream.request.text(text)
		case "thinking_delta":
			block["thinking"] = asString(block["thinking"]) + asString(delta["thinking"])
		case "signature_delta":
			block["signature"] = asString(block["signature"]) + asString(delta["signature"])
		case "input_json_delta":
			stream.inputs[index] += asString(delta["partial_json"])
		case "citations_delta":
			block["citations"] = append(asArray(block["citations"]), delta["citation"])
		}

	case "content_block_stop":
		if input := stream.inputs[index]; input != "" {
			var arguments map[string]any
			if err := json.Unmarshal([]byte(input), &arguments); err != nil {
				return false, errors.New("provider streamed invalid tool arguments")
			}

			stream.blocks[index]["input"] = arguments
		}

		delete(stream.open, index)

	case "message_delta":
		for key, value := range asObject(event["delta"]) {
			stream.message[key] = value
		}

		usage := asObject(stream.message["usage"])

		for key, value := range asObject(event["usage"]) {
			usage[key] = value
		}

		stream.message["usage"] = usage

	case "message_stop":
		stream.stopped = true

		return true, nil
	}

	return false, nil
}

func (stream *anthropicStream) result() (map[string]any, error) {
	if !stream.stopped || len(stream.open) != 0 {
		return nil, errors.New("provider stream ended before completion")
	}

	switch asString(stream.message["stop_reason"]) {
	case "max_tokens", "model_context_window_exceeded":
		return nil, errors.New("provider did not complete the response; retry the request")
	}

	stream.message["content"] = orderedStreamItems(stream.blocks)

	return stream.message, nil
}
