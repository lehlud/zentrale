package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSocketQueryGuardsReuseBudgetsAndRejectAllWriteOperations(t *testing.T) {
	for _, input := range []struct{ query, operation string }{
		{`mutation { logout }`, ""},
		{`query Read { status { authenticated } } mutation Write { logout }`, "Read"},
		{`subscription { status { authenticated } }`, ""},
		{`{ status { a { b { c { d } } } } }`, ""},
		{`{ ` + strings.Repeat(`status { authenticated } `, 21) + ` }`, ""},
		{`{ status { authenticated } }` + strings.Repeat(" ", 64*1024), ""},
		{`query { ...A } fragment A on Query { ...A }`, ""},
	} {
		if err := validateSocketQuery(socketQueryRequest{RequestID: "test", Query: input.query, OperationName: input.operation}); err == nil {
			t.Fatalf("unsafe snapshot accepted: %.150s", input.query)
		}
	}
	if err := validateSocketQuery(socketQueryRequest{RequestID: "valid", Query: `query Named($id:String!){agent(id:$id){id}}`, Variables: map[string]any{"id": "agent"}}); err != nil {
		t.Fatal(err)
	}
}

func TestSocketQueryPoolCancellationRetainsRealSlotsAndBoundsQueue(t *testing.T) {
	poolContext, stopPool := context.WithCancel(context.Background())
	pool := newSocketQueryPool(poolContext, 2, 1)
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan context.Context, 3)
	release := make(chan struct{})
	var releaseOnce sync.Once
	results := make(chan socketQueryResult, 3)
	execute := func(ctx context.Context, _ socketQueryRequest) any {
		entered <- ctx
		<-release // Deliberately ignores cancellation.
		return map[string]any{"data": true}
	}
	t.Cleanup(func() {
		cancel()
		stopPool()
		releaseOnce.Do(func() { close(release) })
		for _, stopped := range pool.stopped {
			select {
			case <-stopped:
			case <-time.After(2 * time.Second):
				t.Error("worker did not stop after real resolver end")
			}
		}
	})
	for index := 0; index < 2; index++ {
		if !pool.submit(socketQueryTask{ctx: ctx, work: socketQueryWork{request: socketQueryRequest{RequestID: fmt.Sprint(index)}}, execute: execute, results: results}) {
			t.Fatal("initial task rejected")
		}
		select {
		case execution := <-entered:
			if execution.Done() != nil {
				t.Fatal("executor may return before actual resolver completion")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	cancel()
	queued := socketQueryTask{ctx: poolContext, work: socketQueryWork{request: socketQueryRequest{RequestID: "queued"}}, execute: execute, results: results}
	if !pool.submit(queued) {
		t.Fatal("waiting slot unavailable")
	}
	if pool.submit(queued) {
		t.Fatal("cancellation released real slots or queue unbounded")
	}
	if len(pool.queue) != 1 {
		t.Fatal("work escaped queue before real resolver end")
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case result := <-results:
		if result.work.request.RequestID != "queued" {
			t.Fatal("cancelled execution pushed result")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pool did not recover after resolver end")
	}
}

func TestSocketQueriesInitialEventPushDeduplicationAndCoalescing(t *testing.T) {
	queries := newSocketQueries()
	if failure := queries.command(socketQueryRequest{Type: "subscribe", RequestID: "one", Query: `{status{authenticated}}`}); failure != nil {
		t.Fatal(failure)
	}
	work := queries.schedule()
	if work == nil || work.request.RequestID != "one" {
		t.Fatal("initial snapshot not scheduled")
	}
	for range 1000 {
		queries.invalidate()
	}
	if queries.schedule() != nil {
		t.Fatal("events created concurrent query work")
	}
	encoded := json.RawMessage(`{"data":{"status":{"authenticated":true}}}`)
	first := queries.receive(socketQueryResult{work: *work, encoded: encoded})
	if first == nil || first.Type != "query_result" || first.RequestID != "one" {
		t.Fatal("missing correlated initial snapshot")
	}
	followup := queries.schedule()
	if followup == nil {
		t.Fatal("events during initial read lost")
	}
	if queries.receive(socketQueryResult{work: *followup, encoded: encoded}) != nil {
		t.Fatal("unchanged snapshot not deduplicated")
	}
	if queries.schedule() != nil {
		t.Fatal("burst scheduled more than one follow-up")
	}
	queries.invalidate()
	changed := queries.schedule()
	if changed == nil {
		t.Fatal("durable update not scheduled")
	}
	if queries.receive(socketQueryResult{work: *changed, encoded: json.RawMessage(`{"data":{"status":{"authenticated":false}}}`)}) == nil {
		t.Fatal("changed snapshot suppressed")
	}
}

func TestSocketQueriesBoundedSubscriptionsUnsubscribeAndRequestIDReuse(t *testing.T) {
	queries := newSocketQueries()
	for index := 0; index < socketQueryLimit; index++ {
		if failure := queries.command(socketQueryRequest{Type: "subscribe", RequestID: fmt.Sprint(index), Query: `{status{authenticated}}`}); failure != nil {
			t.Fatal(failure)
		}
	}
	if queries.command(socketQueryRequest{Type: "subscribe", RequestID: "overflow", Query: `{status{authenticated}}`}) == nil {
		t.Fatal("subscription cap not enforced")
	}
	if queries.command(socketQueryRequest{Type: "subscribe", RequestID: "0", Query: `{status{authenticated}}`}) == nil {
		t.Fatal("duplicate requestId accepted")
	}
	work := queries.schedule()
	if work == nil {
		t.Fatal("initial work missing")
	}
	queries.command(socketQueryRequest{Type: "unsubscribe", RequestID: work.request.RequestID})
	if failure := queries.command(socketQueryRequest{Type: "subscribe", RequestID: work.request.RequestID, Query: `{status{authenticated}}`}); failure != nil {
		t.Fatal(failure)
	}
	if queries.receive(socketQueryResult{work: *work, encoded: json.RawMessage(`{"data":{}}`)}) != nil {
		t.Fatal("unsubscribed generation leaked a result")
	}
	if len(queries.subscriptions) != socketQueryLimit || len(queries.order) != socketQueryLimit {
		t.Fatal("unsubscribe leaked subscription state")
	}
	for _, id := range []string{"", strings.Repeat("x", 129)} {
		if queries.command(socketQueryRequest{Type: "subscribe", RequestID: id, Query: `{status{authenticated}}`}) == nil {
			t.Fatal("invalid requestId accepted")
		}
	}
	if queries.command(socketQueryRequest{Type: "query", RequestID: "unsupported"}) == nil {
		t.Fatal("client-driven polling command accepted")
	}
}
