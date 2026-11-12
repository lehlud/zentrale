package providers

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"
)

const providerIdleTimeout = 5 * time.Minute

var errProviderIdle = errors.New("model connection received no data for too long")

// Generation may take longer than a fixed request deadline while the stream is
// healthy. Bound silence instead, including the wait for response headers.
type providerActivity struct {
	mutex    sync.Mutex
	idle     time.Duration
	deadline time.Time
	timer    *time.Timer
	cancel   context.CancelCauseFunc
	closed   bool
}

func watchProviderActivity(parent context.Context, idle time.Duration) (context.Context, *providerActivity) {
	if idle <= 0 {
		idle = providerIdleTimeout
	}

	ctx, cancel := context.WithCancelCause(parent)
	activity := &providerActivity{idle: idle, deadline: time.Now().Add(idle), cancel: cancel}
	activity.timer = time.AfterFunc(idle, activity.expire)

	return ctx, activity
}

func (activity *providerActivity) expire() {
	activity.mutex.Lock()
	defer activity.mutex.Unlock()

	if activity.closed {
		return
	}

	// Reset can race with an already queued timer callback. Recheck the most
	// recent read before cancelling an otherwise healthy request.
	if remaining := time.Until(activity.deadline); remaining > 0 {
		activity.timer.Reset(remaining)
		return
	}

	activity.cancel(errProviderIdle)
}

func (activity *providerActivity) touch() {
	activity.mutex.Lock()
	defer activity.mutex.Unlock()

	if !activity.closed {
		activity.deadline = time.Now().Add(activity.idle)
		activity.timer.Reset(activity.idle)
	}
}

func (activity *providerActivity) close() {
	activity.mutex.Lock()
	activity.closed = true
	activity.timer.Stop()
	activity.mutex.Unlock()
	activity.cancel(nil)
}

type activeProviderBody struct {
	io.ReadCloser
	activity *providerActivity
}

type providerReadError struct{ error }

func (failure *providerReadError) Unwrap() error { return failure.error }

func (body *activeProviderBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if count > 0 {
		body.activity.touch()
	}

	if err != nil && !errors.Is(err, io.EOF) {
		err = &providerReadError{err}
	}

	return count, err
}
