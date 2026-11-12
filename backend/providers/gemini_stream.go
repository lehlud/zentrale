package providers

type geminiStream struct {
	request streamRequest

	content map[string]any
	parts   []any
	usage   any
	finish  string
}

func (stream *geminiStream) consume(event map[string]any) (bool, error) {
	if event["usageMetadata"] != nil {
		stream.usage = event["usageMetadata"]
	}

	for _, value := range asArray(event["candidates"]) {
		candidate := asObject(value)
		if asInt(candidate["index"]) != 0 {
			continue
		}

		if reason := asString(candidate["finishReason"]); reason != "" {
			stream.finish = reason
		}

		content := asObject(candidate["content"])
		stream.content = content

		for _, value := range asArray(content["parts"]) {
			part := asObject(value)
			text, isText := part["text"].(string)
			if isText && part["thought"] != true {
				stream.request.text(text)
			}

			if isText && len(stream.parts) > 0 {
				previous := asObject(stream.parts[len(stream.parts)-1])
				_, previousText := previous["text"].(string)
				if previousText &&
					previous["thought"] == part["thought"] &&
					previous["thoughtSignature"] == nil {
					previous["text"] = asString(previous["text"]) + text

					if part["thoughtSignature"] != nil {
						previous["thoughtSignature"] = part["thoughtSignature"]
					}

					continue
				}
			}

			stream.parts = append(stream.parts, part)
		}
	}

	return false, nil
}

func (stream *geminiStream) result() (map[string]any, error) {
	if stream.finish == "" {
		return nil, errStreamInterrupted
	}

	if stream.finish != "STOP" {
		return nil, &ResponseError{Reason: stream.finish}
	}

	if stream.content == nil {
		return nil, errStreamInterrupted
	}

	stream.content["parts"] = stream.parts
	stream.content["role"] = "model"

	return map[string]any{
		"candidates":    []any{map[string]any{"content": stream.content}},
		"usageMetadata": stream.usage,
	}, nil
}
