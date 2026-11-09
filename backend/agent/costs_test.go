package agent

import (
	"context"
	"math"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestUsageKeepsTokenProvenanceAndPricesCacheWritesOnce(t *testing.T) {
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		t.Fatal("usage accounting must not call a provider")
		return providers.Result{}, nil
	}))
	configuration := inferenceConfig{Provider: domain.Provider{ID: "provider", Kind: "openai"}, Model: domain.LanguageModel{ID: "model", PricesKnown: true, InputPrice: 1, CachedPrice: 0.1, OutputPrice: 2}}
	result := providers.Result{UsageKnown: true, InputTokens: 12000, OutputTokens: 20, CachedTokens: 4000, CacheReadKnown: true, CacheWriteTokens: 7000, CacheWriteLongTokens: 4000, CacheWriteKnown: true}
	if err := engine.recordUsage(record, configuration, result, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := engine.listAll("usage", &record.ID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	var usage domain.Usage
	if err := engine.Store.Load("usage", rows[0]["id"].(string), &usage); err != nil {
		t.Fatal(err)
	}
	// 1000 ordinary + 4000*.1 read + 3000*1.25 short write +
	// 4000*2.5 long write + 20*2 output; input already includes writes.
	if math.Abs(usage.RealCost-0.01519) > 1e-10 || !usage.TokensKnown || !usage.CacheWriteKnown || !usage.CostKnown || !usage.Estimated || usage.CacheWriteTokens != 7000 {
		t.Fatalf("usage: %+v", usage)
	}
	configuration.Provider.AuthMode = "device"
	if err := engine.recordUsage(record, configuration, result, nil); err != nil {
		t.Fatal(err)
	}
	// Counts without provider usage remain estimates even if an explicit cost is known.
	configuration.Provider.AuthMode = "api"
	cost := 0.03
	if err := engine.recordUsage(record, configuration, providers.Result{Text: "done", Cost: &cost, CachedTokens: 9999, CacheWriteTokens: 9999}, []domain.Message{{Content: "task"}}, 10); err != nil {
		t.Fatal(err)
	}
	totals, err := engine.Store.UsageSummary(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.CacheInputTokens != 24000 || totals.CacheReadTokens != 8000 || totals.CacheWriteTokens != 14000 || totals.UnknownTokenCounts != 1 || totals.UnknownCacheWrites != 1 || math.Abs(totals.FictionalCost-0.01519) > 1e-10 {
		t.Fatalf("provenance or subscription equivalent: %+v", totals)
	}
}
