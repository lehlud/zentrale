package harnesseval

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/lehlud/zentrale/providers"
)

type requestMeter struct {
	client         providers.Client
	mutex          sync.Mutex
	limit          int
	calls          int
	duration       time.Duration
	tokensReported bool
	tools          map[string]int
}

func (meter *requestMeter) measure(
	ctx context.Context,
	request providers.Request,
	invoke func(context.Context, providers.Request) (providers.Result, error),
) (providers.Result, error) {
	meter.mutex.Lock()

	if meter.calls >= meter.limit {
		meter.mutex.Unlock()
		return providers.Result{}, errors.New("evaluation model request budget exhausted")
	}

	meter.calls++
	meter.mutex.Unlock()
	start := time.Now()
	result, err := invoke(ctx, request)
	meter.mutex.Lock()
	defer meter.mutex.Unlock()

	meter.duration += time.Since(start)
	meter.tokensReported = meter.tokensReported && result.UsageKnown

	for _, call := range result.Calls {
		name := call.Name
		if name == "harness" {
			if action, ok := call.Arguments["action"].(string); ok {
				name = action
			}
		}

		meter.tools[name]++
	}

	return result, err
}

func (meter *requestMeter) Complete(ctx context.Context, request providers.Request) (providers.Result, error) {
	return meter.measure(ctx, request, meter.client.Complete)
}

// Preserve the production client's optional native compaction behavior.
func (meter *requestMeter) Compact(ctx context.Context, request providers.Request) (providers.Result, error) {
	compactor, ok := meter.client.(providers.NativeCompactor)
	if !ok {
		return providers.Result{}, providers.ErrCompactionUnsupported
	}

	return meter.measure(ctx, request, compactor.Compact)
}

func (meter *requestMeter) collectActivities(rows []map[string]any) {
	for _, row := range rows {
		var record struct {
			Activities []struct {
				Name string `json:"name"`
			} `json:"activities"`
		}

		_ = decodeValue(row, &record)

		for _, activity := range record.Activities {
			meter.tools[activity.Name]++
		}
	}
}

func newMeter(client providers.Client, limit int) *requestMeter {
	return &requestMeter{client: client, limit: limit, tokensReported: true, tools: map[string]int{}}
}

var _ providers.Client = (*requestMeter)(nil)

var _ providers.NativeCompactor = (*requestMeter)(nil)
