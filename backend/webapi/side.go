package webapi

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/lehlud/zentrale/agent"
)

type sideQuestion struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
}

type sideAnswer struct {
	id   string
	text string
	err  error
}

// Browsers hide failed WebSocket handshake status codes. This session-only
// probe shares authentication without creating a session or retrying a question.
func (server *Server) sideStatus(request echo.Context) error {
	if !server.Auth.Valid(server.token(request.Request())) {
		return echo.ErrUnauthorized
	}

	if !server.sameOrigin(request.Request()) {
		return echo.ErrForbidden
	}

	return request.NoContent(http.StatusNoContent)
}

// One authenticated socket owns one ephemeral conversation. Read/pong handling
// stays live during inference, so disconnect cancels work instead of waiting for
// the model's answer. No unload request or persisted cleanup ticket is required.
func (server *Server) side(request echo.Context) error {
	token := server.token(request.Request())
	if !server.Auth.Valid(token) {
		return echo.ErrUnauthorized
	}

	if !server.sameOrigin(request.Request()) {
		return echo.ErrForbidden
	}

	ctx, cancel := context.WithTimeout(request.Request().Context(), 30*time.Minute)
	defer cancel()
	session, err := server.Engine.OpenSide(ctx, request.Param("agent"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	defer session.Close()

	upgrader := websocket.Upgrader{CheckOrigin: server.sameOrigin, ReadBufferSize: 1024, WriteBufferSize: 4096}
	connection, err := upgrader.Upgrade(request.Response(), request.Request(), nil)
	if err != nil {
		return err
	}
	defer connection.Close()
	connection.SetReadLimit(20000)
	_ = connection.SetReadDeadline(time.Now().Add(35 * time.Second))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(35 * time.Second))
	})

	var writeMu sync.Mutex
	write := func(value any) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		if !server.Auth.Valid(token) {
			_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(sessionExpired, "authentication required"), time.Now().Add(time.Second))
			cancel()
			return false
		}

		_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := connection.WriteJSON(value); err != nil {
			cancel()
			return false
		}

		return true
	}

	questions := make(chan sideQuestion, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		for {
			var question sideQuestion
			if err := connection.ReadJSON(&question); err != nil {
				return
			}

			select {
			case questions <- question:
			case <-ctx.Done():
				return
			}
		}
	}()

	var inference sync.WaitGroup
	defer func() {
		cancel()
		session.Cancel()
		_ = connection.Close()
		inference.Wait()
		<-readerDone
	}()

	answers := make(chan sideAnswer, 1)
	busy := false
	seen := map[string]bool{}
	heartbeat := time.NewTicker(5 * time.Second)
	defer heartbeat.Stop()
	if !write(map[string]any{"type": "ready", "readOnly": true}) {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-session.Done():
			return nil
		case <-heartbeat.C:
			if !server.Auth.Valid(token) {
				_ = connection.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(sessionExpired, "authentication required"), time.Now().Add(time.Second))
				return nil
			}

			if err := connection.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second)); err != nil {
				return nil
			}
		case question := <-questions:
			if question.Type != "question" || len(question.ID) == 0 || len(question.ID) > 64 || len(question.Text) > 16000 || busy || seen[question.ID] {
				if !write(map[string]any{"type": "error", "id": question.ID, "error": "Question invalid, duplicated or another answer is still running."}) {
					return nil
				}
				continue
			}

			if len(seen) >= 64 {
				write(map[string]any{"type": "error", "error": "Temporary conversation limit reached; reopen the modal."})
				return nil
			}

			seen[question.ID] = true
			busy = true
			inference.Add(1)
			go func(question sideQuestion) {
				defer inference.Done()
				text, err := session.Ask(question.Text, func(update agent.SideUpdate) {
					write(map[string]any{"type": update.Type, "id": question.ID, "text": update.Text})
				})
				select {
				case answers <- sideAnswer{id: question.ID, text: text, err: err}:
				case <-ctx.Done():
				}
			}(question)
		case answer := <-answers:
			busy = false
			message := map[string]any{"type": "done", "id": answer.id, "text": answer.text}
			if answer.err != nil {
				message = map[string]any{"type": "error", "id": answer.id, "error": answer.err.Error()}
			}

			if !write(message) {
				return nil
			}
		}
	}
}
