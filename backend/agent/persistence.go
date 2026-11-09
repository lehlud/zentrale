package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) listAll(kind string, parent *string, fields ...string) ([]map[string]any, error) {
	result := []map[string]any{}
	for offset := 0; ; offset += 500 {
		rows, err := engine.Store.ListFields(kind, parent, 500, offset, fields)
		if err != nil {
			return nil, err
		}

		result = append(result, rows...)

		if len(rows) < 500 {
			return result, nil
		}
	}
}

func (engine *Engine) Emit(kind, agentID string, value any) {
	event, err := engine.Store.AppendEvent(kind, agentID, JSON(value))
	if err != nil {
		return
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()

	for ch := range engine.subscribers {
		select {
		case ch <- event:
		default:
			close(ch)
			delete(engine.subscribers, ch)
		}
	}
}

func (engine *Engine) Subscribe() (chan domain.Event, func()) {
	engine.mu.Lock()
	ch := make(chan domain.Event, 129+len(engine.liveResponses))
	// Reconnecting clients first discard stale drafts, then receive the current
	// snapshots. Registration and snapshots share the same lock as live updates.
	ch <- domain.Event{Type: "response_reset"}

	for _, response := range engine.liveResponses {
		ch <- liveResponseEvent(response)
	}

	engine.subscribers[ch] = true
	engine.mu.Unlock()

	return ch, func() {
		engine.mu.Lock()
		defer engine.mu.Unlock()

		if engine.subscribers[ch] {
			delete(engine.subscribers, ch)
			close(ch)
		}
	}
}

func (engine *Engine) saveAgent(agentRecord domain.Agent) error {
	if err := engine.Store.Save("agent", agentRecord.ID, agentRecord.ProjectID, agentRecord); err != nil {
		return err
	}

	engine.Emit("agent", agentRecord.ID, agentRecord)

	return nil
}

func (engine *Engine) appendMessage(agentID, role, content, wire, callID, toolName string, changes ...domain.FileChange) error {
	message := domain.Message{
		ID:        NewID(),
		AgentID:   agentID,
		Role:      role,
		Content:   content,
		Wire:      wire,
		CallID:    callID,
		ToolName:  toolName,
		CreatedAt: time.Now().UTC(),
	}

	return engine.persistMessage(message, changes, nil)
}

func (engine *Engine) persistMessage(
	message domain.Message,
	changes []domain.FileChange,
	activities []domain.ToolActivity,
) error {
	agentID := message.AgentID

	for index, change := range changes {
		change.ID = message.ID

		if index > 0 {
			change.ID = fmt.Sprintf("%s-%03d", message.ID, index)
		}

		change.MessageID = message.ID
		change.AgentID = agentID
		change.CreatedAt = message.CreatedAt

		if err := engine.Store.Save("file_change", change.ID, agentID, change); err != nil {
			return err
		}
	}

	if len(activities) > 0 {
		record := map[string]any{"id": message.ID, "agentId": agentID, "activities": activities}
		if err := engine.Store.Save("tool_activity", message.ID, agentID, record); err != nil {
			return err
		}
	}

	if err := engine.Store.Save("message", message.ID, agentID, message); err != nil {
		return err
	}

	engine.Emit("message", agentID, message)

	return nil
}

func (engine *Engine) loadHistory(agentID string) ([]domain.Message, error) {
	rows, err := engine.listAll("message", &agentID)
	if err != nil {
		return nil, err
	}

	var messages []domain.Message
	err = json.Unmarshal([]byte(JSON(rows)), &messages)

	return messages, err
}
