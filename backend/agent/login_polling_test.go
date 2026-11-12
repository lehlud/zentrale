package agent

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestLoginProtocolPendingSlowDownBackoffAndCompletion(t *testing.T) {
	now := time.Now()
	var delays []time.Duration
	polls := 0
	finished := ""
	runLoginProtocol(context.Background(), now.Add(time.Hour), 5,
		func(ctx context.Context) (loginPollState, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded external request")
			}
			polls++
			switch polls {
			case 1:
				return loginPollState{"pending", 5}, nil
			case 2:
				return loginPollState{"pending", 10}, nil
			case 3, 4:
				return loginPollState{}, errors.New("temporary network failure")
			case 5:
				return loginPollState{"pending", 10}, nil
			default:
				return loginPollState{"connected", 10}, nil
			}
		}, func(reason string) { finished = reason }, func() time.Time { return now },
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			now = now.Add(delay)
			return nil
		})
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 10 * time.Second}
	if !reflect.DeepEqual(delays, want) || finished != "" || polls != 6 {
		t.Fatalf("delays=%v finished=%q polls=%d", delays, finished, polls)
	}
}

func TestLoginProtocolExpiryCancellationAndManualFlow(t *testing.T) {
	for _, manual := range []bool{false, true} {
		now := time.Now()
		finished, polls := "", 0
		var poll func(context.Context) (loginPollState, error)
		if !manual {
			poll = func(context.Context) (loginPollState, error) { polls++; return loginPollState{}, nil }
		}
		runLoginProtocol(context.Background(), now.Add(time.Second), 5, poll,
			func(reason string) { finished = reason }, func() time.Time { return now },
			func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil })
		if finished != "expired" || polls != 0 {
			t.Fatalf("manual=%v finished=%s polls=%d", manual, finished, polls)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished, polls := "", 0
	runLoginProtocol(ctx, time.Now().Add(time.Hour), 5,
		func(context.Context) (loginPollState, error) { polls++; return loginPollState{}, nil },
		func(reason string) { finished = reason }, time.Now,
		func(context.Context, time.Duration) error { cancel(); return context.Canceled })
	if finished != "cancelled" || polls != 0 {
		t.Fatal(finished, polls)
	}
}

func TestLoginProtocolNeverBacksOffBelowProviderInterval(t *testing.T) {
	now := time.Now()
	var delays []time.Duration
	runLoginProtocol(context.Background(), now.Add(5*time.Minute), 120,
		func(context.Context) (loginPollState, error) { return loginPollState{}, errors.New("temporary") },
		func(string) {}, func() time.Time { return now },
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			now = now.Add(delay)
			return nil
		})
	if !reflect.DeepEqual(delays, []time.Duration{2 * time.Minute, 2 * time.Minute, time.Minute}) {
		t.Fatal(delays)
	}
	if loginProtocolDelay(int(^uint(0)>>1)) <= 0 {
		t.Fatal("interval overflow")
	}
}

func TestLoginProtocolWorkerLimitAndEngineCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := &Engine{ctx: ctx}
	defer engine.workers.Wait()
	defer cancel()
	for i := 0; i < 32; i++ {
		if err := engine.startLoginPolling(strconv.Itoa(i), strconv.Itoa(i), time.Now().Add(time.Hour), 5, nil, func(string) {}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.startLoginPolling("overflow", "overflow", time.Now().Add(time.Hour), 5, nil, func(string) {}); err == nil {
		t.Fatal("unbounded login workers")
	}
	cancel()
	engine.workers.Wait()
	engine.loginPollMu.Lock()
	defer engine.loginPollMu.Unlock()
	if len(engine.loginPollers) != 0 {
		t.Fatal("workers retained after cancellation")
	}
}
