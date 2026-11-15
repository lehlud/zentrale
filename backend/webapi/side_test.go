package webapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type sideProvider func(context.Context, providers.Request) (providers.Result, error)

func (provider sideProvider) Complete(ctx context.Context, request providers.Request) (providers.Result, error) {
	return provider(ctx, request)
}

func sideFixture(t *testing.T, provider providers.Client) (*apiFixture, http.Header) {
	t.Helper()
	fixture := newAPIFixture(t)
	fixture.engine.Provider = provider
	requireData(t, fixture.query(t, `mutation{setup(password:"correct test password",commitIdentity:{name:"Test",email:"test@example.org"})}`, nil))

	resources := []struct {
		kind, id, parent string
		value            any
	}{
		{"provider", "side-provider", "", domain.Provider{ID: "side-provider", Kind: "openai"}},
		{"model", "side-provider:model", "side-provider", domain.LanguageModel{ID: "model", ProviderID: "side-provider", ContextWindow: 128000}},
		{"project", "side-project", "", domain.Project{ID: "side-project"}},
		{"agent", "side-main", "side-project", domain.Agent{ID: "side-main", ProjectID: "side-project", Role: "main", ProviderID: "side-provider", ModelID: "model"}},
	}
	for _, resource := range resources {
		if err := fixture.engine.Store.Save(resource.kind, resource.id, resource.parent, resource.value); err != nil {
			t.Fatal(err)
		}
	}

	address, err := url.Parse(fixture.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, fixture.server.URL, nil)
	for _, cookie := range fixture.client.Jar.Cookies(address) {
		request.AddCookie(cookie)
	}
	request.Header.Set("Origin", fixture.server.URL)
	return fixture, request.Header
}

func openSideSocket(t *testing.T, fixture *apiFixture, headers http.Header) *websocket.Conn {
	t.Helper()
	address := strings.Replace(fixture.server.URL, "http", "ws", 1) + "/side/side-main"
	connection, response, err := websocket.DefaultDialer.Dial(address, headers)
	if err != nil {
		t.Fatalf("side handshake: %v, response=%v", err, response)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))

	var ready map[string]any
	if err := connection.ReadJSON(&ready); err != nil || ready["type"] != "ready" || ready["readOnly"] != true {
		t.Fatal("missing read-only ready frame", ready, err)
	}
	return connection
}

func TestSideSocketAnswersWithoutStartingMainWork(t *testing.T) {
	fixture, headers := sideFixture(t, echoProvider{})
	connection := openSideSocket(t, fixture, headers)
	if err := connection.WriteJSON(sideQuestion{Type: "question", ID: "q1", Text: "What is the current state?"}); err != nil {
		t.Fatal(err)
	}

	var answer map[string]any
	if err := connection.ReadJSON(&answer); err != nil || answer["type"] != "done" || !strings.Contains(answer["text"].(string), "current state") {
		t.Fatal("side answer", answer, err)
	}
	rows, err := fixture.engine.Store.RecentMessages("side-main", 10)
	if err != nil || len(rows) != 0 {
		t.Fatal("side polluted main history", rows, err)
	}

	response, err := fixture.client.Head(fixture.server.URL + "/side/nonexistent")
	if err != nil || response.StatusCode != http.StatusNoContent {
		t.Fatal("status probe performed agent work", response, err)
	}
	response.Body.Close()
}

func TestSideSocketDisconnectCancelsInFlightInference(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	fixture, headers := sideFixture(t, sideProvider(func(ctx context.Context, _ providers.Request) (providers.Result, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		return providers.Result{}, ctx.Err()
	}))
	connection := openSideSocket(t, fixture, headers)
	if err := connection.WriteJSON(sideQuestion{Type: "question", ID: "q1", Text: "Explain status"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("inference not started")
	}

	connection.Close()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("side inference survived socket loss")
	}
}

func TestSideSocketRequiresSessionAndOriginAndClosesOnRevocation(t *testing.T) {
	fixture, headers := sideFixture(t, echoProvider{})
	address := strings.Replace(fixture.server.URL, "http", "ws", 1) + "/side/side-main"
	for _, test := range []struct {
		headers http.Header
		status  int
	}{
		{nil, http.StatusUnauthorized},
		{http.Header{"Cookie": headers.Values("Cookie"), "Origin": {"https://unrelated.example"}}, http.StatusForbidden},
	} {
		connection, response, err := websocket.DefaultDialer.Dial(address, test.headers)
		if connection != nil {
			connection.Close()
		}
		if err == nil || response == nil || response.StatusCode != test.status {
			t.Fatal("side boundary rejected incorrectly", response, err)
		}
		if response != nil {
			response.Body.Close()
		}
	}

	connection := openSideSocket(t, fixture, headers)
	requireData(t, fixture.query(t, `mutation{logout}`, nil))
	for {
		_, _, err := connection.ReadMessage()
		if err == nil {
			continue
		}
		var closed *websocket.CloseError
		if !errors.As(err, &closed) || closed.Code != sessionExpired {
			t.Fatalf("revoked side looked like a network failure: %v", err)
		}
		break
	}
}
