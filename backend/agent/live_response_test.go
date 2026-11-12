package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func nextLiveResponse(t *testing.T, events <-chan domain.Event) domain.LiveResponse {
	t.Helper()
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal("live subscription closed")
			}

			if event.Type == "response" {
				var response domain.LiveResponse
				if err := decode(event.Data, &response); err != nil {
					t.Fatal(err)
				}

				return response
			}
		case <-timeout.C:
			t.Fatal("no live response")
		}
	}
}

func TestRetriedResponseReplacesDraftInsteadOfDuplicatingText(t *testing.T) {
	engine, record := testEngine(t, nil)
	events, unsubscribe := engine.Subscribe()
	defer unsubscribe()
	writer := engine.beginResponse(record.ID)
	start := nextLiveResponse(t, events)
	writer.append("old timed-out generation")
	writer.reset()
	reset := nextLiveResponse(t, events)
	if !reset.Snapshot {
		reset = nextLiveResponse(t, events)
	}
	if reset.ID != start.ID || !reset.Snapshot || reset.Text != "" || reset.Revision <= start.Revision {
		t.Fatal(reset)
	}
	writer.append("replacement generation")
	writer.finish("completed", "replacement generation")
	finished := nextLiveResponse(t, events)
	if !finished.Snapshot {
		finished = nextLiveResponse(t, events)
	}
	if finished.Status != "completed" || !finished.Snapshot || finished.Text != "replacement generation" {
		t.Fatal(finished)
	}
}

func TestLiveResponsesAreTransientOrderedAndRecoverable(t *testing.T) {
	engine, agent := testEngine(t, nil)
	events, unsubscribe := engine.Subscribe()
	defer unsubscribe()

	writer := engine.beginResponse(agent.ID)
	start := nextLiveResponse(t, events)
	if !start.Snapshot || start.Text != "" || start.Status != "running" {
		t.Fatal(start)
	}

	for range 100 {
		writer.append("é🙂")
	}

	delta := nextLiveResponse(t, events)
	if delta.Snapshot ||
		delta.Delta != strings.Repeat("é🙂", 100) ||
		delta.Revision != 1 ||
		delta.Text != "" {
		t.Fatal(delta)
	}

	reconnected, closeReconnect := engine.Subscribe()
	defer closeReconnect()

	snapshot := nextLiveResponse(t, reconnected)
	if snapshot.Text != delta.Delta || !snapshot.Snapshot || snapshot.Revision != 1 {
		t.Fatal(snapshot)
	}

	writer.append(" tail")
	writer.finish("completed", snapshot.Text+" tail")
	end := nextLiveResponse(t, events)
	if end.Status != "completed" || end.Text != snapshot.Text+" tail" || !end.Snapshot {
		t.Fatal(end)
	}

	rows, err := engine.Store.Events(0, 500)
	if err != nil {
		t.Fatal(err)
	}

	for _, event := range rows {
		if event.Type == "response" {
			t.Fatal("token frames were persisted to the database")
		}
	}

	engine.mu.Lock()
	remaining := len(engine.liveResponses)
	engine.mu.Unlock()

	if remaining != 0 {
		t.Fatal("finished draft retained", remaining)
	}
}

func TestCancelledLiveResponseDoesNotBecomeConversationHistory(t *testing.T) {
	engine, agent := testEngine(t, providerFunction(func(ctx context.Context, request providers.Request) (providers.Result, error) {
		request.OnText("Partial visible answer")
		<-ctx.Done()

		return providers.Result{}, ctx.Err()
	}))

	events, unsubscribe := engine.Subscribe()
	defer unsubscribe()

	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, _, err := engine.completeVisible(ctx, agent.ID, providers.Request{})
		finished <- err
	}()
	nextLiveResponse(t, events)

	if update := nextLiveResponse(t, events); update.Delta != "Partial visible answer" {
		t.Fatal(update)
	}

	cancel()

	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	last := nextLiveResponse(t, events)
	if last.Status != "interrupted" || last.Text != "Partial visible answer" {
		t.Fatal(last)
	}

	messages, err := engine.loadHistory(agent.ID)
	if err != nil || len(messages) != 0 {
		t.Fatal("partial generation entered model history", messages, err)
	}
}
