package webapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
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
	Type     string `json:"type"`
	Data     string `json:"data"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Sequence uint64 `json:"sequence"`
	Encoding string `json:"encoding"`
}

func terminalWebSocketReadReason(err error) string {
	if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return "browser_closed"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "websocket_heartbeat_timeout"
	}
	return "websocket_read_error"
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
	attachmentID := request.URL.Query().Get("session")
	var cursor int64
	if raw := request.URL.Query().Get("cursor"); raw != "" {
		cursor, err = strconv.ParseInt(raw, 10, 64)
	}
	if err != nil || cursor < 0 || (attachmentID != "" && !remote.ValidID(attachmentID)) {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid terminal attachment")
	}
	if err := server.projectURLAccess(requestContext, "project", false, true); err != nil {
		return err
	}

	var explicitlyClosed atomic.Bool
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
	started := time.Now()
	var receiptMu sync.Mutex
	phase, endReason := "starting", ""
	markEnd := func(reason string) {
		receiptMu.Lock()
		if endReason == "" {
			endReason = reason
		}
		receiptMu.Unlock()
	}
	defer func() {
		receiptMu.Lock()
		defer receiptMu.Unlock()
		// Only fixed categories/phase names and identifiers: never log shell
		// input, output, error text (which may contain output), or credentials.
		slog.Info("terminal session ended", "project", requestContext.Param("project"),
			"agent", request.URL.Query().Get("agent"), "phase", phase,
			"reason", endReason, "duration_ms", time.Since(started).Milliseconds())
	}()
	var writer sync.Mutex
	write := func(kind int, data []byte) error {
		writer.Lock()
		defer writer.Unlock()
		_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		err := connection.WriteMessage(kind, data)
		if err != nil {
			markEnd("websocket_write_error")
		}
		return err
	}
	event := func(value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return write(websocket.TextMessage, data)
	}
	fail := func(err error) { _ = event(map[string]string{"type": "error", "message": err.Error()}) }
	ctx = remote.WithStartupProgress(ctx, func(status string) {
		receiptMu.Lock()
		phase = status
		receiptMu.Unlock()
		if err := event(map[string]string{"type": "status", "status": status}); err != nil {
			cancel()
			connection.Close()
		}
	})
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
				if err := server.projectURLAccess(requestContext, "project", false, true); err != nil {
					markEnd("project_access_revoked")
					cancel()
					connection.Close()
					return
				}

				if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					markEnd("websocket_ping_failed")
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
				markEnd(terminalWebSocketReadReason(err))
				return
			}
			if err := server.projectURLAccess(requestContext, "project", false, true); err != nil {
				markEnd("project_access_revoked")
				return
			}

			var frame terminalFrame
			if kind == websocket.BinaryMessage {
				frame = terminalFrame{Type: "input", Data: string(data)}
			} else if kind == websocket.TextMessage {
				if err := json.Unmarshal(data, &frame); err != nil {
					markEnd("invalid_frame")
					fail(errors.New("invalid terminal message"))
					return
				}
			} else {
				markEnd("invalid_frame")
				fail(errors.New("unsupported terminal message"))
				return
			}
			// JavaScript cannot observe native WebSocket ping/pong. Answer its
			// liveness probe during VM preparation too, never via the shell or
			// the bounded terminal input queue.
			if frame.Type == "ping" {
				if err := event(map[string]string{"type": "pong"}); err != nil {
					return
				}
				continue
			}
			if frame.Type == "close" {
				explicitlyClosed.Store(true)
				markEnd("user_closed")
				return
			}
			if frame.Encoding == "base64" {
				decoded, err := base64.StdEncoding.DecodeString(frame.Data)
				if err != nil {
					fail(errors.New("invalid terminal input encoding"))
					return
				}
				frame.Data = string(decoded)
			} else if frame.Encoding != "" {
				fail(errors.New("unsupported terminal input encoding"))
				return
			}
			if (frame.Type != "input" && frame.Type != "resize") || (frame.Type == "input" && len(frame.Data) > 65536) || (frame.Type == "resize" && !remote.ValidTerminalSize(frame.Cols, frame.Rows)) {
				markEnd("invalid_frame")
				fail(errors.New("invalid terminal input or resize"))
				return
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			default:
				markEnd("input_queue_exceeded")
				fail(errors.New("terminal input queue exceeded"))
				return
			}
		}
	}()
	defer func() { cancel(); connection.Close(); <-readerDone }()
	if err := event(map[string]string{"type": "status", "status": "starting"}); err != nil {
		return nil
	}
	err = server.Engine.TerminalAttached(ctx, requestContext.Param("project"), request.URL.Query().Get("agent"), agent.TerminalAttachment{ID: attachmentID, OutputOffset: cursor, KeepOnDisconnect: func() bool { return !explicitlyClosed.Load() }}, cols, rows, func(sessionContext context.Context, terminal *remote.Terminal) error {
		stop := context.AfterFunc(sessionContext, func() { connection.Close() })
		defer stop()
		receiptMu.Lock()
		phase = "ready"
		receiptMu.Unlock()
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
					err = terminal.AcceptInput(frame.Sequence, func() error {
						if frame.Type == "input" {
							return terminal.Input([]byte(frame.Data))
						}
						return terminal.Resize(frame.Cols, frame.Rows)
					})
					if err == nil && frame.Sequence > 0 {
						err = event(map[string]any{"type": "ack", "sequence": frame.Sequence})
					}
					if err != nil {
						markEnd("ssh_input_error")
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
					markEnd("session_canceled")
					return sessionContext.Err()
				}
				if !errors.Is(readError, io.EOF) {
					markEnd("ssh_read_error")
					return readError
				}
				code := terminal.Exit(sessionContext)
				if code == nil {
					markEnd("ssh_exit_status_missing")
					return errors.New("terminal SSH connection closed without an exit status")
				}
				markEnd("shell_exit")
				return event(struct {
					Type string `json:"type"`
					Code int    `json:"code"`
				}{"exit", *code})
			}
		}
	})
	markEnd("environment_or_terminal_error")
	if err != nil && ctx.Err() == nil {
		fail(err)
	}
	return nil
}
