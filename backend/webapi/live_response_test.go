package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type gatedStreamingProvider struct{ release <-chan struct{} }

func (provider gatedStreamingProvider) Complete(ctx context.Context, request providers.Request) (providers.Result, error) {
	if strings.Contains(request.System, "goal_completion_review") {
		return providers.Result{
			Text:       `{"status":"complete","reason":"Fixture finished","evidence":"The scripted stream completed"}`,
			UsageKnown: true,
		}, nil
	}

	request.OnText("First fragment ")

	select {
	case <-provider.release:
	case <-ctx.Done():
		return providers.Result{}, ctx.Err()
	}

	request.OnText("and final text.")

	return providers.Result{Text: "First fragment and final text.", Wire: "[]", UsageKnown: true}, nil
}

func readLiveEvent(t *testing.T, connection *websocket.Conn) domain.LiveResponse {
	t.Helper()
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))

	for {
		var event domain.Event
		if err := connection.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}

		if event.Type != "response" {
			continue
		}

		if event.ID != 0 {
			t.Fatal("a live frame consumed the durable event cursor", event.ID)
		}

		var response domain.LiveResponse
		if err := json.Unmarshal([]byte(event.Data), &response); err != nil {
			t.Fatal(err)
		}

		return response
	}
}

func TestAuthenticatedWebSocketStreamsBeforeCompletionAndRestoresDraft(t *testing.T) {
	fixture := newAPIFixture(t)
	release := make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()

	fixture.engine.Provider = gatedStreamingProvider{release: release}
	requireData(
		t,
		fixture.query(
			t,
			`mutation{setup(password:"correct test password",commitIdentity:{name:"Team",email:"team@example.test"})}`,
			nil,
		),
	)
	store := fixture.engine.Store
	key, err := store.GenerateSSHKey()
	if err != nil {
		t.Fatal(err)
	}

	machine := domain.Machine{
		ID:      "stream-worker",
		Name:    "Worker",
		Host:    fixture.ssh.Host,
		Port:    fixture.ssh.Port,
		User:    "developer",
		KeyID:   key.ID,
		Sandbox: "none",
	}

	project := domain.Project{
		ID:        "stream-project",
		Name:      "Project",
		Directory: "/srv/project",
		MachineIDs: []string{
			machine.ID,
		},
	}

	model := domain.LanguageModel{ID: "test", ProviderID: "stream-provider", ContextWindow: 128000}

	for _, record := range []struct {
		kind, id, parent string
		value            any
	}{
		{"machine", machine.ID, "", machine},
		{"project", project.ID, "", project},
		{
			"provider",
			"stream-provider",
			"",
			domain.Provider{
				ID:       "stream-provider",
				Kind:     "openai",
				Name:     "Provider",
				AuthMode: "api",
			},
		},
		{"model", "stream-provider:test", "stream-provider", model},
	} {
		if err := store.Save(record.kind, record.id, record.parent, record.value); err != nil {
			t.Fatal(err)
		}
	}

	choice := domain.ModelChoice{ProviderID: "stream-provider", ModelID: "test"}
	if err := fixture.engine.SaveModelDefaults(domain.ModelDefaults{
		Main:     choice,
		Approver: choice,
	}); err != nil {
		t.Fatal(err)
	}

	agent, err := fixture.engine.Create(project.ID, "Streaming", "stream-provider", "stream-provider", "")
	if err != nil {
		t.Fatal(err)
	}

	past, err := store.Events(0, 500)
	if err != nil {
		t.Fatal(err)
	}

	cursor := int64(0)

	if len(past) > 0 {
		cursor = past[len(past)-1].ID
	}

	connect := func() *websocket.Conn {
		headers := http.Header{}
		request, _ := http.NewRequest("GET", fixture.server.URL, nil)

		for _, cookie := range fixture.client.Jar.Cookies(request.URL) {
			headers.Add("Cookie", cookie.String())
		}

		connection, _, err := websocket.DefaultDialer.Dial(
			"ws"+strings.TrimPrefix(fixture.server.URL, "http")+fmt.Sprintf("/events?after=%d", cursor),
			headers,
		)
		if err != nil {
			t.Fatal(err)
		}

		return connection
	}

	connection := connect()
	defer connection.Close()

	if err := fixture.engine.PromptContext(context.Background(), agent.ID, "Stream a response"); err != nil {
		t.Fatal(err)
	}

	start := readLiveEvent(t, connection)
	partial := readLiveEvent(t, connection)
	if !start.Snapshot || partial.Delta != "First fragment " || partial.Snapshot {
		t.Fatal(start, partial)
	}

	reconnected := connect()
	defer reconnected.Close()

	snapshot := readLiveEvent(t, reconnected)
	if snapshot.ID != start.ID || snapshot.Text != partial.Delta || !snapshot.Snapshot {
		t.Fatal("reconnect lost or duplicated partial text", snapshot)
	}

	resume()
	completed := readLiveEvent(t, connection)
	if completed.Status != "completed" || completed.Text != "First fragment and final text." {
		t.Fatal(completed)
	}

	for {
		var event domain.Event
		if err := connection.ReadJSON(&event); err != nil {
			t.Fatal(err)
		}

		if event.Type != "message" {
			continue
		}

		var message domain.Message
		_ = json.Unmarshal([]byte(event.Data), &message)

		if message.Role == "assistant" {
			if message.ID != start.ID || message.Content != completed.Text || event.ID <= cursor {
				t.Fatal("draft did not become the same durable message", message, event)
			}

			break
		}
	}
}
