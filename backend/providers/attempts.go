package providers

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
)

type attemptObserverKey struct{}

func WithAttemptObserver(ctx context.Context, observer func(domain.InferenceAttempt)) context.Context {
	return context.WithValue(ctx, attemptObserverKey{}, observer)
}

func observeAttempt(ctx context.Context, transport string, started time.Time, sent, status int, result map[string]any, failure error) {
	observer, _ := ctx.Value(attemptObserverKey{}).(func(domain.InferenceAttempt))
	if observer == nil {
		return
	}
	usage := Result{}
	readResponsesUsage(&usage, result["usage"])
	raw := asObject(result["usage"])
	if raw["cache_read_input_tokens"] != nil || raw["cache_creation_input_tokens"] != nil {
		usage.CachedTokens = asInt(raw["cache_read_input_tokens"])
		usage.InputTokens += usage.CachedTokens + asInt(raw["cache_creation_input_tokens"])
		usage.CacheReadKnown = raw["cache_read_input_tokens"] != nil
	}
	if raw["prompt_tokens"] != nil {
		usage.InputTokens, usage.OutputTokens = asInt(raw["prompt_tokens"]), asInt(raw["completion_tokens"])
		usage.UsageKnown = raw["completion_tokens"] != nil
		details := asObject(raw["prompt_tokens_details"])
		usage.CachedTokens, usage.CacheReadKnown = asInt(details["cached_tokens"]), details["cached_tokens"] != nil
	}
	if raw := asObject(result["usageMetadata"]); raw["promptTokenCount"] != nil {
		usage.InputTokens = asInt(raw["promptTokenCount"])
		usage.OutputTokens = asInt(raw["candidatesTokenCount"]) + asInt(raw["thoughtsTokenCount"])
		usage.UsageKnown = raw["candidatesTokenCount"] != nil
		usage.CachedTokens, usage.CacheReadKnown = asInt(raw["cachedContentTokenCount"]), raw["cachedContentTokenCount"] != nil
	}
	observer(domain.InferenceAttempt{
		Transport: transport, DurationMillis: int(time.Since(started).Milliseconds()), SentBytes: sent,
		Status: status, Outcome: attemptOutcome(failure), InputTokens: usage.InputTokens,
		OutputTokens: usage.OutputTokens, CachedTokens: usage.CachedTokens,
		TokensKnown: usage.UsageKnown, CacheReadKnown: usage.CacheReadKnown,
	})
}

// Categories are deliberately fixed strings; server diagnostics can contain
// secrets and are not copied into persistent traces.
func attemptOutcome(failure error) string {
	if errors.Is(failure, errSocketUnsupported) {
		return "unsupported_transport"
	}
	if rejectedCacheOption(failure) {
		return "unsupported_cache_option"
	}
	switch {
	case failure == nil:
		return "completed"
	case errors.Is(failure, context.Canceled):
		return "canceled"
	case errors.Is(failure, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(failure, errProviderIdle):
		return "idle_timeout"
	case errors.Is(failure, errStreamInterrupted):
		return "stream_interrupted"
	}
	var httpFailure *HTTPError
	if errors.As(failure, &httpFailure) {
		return "http_error"
	}
	var responseFailure *ResponseError
	if errors.As(failure, &responseFailure) {
		if responseFailure.Code == "previous_response_not_found" {
			return "server_state_missing"
		}
		return "response_error"
	}
	return "transport_error"
}
