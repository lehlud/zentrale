package agent

import (
	"context"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestShutdownDoesNotReopenSuccessfullyFinishedTurn(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		return providers.Result{}, nil
	}))
	engine.cancel()
	engine.finishAgent(record.ID, nil)
	var stored domain.Agent
	if err := engine.Store.Load("agent", record.ID, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status != "idle" {
		t.Fatalf("completed turn marked for restart: %+v", stored)
	}
}
