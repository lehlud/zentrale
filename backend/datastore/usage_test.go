package datastore

import (
	"fmt"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestUsageTotalsIncludeMoreThanOneListingPage(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for index := range 510 {
		usage := domain.Usage{ID: fmt.Sprint(index), AgentID: "agent", InputTokens: 100, OutputTokens: 20, CachedTokens: 60, RealCost: 0.5, FictionalCost: 0.2, CostKnown: index != 0, Estimated: true}
		if err := store.Save("usage", usage.ID, usage.AgentID, usage); err != nil {
			t.Fatal(err)
		}
	}
	totals, err := store.UsageSummary("agent")
	if err != nil {
		t.Fatal(err)
	}
	if totals.RequestCount != 510 || totals.InputTokens != 51000 || totals.CachedTokens != 30600 || totals.RealCost != 255 || totals.UnknownPrices != 1 || totals.UnknownTokenCounts != 510 || totals.UnknownCacheWrites != 510 || totals.CacheInputTokens != 0 || totals.CacheReadTokens != 0 {
		t.Fatalf("unbounded totals: %+v", totals)
	}
	empty, err := store.UsageSummary("other")
	if err != nil || empty.RequestCount != 0 {
		t.Fatalf("empty summary: %+v %v", empty, err)
	}
}

func TestResourceProjectionOmitsUnrequestedPayloads(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save("message", "message", "agent", map[string]any{"id": "message", "content": "large body", "wire": "private provider reasoning"}); err != nil {
		t.Fatal(err)
	}
	parent := "agent"
	rows, err := store.ListFields("message", &parent, 100, 0, []string{"id"})
	if err != nil || len(rows) != 1 || rows[0]["id"] != "message" || len(rows[0]) != 1 {
		t.Fatalf("projection: %+v %v", rows, err)
	}
}
