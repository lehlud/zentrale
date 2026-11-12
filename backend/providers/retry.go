package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const providerAttempts = 4
const maximumRetryWait = 30 * time.Second

type Retry struct {
	Attempt     int
	MaxAttempts int
	Delay       time.Duration
}

// Only inference is repeated. Partial responses are display-only and tools are
// dispatched after Complete returns a completed response. A retry can still be
// billable at the provider; incomplete attempts never enter conversation replay.
func (client *HTTPClient) postWithRetry(original *http.Request, streaming ...streamRequest) (map[string]any, error) {
	var totalWait time.Duration

	for attempt := 0; ; attempt++ {
		if err := original.Context().Err(); err != nil {
			return nil, err
		}

		body, err := original.GetBody()
		if err != nil {
			return nil, err
		}

		ctx, activity := watchProviderActivity(original.Context(), client.idleTimeout)
		request := original.Clone(ctx)
		request.Body = body
		response, err := client.HTTP.Do(request)
		received := time.Now()
		published := false
		attemptStreams := append([]streamRequest(nil), streaming...)
		for index := range attemptStreams {
			onText := attemptStreams[index].onText
			if onText == nil {
				continue
			}
			attemptStreams[index].onText = func(text string) {
				if text != "" {
					published = true
				}
				onText(text)
			}
		}
		var result map[string]any
		var failure error
		if err != nil {
			failure = fmt.Errorf("provider request failed: %w", err)
		} else {
			activity.touch()
			response.Body = &activeProviderBody{ReadCloser: response.Body, activity: activity}
			result, failure = decodeProviderResponse(response, attemptStreams...)

			if failure == nil && (result["error"] != nil || result["status"] == "failed" || result["status"] == "incomplete") {
				failure = responseFailure(result)
			}
		}

		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		cause := context.Cause(ctx)
		activity.close()

		if err := original.Context().Err(); err != nil {
			return nil, err
		}

		if errors.Is(cause, errProviderIdle) && (response == nil || response.StatusCode >= 200 && response.StatusCode < 300) {
			failure = errProviderIdle
		}

		if failure == nil {
			return result, nil
		}

		canReset := len(streaming) > 0 && streaming[0].onRetry != nil
		if attempt+1 == providerAttempts || !temporaryProviderFailure(failure) || (published && !canReset) {
			return nil, failure
		}

		delay := time.Duration(float64(time.Second*(1<<attempt)) * (0.9 + rand.Float64()*0.2))

		if response != nil {
			if advice, valid := providerRetryAfter(response.Header.Get("Retry-After"), received); valid {
				delay = max(0, advice-time.Since(received))
			}
		}

		// Do not shorten a long Retry-After and hit the server before its deadline.
		// Generation time is not backoff time. Otherwise a single slow attempt
		// would consume this budget before its first retry can even begin.
		if totalWait+delay > maximumRetryWait {
			return nil, failure
		}

		totalWait += delay
		if len(streaming) > 0 && streaming[0].onRetry != nil {
			streaming[0].onRetry(Retry{Attempt: attempt + 2, MaxAttempts: providerAttempts, Delay: delay})
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

func temporaryProviderFailure(failure error) bool {
	var readFailure *providerReadError
	if errors.As(failure, &readFailure) {
		return true
	}

	var httpFailure *HTTPError
	if errors.As(failure, &httpFailure) {
		return temporaryProviderError(httpFailure)
	}

	var responseFailure *ResponseError
	if errors.As(failure, &responseFailure) {
		switch strings.ToLower(responseFailure.Code) {
		case "server_error", "internal_server_error", "overloaded_error", "rate_limit_error", "rate_limit_exceeded":
			return true
		default:
			return false
		}
	}

	if errors.Is(failure, errProviderIdle) || errors.Is(failure, errStreamInterrupted) ||
		errors.Is(failure, context.DeadlineExceeded) || errors.Is(failure, io.ErrUnexpectedEOF) ||
		errors.Is(failure, io.EOF) || errors.Is(failure, syscall.ECONNRESET) ||
		errors.Is(failure, syscall.ECONNREFUSED) || errors.Is(failure, syscall.EPIPE) {
		return true
	}

	var networkFailure net.Error
	return errors.As(failure, &networkFailure) && networkFailure.Timeout()
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
