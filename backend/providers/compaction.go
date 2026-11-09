package providers

import (
	"context"
	"errors"
	"net/url"
)

var ErrCompactionUnsupported = errors.New("provider does not support native context compaction")

// NativeCompactor is optional. Every provider can use the portable text
// compactor; future native adapters implement the same capability.
type NativeCompactor interface {
	Compact(context.Context, Request) (Result, error)
}

func (client *HTTPClient) Compact(ctx context.Context, request Request) (Result, error) {
	address, _ := url.Parse(request.Config.BaseURL)
	if request.Config.Kind != "openai" || address == nil ||
		(address.Hostname() != "api.openai.com" && address.Hostname() != "chatgpt.com") {
		return Result{}, ErrCompactionUnsupported
	}

	data, err := client.post(ctx, request.Config, "/responses/compact", map[string]any{
		"model":        request.Model.ID,
		"instructions": request.System,
		"input":        responsesInput(request.Messages),
	})
	if err != nil {
		var failure *HTTPError
		// Optional endpoint/model/schema support can fail with 400 or 422.
		// Retain the complete originals and use portable compaction in that case.
		// Authentication, quota and transient server failures remain explicit.
		if errors.As(err, &failure) &&
			(failure.Status == 400 ||
				failure.Status == 422 ||
				failure.Status == 404 ||
				failure.Status == 405 ||
				failure.Status == 501 ||
				failure.Code == "unsupported_model" || failure.Code == "unsupported_parameter") {
			return Result{}, ErrCompactionUnsupported
		}

		return Result{}, err
	}

	output := asArray(data["output"])
	valid := false

	for _, value := range output {
		item := asObject(value)
		if asString(item["type"]) == "compaction" && asString(item["encrypted_content"]) != "" {
			valid = true
		}
	}

	if !valid {
		return Result{}, errors.New("native compaction returned no encrypted checkpoint; original history retained")
	}

	result := Result{Wire: encodeWire(output)}
	readResponsesUsage(&result, data["usage"])

	return result, nil
}
