package providers

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const providerAttempts = 4
const maximumRetryWait = 30 * time.Second

type providerRetryResetKey struct{}

// Retry temporary rejections and attempt-local transport/read timeouts. Caller
// cancellation and streams whose text has already been published are not
// replayed. Environmental tools are never executed by this retry loop.
func (client *HTTPClient) postWithRetry(original *http.Request, streaming ...streamRequest) (map[string]any, error) {
	var waited time.Duration

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
		received := time.Now()
		var result map[string]any
		var failure error
		published := false
		attemptStreams := append([]streamRequest(nil), streaming...)
		for index := range attemptStreams {
			onText := attemptStreams[index].onText
			attemptStreams[index].onText = func(text string) {
				if text != "" {
					published = true
				}
				if onText != nil {
					onText(text)
				}
			}
		}
		if err != nil {
			failure = fmt.Errorf("provider request failed: %w", err)
		} else {
			result, failure = decodeProviderResponse(response, attemptStreams...)
			_ = response.Body.Close()
		}
		if err := original.Context().Err(); err != nil {
			return nil, err
		}
		var httpFailure *HTTPError
		retryable := errors.As(failure, &httpFailure) && temporaryProviderError(httpFailure)
		var timeout net.Error
		reset, _ := original.Context().Value(providerRetryResetKey{}).(func())
		retryable = retryable || ((!published || reset != nil) && errors.As(failure, &timeout) && timeout.Timeout())
		if published && reset == nil {
			retryable = false
		}
		if attempt+1 == providerAttempts ||
			!retryable {
			return result, failure
		}

		delay := time.Duration(float64(time.Second*(1<<attempt)) * (0.9 + rand.Float64()*0.2))

		if response != nil {
			if advice, valid := providerRetryAfter(response.Header.Get("Retry-After"), received); valid {
				delay = max(0, advice-time.Since(received))
			}
		}

		// Do not shorten a long Retry-After and hit the server before its deadline.
		// Budget the backoff, not inference time. A Client.Timeout can exceed
		// thirty seconds on its first attempt and must still be retryable.
		if waited+delay > maximumRetryWait {
			return nil, failure
		}

		wait := client.waitRetry
		if wait == nil {
			wait = waitForProviderRetry
		}

		if err := wait(original.Context(), delay); err != nil {
			return nil, err
		}
		if published && reset != nil {
			reset()
		}
		waited += delay
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
