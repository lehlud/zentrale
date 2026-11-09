package agent

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) RenameAgent(agentID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > 200 || strings.ContainsAny(name, "\x00\r\n") {
		return errors.New("conversation name must contain 1–200 characters without line breaks")
	}
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return err
	}
	engine.mu.Lock()
	busy := engine.running[agentID] != nil || record.Status == "running" || record.Status == "waiting"
	engine.mu.Unlock()
	if busy {
		return errors.New("stop the agent before renaming the conversation")
	}
	if err := engine.Store.Patch("agent", agentID, map[string]any{"name": name}); err != nil {
		return err
	}
	engine.Emit("resources", agentID, nil)
	return nil
}

// A new conversation gets a useful title from its first instruction without a
// separate naming form or an extra model request. Explicit names stay intact.
func (engine *Engine) nameFirstConversation(record *domain.Agent, text string) error {
	if record.Name != "Neue Unterhaltung" && record.Name != "New conversation" {
		return nil
	}
	rows, err := engine.Store.List("message", &record.ID, 1, 0)
	if err != nil || len(rows) != 0 {
		return err
	}
	title := []rune(strings.Join(strings.Fields(text), " "))
	if len(title) > 70 {
		title = append(title[:69], '…')
	}
	record.Name = string(title)
	return nil
}
