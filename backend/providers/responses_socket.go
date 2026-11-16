package providers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var errSocketUnsupported = errors.New("Responses WebSocket is unavailable")

// A socket belongs to one turn and account/model/thread. Only hashes of prior
// items are retained locally; server response IDs never enter stored history.
type responsesSocket struct {
	mutex      sync.Mutex
	connection *websocket.Conn
	idle       *time.Timer
	owner      [32]byte
	options    [32]byte
	prefix     [][32]byte
	responseID string
}

func (socket *responsesSocket) close() {
	socket.mutex.Lock()
	defer socket.mutex.Unlock()
	if socket.idle != nil {
		socket.idle.Stop()
	}
	if socket.connection != nil {
		_ = socket.connection.Close()
		socket.connection = nil
	}
}

func (client *HTTPClient) responsesTransport(ctx context.Context, request Request, body map[string]any) (map[string]any, error) {
	if client.useResponsesSocket(request) {
		data, err := client.responsesWebSocket(ctx, request, body)
		if !errors.Is(err, errSocketUnsupported) {
			return data, err
		}
		client.disableFeature(request, "websocket")
	}
	return client.post(ctx, request.Config, "/responses", body, request.stream())
}

func (client *HTTPClient) responsesWebSocket(ctx context.Context, request Request, body map[string]any) (map[string]any, error) {
	turn, _ := ctx.Value(responsesTurnKey{}).(*responsesTurn)
	if turn == nil {
		ctx = WithResponsesTurn(ctx)
		defer CloseResponsesTurn(ctx)
		turn = ctx.Value(responsesTurnKey{}).(*responsesTurn)
	}
	select {
	case turn.wsGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-turn.wsGate }()
	turn.wsMutex.Lock()
	if turn.socket == nil {
		turn.socket = &responsesSocket{}
	}
	socket := turn.socket
	turn.wsMutex.Unlock()
	owner := sha256.Sum256([]byte(fmt.Sprintf("%x:%s", responsesOwner(request), request.CacheKey)))
	socket.mutex.Lock()
	if socket.idle != nil {
		socket.idle.Stop()
	}
	sameOwner := socket.owner == owner
	socket.mutex.Unlock()
	if !sameOwner {
		socket.close()
		socket.owner = owner
		socket.prefix, socket.responseID = nil, ""
	}

	// Match request options and the entire old input+output prefix, not merely
	// message counts. Compaction, steering, changed tools or settings start fresh.
	options := map[string]any{}
	for key, value := range body {
		if key != "input" && key != "stream" {
			options[key] = value
		}
	}
	encodedOptions, _ := json.Marshal(options)
	optionsHash := sha256.Sum256(encodedOptions)
	input := asArray(body["input"])
	inputHashes := responseItemHashes(input)
	canContinue := socket.responseID != "" && socket.options == optionsHash && len(inputHashes) >= len(socket.prefix)
	if canContinue {
		for index, hash := range socket.prefix {
			if inputHashes[index] != hash {
				canContinue = false
				break
			}
		}
	}
	var totalWait time.Duration
	for attempt := 0; attempt < providerAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		started := time.Now()
		activityCtx, activity := watchProviderActivity(ctx, client.idleTimeout)
		cancelRead := context.AfterFunc(activityCtx, socket.close)
		data, sent, status, published, failure := client.socketAttempt(activityCtx, request, body, socket, input, canContinue, activity)
		cancelRead()
		cause := context.Cause(activityCtx)
		activity.close()
		if ctx.Err() != nil {
			failure = ctx.Err()
		} else if errors.Is(cause, errProviderIdle) {
			failure = errProviderIdle
		}
		observeAttempt(ctx, "websocket", started, sent, status, data, failure)
		if failure == nil {
			socket.options = optionsHash
			socket.responseID = asString(data["id"])
			socket.prefix = append(inputHashes, responseItemHashes(asArray(data["output"]))...)
			socket.mutex.Lock()
			socket.idle = time.AfterFunc(2*time.Minute, func() {
				select {
				case turn.wsGate <- struct{}{}:
					socket.close()
					<-turn.wsGate
				default:
				}
			})
			socket.mutex.Unlock()
			return data, nil
		}
		socket.close()
		var responseError *ResponseError
		missingPrevious := canContinue && errors.As(failure, &responseError) && responseError.Code == "previous_response_not_found"
		canContinue = false
		if errors.Is(failure, errSocketUnsupported) || ctx.Err() != nil {
			return nil, failure
		}
		if attempt+1 == providerAttempts || (!missingPrevious && !temporaryProviderFailure(failure)) || published && request.OnRetry == nil {
			return nil, failure
		}
		delay := time.Duration(0)
		if !missingPrevious {
			delay = time.Second * time.Duration(1<<attempt)
		}
		var httpFailure *HTTPError
		if errors.As(failure, &httpFailure) {
			received := httpFailure.ReceivedAt
			if received.IsZero() {
				received = started
			}
			if advice, ok := providerRetryAfter(httpFailure.RetryAfter, received); ok {
				delay = max(0, advice-time.Since(received))
			}
		}
		if totalWait+delay > maximumRetryWait {
			return nil, failure
		}
		totalWait += delay
		if request.OnRetry != nil {
			request.OnRetry(Retry{Attempt: attempt + 2, MaxAttempts: providerAttempts, Delay: delay})
		}
		wait := client.waitRetry
		if wait == nil {
			wait = waitForProviderRetry
		}
		if err := wait(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, errStreamInterrupted
}

func (client *HTTPClient) socketAttempt(ctx context.Context, request Request, body map[string]any, socket *responsesSocket, input []any, incremental bool, activity *providerActivity) (map[string]any, int, int, bool, error) {
	socket.mutex.Lock()
	connection := socket.connection
	socket.mutex.Unlock()
	if connection == nil {
		address := strings.TrimRight(request.Config.BaseURL, "/") + "/responses"
		handshake, err := http.NewRequestWithContext(ctx, "GET", address, nil)
		if err != nil {
			return nil, 0, 0, false, err
		}
		credential := request.Config.Credential
		if request.Config.OAuth != nil {
			credential = request.Config.OAuth.AccessToken
			handshake.Header.Set("originator", "zentrale")
			if request.Config.OAuth.AccountID != "" {
				handshake.Header.Set("ChatGPT-Account-Id", request.Config.OAuth.AccountID)
			}
		}
		if credential != "" {
			handshake.Header.Set("Authorization", "Bearer "+credential)
		}
		if request.Config.AuthMode == "device" {
			handshake.Header.Set("OpenAI-Beta", "responses_websockets=2026-02-06")
		}
		handshake = bindResponsesRoute(handshake, request.Config, body)
		applyResponsesRoute(handshake)
		handshake.URL.Scheme = strings.Replace(handshake.URL.Scheme, "http", "ws", 1)
		dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 30 * time.Second, Jar: client.HTTP.Jar}
		if transport, ok := client.HTTP.Transport.(*http.Transport); ok {
			dialer.Proxy, dialer.NetDialContext, dialer.TLSClientConfig = transport.Proxy, transport.DialContext, transport.TLSClientConfig
			dialer.NetDialTLSContext = transport.DialTLSContext
		}
		var response *http.Response
		connection, response, err = dialer.DialContext(ctx, handshake.URL.String(), handshake.Header)
		if err != nil {
			status := 0
			if response != nil {
				status = response.StatusCode
				if response.Body != nil {
					_ = response.Body.Close()
				}
			}
			switch status {
			case 400, 401, 403, 404, 405, 426, 501:
				return nil, 0, status, false, errSocketUnsupported
			}
			if status != 0 {
				return nil, 0, status, false, &HTTPError{Status: status, RetryAfter: response.Header.Get("Retry-After"), ReceivedAt: time.Now()}
			}
			return nil, 0, status, false, err
		}
		captureResponsesRoute(handshake, response)
		connection.SetReadLimit(maximumJSONResponseBytes)
		socket.mutex.Lock()
		// Cancellation may have happened just as Dial returned.
		if ctx.Err() != nil {
			socket.mutex.Unlock()
			_ = connection.Close()
			return nil, 0, 101, false, ctx.Err()
		}
		socket.connection = connection
		socket.mutex.Unlock()
		incremental = false // A new socket cannot reuse store=false state.
	}
	activity.touch()
	event := map[string]any{"type": "response.create"}
	for key, value := range body {
		if key != "stream" {
			event[key] = value
		}
	}
	if request.Config.AuthMode != "device" {
		event["stream_id"] = "main"
	}
	if incremental {
		event["previous_response_id"] = socket.responseID
		event["input"] = input[len(socket.prefix):]
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return nil, 0, 101, false, err
	}
	sentBytes := len(encoded)
	if err = connection.WriteMessage(websocket.TextMessage, encoded); err != nil {
		return nil, len(encoded), 101, false, errStreamInterrupted
	}
	published := false
	stream := responsesStream{request: streamRequest{onText: func(text string) {
		if request.OnText != nil {
			published = published || text != ""
			request.OnText(text)
		}
	}}}
	readBytes := 0
	for {
		_, encoded, err := connection.ReadMessage()
		if err != nil {
			return nil, sentBytes, 101, published, errStreamInterrupted
		}
		activity.touch()
		readBytes += len(encoded)
		if readBytes > 32*1024*1024 {
			return nil, sentBytes, 101, published, errors.New("provider WebSocket response too large")
		}
		var event map[string]any
		if json.Unmarshal(encoded, &event) != nil {
			return nil, sentBytes, 101, published, errors.New("invalid Responses WebSocket event")
		}
		if event["type"] == "message" {
			if wrapped, ok := event["message"].(map[string]any); ok {
				event = wrapped
			}
		}
		if event["error"] != nil || event["type"] == "error" {
			failure := responseFailure(event)
			if !published && (failure.Code == "unsupported_transport" || failure.Code == "websocket_not_supported") {
				return nil, sentBytes, 101, published, errSocketUnsupported
			}
			return nil, sentBytes, 101, published, failure
		}
		done, failure := stream.consume(event)
		if failure != nil {
			return asObject(event["response"]), sentBytes, 101, published, failure
		}
		if done {
			result, failure := stream.result()
			if failure == nil && (result["error"] != nil || result["status"] == "failed" || result["status"] == "incomplete") {
				failure = responseFailure(result)
			}
			return result, sentBytes, 101, published, failure
		}
	}
}

func responseItemHashes(items []any) [][32]byte {
	hashes := make([][32]byte, 0, len(items))
	for _, item := range items {
		encoded, _ := json.Marshal(item)
		hashes = append(hashes, sha256.Sum256(encoded))
	}
	return hashes
}
