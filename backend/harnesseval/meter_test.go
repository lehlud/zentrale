package harnesseval

import (
	"context"
	"testing"

	"github.com/lehlud/zentrale/providers"
)

type meteredFixture struct{ complete, compact int }

func (fixture *meteredFixture) Complete(context.Context, providers.Request) (providers.Result, error) {
	fixture.complete++
	return providers.Result{UsageKnown: true}, nil
}

func (fixture *meteredFixture) Compact(context.Context, providers.Request) (providers.Result, error) {
	fixture.compact++
	return providers.Result{UsageKnown: true}, nil
}

func TestEvaluationBudgetIncludesCompactionWithoutChangingProviderCapabilities(t *testing.T) {
	fixture := &meteredFixture{}
	meter := newMeter(fixture, 2)
	if _, err := meter.Complete(context.Background(), providers.Request{}); err != nil {
		t.Fatal(err)
	}

	if _, err := meter.Compact(context.Background(), providers.Request{}); err != nil {
		t.Fatal(err)
	}

	if _, err := meter.Complete(context.Background(), providers.Request{}); err == nil {
		t.Fatal("request budget ignored")
	}

	if fixture.complete != 1 || fixture.compact != 1 || meter.calls != 2 {
		t.Fatal(fixture, meter.calls)
	}
}
