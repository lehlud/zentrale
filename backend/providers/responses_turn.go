package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/lehlud/zentrale/domain"
)

type responsesTurnKey struct{}
type responsesRouteKey struct{}

type responsesTurn struct {
	wsGate  chan struct{}
	wsMutex sync.Mutex
	socket  *responsesSocket
	mutex   sync.Mutex
	owner   [32]byte
	state   string
	epoch   uint64
}

type responsesRoute struct {
	turn  *responsesTurn
	owner [32]byte
	epoch uint64
}

// WithResponsesTurn starts an isolated routing lifetime. Reuse the returned
// context for continuations/inspections of this turn, never for a new user input
// or independent approval. State is request-local, opaque, and never persisted.
func WithResponsesTurn(ctx context.Context) context.Context {
	return context.WithValue(ctx, responsesTurnKey{}, &responsesTurn{wsGate: make(chan struct{}, 1)})
}

// Call at a turn/review boundary; close the transport without discarding any
// persisted conversation. A later turn starts from its full prepared context.
func CloseResponsesTurn(ctx context.Context) {
	if ctx == nil {
		return
	}
	turn, _ := ctx.Value(responsesTurnKey{}).(*responsesTurn)
	if turn == nil {
		return
	}
	turn.wsMutex.Lock()
	defer turn.wsMutex.Unlock()
	if turn.socket != nil {
		turn.socket.close()
	}
}

func bindResponsesRoute(request *http.Request, configuration domain.Provider, body map[string]any) *http.Request {
	// A caller may derive a new request from an earlier HTTP request's context.
	// Never inherit a binding into another protocol/endpoint or another turn.
	request = request.WithContext(context.WithValue(request.Context(), responsesRouteKey{}, responsesRoute{}))
	request.Header.Del("x-codex-turn-state")
	if configuration.Kind != "openai" || configuration.AuthMode != "device" ||
		configuration.OAuth == nil || !strings.HasSuffix(request.URL.Path, "/responses") {
		return request
	}

	key := asString(body["prompt_cache_key"])
	if key == "" || !validRoutingValue(key, 256) {
		return request
	}

	// These identify Zentrale's own thread/session; originator stays "zentrale".
	request.Header.Set("session-id", key)
	request.Header.Set("thread-id", key)

	turn, _ := request.Context().Value(responsesTurnKey{}).(*responsesTurn)
	if turn == nil {
		return request
	}

	account := configuration.OAuth.AccountID
	if account == "" {
		account = configuration.OAuth.AccessToken
	}

	encoded, _ := json.Marshal([]string{
		configuration.ID, configuration.BaseURL, configuration.AuthMode,
		account, asString(body["model"]), key,
	})
	owner := sha256.Sum256(encoded)
	turn.mutex.Lock()
	if turn.owner != owner {
		turn.owner, turn.state = owner, ""
		turn.epoch++
	}
	epoch := turn.epoch
	turn.mutex.Unlock()

	return request.WithContext(context.WithValue(request.Context(), responsesRouteKey{}, responsesRoute{turn: turn, owner: owner, epoch: epoch}))
}

func applyResponsesRoute(request *http.Request) {
	route, ok := request.Context().Value(responsesRouteKey{}).(responsesRoute)
	if !ok || route.turn == nil {
		return
	}

	route.turn.mutex.Lock()
	defer route.turn.mutex.Unlock()

	request.Header.Del("x-codex-turn-state")
	if route.turn.owner == route.owner && route.turn.epoch == route.epoch && route.turn.state != "" {
		request.Header.Set("x-codex-turn-state", route.turn.state)
	}
}

func captureResponsesRoute(request *http.Request, response *http.Response) {
	route, ok := request.Context().Value(responsesRouteKey{}).(responsesRoute)
	if !ok || route.turn == nil || (response.StatusCode != http.StatusSwitchingProtocols && (response.StatusCode < 200 || response.StatusCode >= 300)) {
		return
	}

	state := response.Header.Get("x-codex-turn-state")
	if state == "" || !validRoutingValue(state, 8192) {
		return
	}

	route.turn.mutex.Lock()
	defer route.turn.mutex.Unlock()

	// Like Codex's OnceLock: replay the first value unchanged within this turn.
	// A late response from a previous account/model must not replace new state.
	if route.turn.owner == route.owner && route.turn.epoch == route.epoch && route.turn.state == "" {
		route.turn.state = state
	}
}

func validRoutingValue(value string, limit int) bool {
	if len(value) > limit {
		return false
	}

	for _, char := range value {
		if char < 32 || char > 126 {
			return false
		}
	}

	return true
}
