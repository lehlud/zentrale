package webapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/remote"
)

// Unlike non-browser API calls, browser terminals must always supply an exact
// Origin. Do not trust forwarded headers to widen the accepted origin.
func (server *Server) terminalOrigin(request *http.Request) bool {
	origin := request.Header.Get("Origin")
	parsed, err := url.Parse(origin)
	if err != nil || origin == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return false
	}
	if server.Origin != "" {
		return origin == server.Origin
	}
	scheme := "http"
	if request.TLS != nil || server.SecureCookie {
		scheme = "https"
	}
	return parsed.Scheme == scheme && parsed.Host == request.Host
}

type terminalFrame struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func terminalGeometry(request *http.Request) (int, int, error) {
	values := request.URL.Query()
	dimensions := []int{80, 24}
	for index, key := range []string{"cols", "rows"} {
		if raw, present := values[key]; present {
			if len(raw) != 1 {
				return 0, 0, errors.New("invalid terminal geometry")
			}
			value, err := strconv.Atoi(raw[0])
			if err != nil {
				return 0, 0, errors.New("invalid terminal geometry")
			}
			dimensions[index] = value
		}
	}
	if !remote.ValidTerminalSize(dimensions[0], dimensions[1]) {
		return 0, 0, errors.New("terminal dimensions must be between 1 and 1000")
	}
	return dimensions[0], dimensions[1], nil
}

func (server *Server) terminal(requestContext echo.Context) error {
	request := requestContext.Request()
	if !server.Auth.Valid(server.token(request)) {
		return echo.ErrUnauthorized
	}
	if !server.terminalOrigin(request) {
		return echo.ErrForbidden
	}
	cols, rows, err := terminalGeometry(request)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// Browsers hide the HTTP status of rejected WebSocket handshakes. This
	// authenticated, same-origin probe uses the very same guards and route,
	// but never provisions a VM or opens a shell.
	if request.Method == http.MethodPost {
		return requestContext.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
	connection, err := (&websocket.Upgrader{CheckOrigin: server.terminalOrigin, ReadBufferSize: 4096, WriteBufferSize: 32768}).Upgrade(requestContext.Response(), request, nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(request.Context())
	defer cancel()
	var writer sync.Mutex
	write := func(kind int, data []byte) error {
		writer.Lock()
		defer writer.Unlock()
		_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return connection.WriteMessage(kind, data)
	}
	event := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return write(websocket.TextMessage, data)
	}
	fail := func(err error) { _ = event(map[string]string{"type": "error", "message": err.Error()}) }
	connection.SetReadLimit(65536)
	_ = connection.SetReadDeadline(time.Now().Add(75 * time.Second))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(75 * time.Second))
	})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(25 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					cancel()
					connection.Close()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-heartbeatDone }()
	frames := make(chan terminalFrame, 8)
	readerDone := make(chan struct{})
	// Read immediately: closing a tab during boot must cancel provisioning too.
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			kind, data, err := connection.ReadMessage()
			if err != nil {
				return
			}
			var frame terminalFrame
			if kind == websocket.BinaryMessage {
				frame = terminalFrame{Type: "input", Data: string(data)}
			} else if kind == websocket.TextMessage {
				if err := json.Unmarshal(data, &frame); err != nil {
					fail(errors.New("invalid terminal message"))
					return
				}
			} else {
				fail(errors.New("unsupported terminal message"))
				return
			}
			if (frame.Type != "input" && frame.Type != "resize") || (frame.Type == "input" && len(frame.Data) > 65536) || (frame.Type == "resize" && !remote.ValidTerminalSize(frame.Cols, frame.Rows)) {
				fail(errors.New("invalid terminal input or resize"))
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			default:
				fail(errors.New("terminal input queue exceeded"))
				return
			}
		}
	}()
	defer func() { cancel(); connection.Close(); <-readerDone }()
	if err := event(map[string]string{"type": "status", "status": "starting"}); err != nil {
		return nil
	}
	err = server.Engine.Terminal(ctx, requestContext.Param("project"), request.URL.Query().Get("agent"), cols, rows, func(sessionContext context.Context, terminal *remote.Terminal) error {
		stop := context.AfterFunc(sessionContext, func() { connection.Close() })
		defer stop()
		if err := event(map[string]string{"type": "status", "status": "ready"}); err != nil {
			return err
		}
		inputDone := make(chan struct{})
		inputContext, stopInput := context.WithCancel(sessionContext)
		defer func() { stopInput(); terminal.Close(); <-inputDone }()
		go func() {
			defer close(inputDone)
			for {
				select {
				case <-inputContext.Done():
					return
				case frame := <-frames:
					var err error
					if frame.Type == "input" {
						err = terminal.Input([]byte(frame.Data))
					} else {
						err = terminal.Resize(frame.Cols, frame.Rows)
					}
					if err != nil {
						cancel()
						terminal.Close()
						return
					}
				}
			}
		}()
		buffer := make([]byte, 32768)
		for {
			count, readError := terminal.Read(buffer)
			if count > 0 {
				if err := write(websocket.BinaryMessage, buffer[:count]); err != nil {
					return err
				}
			}
			if readError != nil {
				if sessionContext.Err() != nil {
					return sessionContext.Err()
				}
				if !errors.Is(readError, io.EOF) {
					return readError
				}
				return event(struct {
					Type string `json:"type"`
					Code *int   `json:"code,omitempty"`
				}{"exit", terminal.Exit(sessionContext)})
			}
		}
	})
	if err != nil && ctx.Err() == nil {
		fail(err)
	}
	return nil
}
