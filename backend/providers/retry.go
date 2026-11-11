package providers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const providerAttempts = 4
const maximumRetryWait = 30 * time.Second

// Retry explicit temporary HTTP rejections only. Transport failures and partial
// successful streams can already have generated a billed response; replaying
// those requests would be ambiguous. Environmental tools are never replayed.
func (client *HTTPClient) postWithRetry(original *http.Request, streaming ...streamRequest) (map[string]any, error) {
	started := time.Now()

	for attempt := 0; ; attempt++ {
		if err := original.Context().Err(); err != nil {
			return nil, err
		}

		request := original.Clone(original.Context())
		body, err := original.GetBody()
		if err != nil {
			return nil, err
		}

		request.Body = body
		response, err := client.HTTP.Do(request)
		if err != nil {
			return nil, fmt.Errorf("provider request failed: %w", err)
		}

		received := time.Now()
		result, failure := decodeProviderResponse(response, streaming...)
		_ = response.Body.Close()
		var httpFailure *HTTPError
		if attempt+1 == providerAttempts ||
			!errors.As(failure, &httpFailure) ||
			!temporaryProviderError(httpFailure) {
			return result, failure
		}

		delay := time.Duration(float64(time.Second*(1<<attempt)) * (0.9 + rand.Float64()*0.2))

		if advice, valid := providerRetryAfter(response.Header.Get("Retry-After"), received); valid {
			delay = max(0, advice-time.Since(received))
		}

		// Do not shorten a long Retry-After and hit the server before its deadline.
		if time.Since(started)+delay > maximumRetryWait {
			return nil, failure
		}

		wait := client.waitRetry
		if wait == nil {
			wait = waitForProviderRetry
		}

		if err := wait(original.Context(), delay); err != nil {
			return nil, err
		}
	}
}

func temporaryProviderError(failure *HTTPError) bool {
	switch strings.ToLower(failure.Code) {
	case "insufficient_quota", "billing_hard_limit_reached", "billing_not_active", "credit_balance_too_low", "quota_exceeded":
		return false
	}

	switch failure.Status {
	case 408, 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

func providerRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
		if seconds > uint64(maximumRetryWait/time.Second) {
			return maximumRetryWait + time.Second, true
		}

		return time.Duration(seconds) * time.Second, true
	}

	deadline, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}

	return max(0, deadline.Sub(now)), true
}

func waitForProviderRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
