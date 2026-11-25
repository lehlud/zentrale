package agent

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func quotaTestEngine(t *testing.T, user domain.User, provider providers.Client) (*Engine, domain.Agent, domain.Agent) {
	t.Helper()
	store, err := datastore.Open("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	user.ID, user.Username, user.Role = "owner", "owner", domain.RoleUser
	if err := store.InsertAccount(datastore.Account{User: user, PasswordHash: "test-placeholder"}); err != nil {
		t.Fatal(err)
	}
	project := domain.Project{ID: "project", OwnerID: user.ID}
	if err := store.Save("project", project.ID, "", project); err != nil {
		t.Fatal(err)
	}
	chat := domain.Agent{ID: "chat", ChatOwnerID: user.ID}
	reviewer := domain.Agent{ID: "approver", ProjectID: project.ID, Role: "approver"}
	for _, agent := range []domain.Agent{chat, reviewer} {
		if err := store.Save("agent", agent.ID, agent.ProjectID, agent); err != nil {
			t.Fatal(err)
		}
	}
	return &Engine{Store: store, Provider: provider}, chat, reviewer
}

func TestUsageLimitsReserveAcrossChatAndProjectBeforeProviderCall(t *testing.T) {
	const quota = 50
	started := make(chan providers.Request, 1)
	finish := make(chan struct{})
	var calls atomic.Int32
	provider := providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		if calls.Add(1) != 1 {
			return providers.Result{}, errors.New("unexpected second inference")
		}
		started <- request
		select {
		case <-finish:
			return providers.Result{UsageKnown: true, InputTokens: 10, OutputTokens: quota - 10}, nil
		case <-ctx.Done():
			return providers.Result{}, ctx.Err()
		}
	})
	engine, chat, reviewer := quotaTestEngine(t, domain.User{MonthlyTokenLimit: quota}, provider)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := engine.completeModel(ctx, reviewer.ID, "goal_review", providers.Request{})
		done <- err
	}()

	select {
	case request := <-started:
		if !request.SingleAttempt || request.MaxOutputTokens <= 0 || request.MaxOutputTokens >= quota {
			t.Fatalf("provider call lacks a one-attempt output bound: %+v", request)
		}
	case err := <-done:
		t.Fatalf("first inference rejected before provider call: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	if _, err := engine.completeModel(ctx, chat.ID, "main", providers.Request{}); !errors.Is(err, errMonthlyTokens) {
		t.Fatalf("in-flight project review did not reserve the owner's chat budget: %v", err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := engine.completeModel(ctx, reviewer.ID, "summary", providers.Request{}); !errors.Is(err, errMonthlyTokens) {
		t.Fatalf("settled review did not count against the compaction budget: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("got %d provider calls, want one", got)
	}
}

func TestUsageLimitsFailClosedWhenPricesOrOutputBoundsUnavailable(t *testing.T) {
	var calls int
	provider := providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls++
		return providers.Result{Text: "done", UsageKnown: true, InputTokens: 2, OutputTokens: 1}, nil
	})
	engine, chat, _ := quotaTestEngine(t, domain.User{MonthlyCostLimit: 0.05}, provider)
	paid := providers.Request{Config: domain.Provider{Kind: "openai"}}
	if _, err := engine.completeModel(t.Context(), chat.ID, "main", paid); !errors.Is(err, errUnknownPrice) {
		t.Fatalf("unpriced metered call returned %v", err)
	}
	if err := engine.guardOpaqueUsage(chat, paid.Config); !errors.Is(err, errOpaqueUsage) {
		t.Fatalf("unpriced hosted inference returned %v", err)
	}
	if _, err := engine.traceInference(t.Context(), chat.ID, "native_compaction", paid, provider); !errors.Is(err, providers.ErrCompactionUnsupported) {
		t.Fatalf("native compaction did not fall back: %v", err)
	}

	fixed := paid
	fixed.Config.BillingMode = "fixed"
	if err := engine.guardOpaqueUsage(chat, fixed.Config); err != nil {
		t.Fatalf("fixed-price operation incorrectly blocked by the dollar cap: %v", err)
	}
	if _, err := engine.completeModel(t.Context(), chat.ID, "main", fixed); err != nil {
		t.Fatalf("fixed-price model incorrectly blocked by the dollar cap: %v", err)
	}
	paid.Model = domain.LanguageModel{PricesKnown: true, InputPrice: 1_000_000, OutputPrice: 1_000_000}
	if _, err := engine.completeModel(t.Context(), chat.ID, "main", paid); !errors.Is(err, errMonthlyCost) {
		t.Fatalf("known expensive paid call returned %v", err)
	}
	if calls != 1 {
		t.Fatalf("got %d provider calls, want only the fixed-price one", calls)
	}
}

func TestUsageLimitsStopFurtherCallsAfterFailedMeterWrite(t *testing.T) {
	var calls int
	provider := providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		calls++
		// An invalid provider cost cannot be serialized into the accounting row.
		cost := math.NaN()
		return providers.Result{Cost: &cost}, nil
	})
	engine, chat, _ := quotaTestEngine(t, domain.User{MonthlyTokenLimit: 100}, provider)
	if _, err := engine.completeModel(t.Context(), chat.ID, "main", providers.Request{}); err == nil {
		t.Fatal("expected usage persistence failure")
	}
	if _, err := engine.completeModel(t.Context(), chat.ID, "main", providers.Request{}); !errors.Is(err, errUsageUnsettled) {
		t.Fatalf("unsettled usage should block further calls, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("got %d provider calls after failed accounting, want one", calls)
	}
}
