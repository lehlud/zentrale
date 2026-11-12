package datastore

import (
	"fmt"
	"testing"
)

func TestPendingProviderLoginRecoveryIsBoundedAndLatestPerProvider(t *testing.T) {
	store, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	save := func(id, provider, status string) {
		t.Helper()
		if err := store.Save("provider_login", id, provider, map[string]any{"session": map[string]any{"id": id, "providerId": provider, "status": status}, "deviceAuthorizationId": "NEVER-RETURN-THIS"}); err != nil {
			t.Fatal(err)
		}
	}
	save("a-old-pending", "completed-provider", "pending")
	save("z-new-completed", "completed-provider", "connected")
	save("a-old-grant", "replacement-provider", "pending")
	save("z-new-grant", "replacement-provider", "pending")
	for i := 0; i < 40; i++ {
		save(fmt.Sprintf("login-%02d", i), fmt.Sprintf("provider-%02d", i), "pending")
	}
	ids, err := store.ListPendingProviderLogins(32)
	if err != nil || len(ids) != 32 {
		t.Fatal(ids, err)
	}
	for _, id := range ids {
		if id == "a-old-pending" || id == "a-old-grant" || id == "z-new-completed" || id == "NEVER-RETURN-THIS" {
			t.Fatal("ineligible/private record returned", id)
		}
	}
	for _, limit := range []int{-1, 0, 33} {
		if _, err := store.ListPendingProviderLogins(limit); err == nil {
			t.Fatal("unbounded recovery limit accepted", limit)
		}
	}
}
