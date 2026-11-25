package oidc

import (
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFlowStoreExpirationAndCapacity(t *testing.T) {
	now := time.Now()
	store := flowStore{pending: make(map[string]authFlow)}
	flow := authFlow{browserToken: "browser", expiresAt: now.Add(flowTTL)}

	for i := range maxFlows {
		if !store.add(strconv.Itoa(i), flow, now) {
			t.Fatalf("store rejected flow %d before reaching capacity", i)
		}
	}
	if store.add("overflow", flow, now) {
		t.Fatal("pending flows are not bounded")
	}
	if _, ok := store.consume("0", "browser", flow.expiresAt); ok {
		t.Fatal("expired flow was consumed")
	}

	newFlow := authFlow{browserToken: "browser", expiresAt: flow.expiresAt.Add(flowTTL)}
	if !store.add("fresh", newFlow, flow.expiresAt) || len(store.pending) != 1 {
		t.Fatalf("new flow was not stored or expired entries linger: %d pending", len(store.pending))
	}
}

func TestFlowStoreConsumesExactlyOnceConcurrently(t *testing.T) {
	now := time.Now()
	store := flowStore{pending: map[string]authFlow{"state": {browserToken: "browser", expiresAt: now.Add(flowTTL)}}}
	var group sync.WaitGroup
	var consumed atomic.Int32

	for range 16 {
		group.Go(func() {
			if _, ok := store.consume("state", "browser", now); ok {
				consumed.Add(1)
			}
		})
	}
	group.Wait()
	if count := consumed.Load(); count != 1 {
		t.Fatalf("consumed the same flow %d times", count)
	}
}
