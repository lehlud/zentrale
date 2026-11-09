package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

const responseFrameInterval = 35 * time.Millisecond

type responseWriter struct {
	engine  *Engine
	mutex   sync.Mutex
	state   domain.LiveResponse
	text    strings.Builder
	pending strings.Builder
	timer   *time.Timer
	closed  bool
}

func liveResponseEvent(response domain.LiveResponse) domain.Event {
	return domain.Event{
		Type:      "response",
		AgentID:   response.AgentID,
		Data:      JSON(response),
		CreatedAt: time.Now().UTC(),
	}
}

func (engine *Engine) publishResponse(state, update domain.LiveResponse, finished bool) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	if finished {
		delete(engine.liveResponses, state.AgentID)
	} else {
		engine.liveResponses[state.AgentID] = state
	}

	event := liveResponseEvent(update)

	for channel := range engine.subscribers {
		select {
		case channel <- event:
		default:
			close(channel)
			delete(engine.subscribers, channel)
		}
	}
}

func (engine *Engine) beginResponse(agentID string) *responseWriter {
	writer := &responseWriter{
		engine: engine,
		state: domain.LiveResponse{
			ID:        NewID(),
			AgentID:   agentID,
			Status:    "running",
			Snapshot:  true,
			CreatedAt: time.Now().UTC(),
		},
	}

	engine.publishResponse(writer.state, writer.state, false)

	return writer
}

func (writer *responseWriter) append(text string) {
	if text == "" {
		return
	}

	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	if writer.closed {
		return
	}

	writer.text.WriteString(text)
	writer.pending.WriteString(text)

	if writer.timer == nil {
		writer.timer = time.AfterFunc(responseFrameInterval, writer.flush)
	}
}

func (writer *responseWriter) flush() {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	writer.timer = nil

	if writer.closed {
		return
	}

	writer.state.Revision++
	writer.state.Text = writer.text.String()
	update := writer.state
	update.Text, update.Delta, update.Snapshot = "", writer.pending.String(), false
	writer.pending.Reset()
	writer.engine.publishResponse(writer.state, update, false)
}

func (writer *responseWriter) finish(status, finalText string) {
	writer.mutex.Lock()
	defer writer.mutex.Unlock()

	writer.closed = true

	if writer.timer != nil {
		writer.timer.Stop()
	}

	writer.state.Revision++
	writer.state.Status = status
	writer.state.Text = writer.text.String()

	if status == "completed" {
		writer.state.Text = finalText
	}

	writer.engine.publishResponse(writer.state, writer.state, true)
}

// Partial text is display-only. Only a successfully completed response becomes
// a message, with the same ID so the UI can replace its draft without duplication.
func (engine *Engine) completeVisible(
	ctx context.Context,
	agentID string,
	request providers.Request,
) (result providers.Result, message domain.Message, err error) {
	writer := engine.beginResponse(agentID)
	request.OnText = writer.append
	defer func() {
		status := "completed"

		if err != nil {
			status = "failed"

			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				status = "interrupted"
			}
		}

		writer.finish(status, result.Text)
	}()

	result, err = engine.Provider.Complete(ctx, request)
	if err == nil {
		message = domain.Message{
			ID:      writer.state.ID,
			AgentID: agentID,
			Role:    "assistant",

			Content:   result.Text,
			Wire:      result.Wire,
			CreatedAt: time.Now().UTC(),
		}
	}

	return result, message, err
}
