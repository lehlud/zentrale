package agent

import (
	"errors"
	"unicode/utf8"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) note(agentRecord domain.Agent, action string, args map[string]any) (any, error) {
	rows, err := engine.listAll("note", &agentRecord.ID)
	if err != nil {
		return nil, err
	}
	id := stringArg(args, "id")
	switch action {
	case "note_list":
		titles := []map[string]any{}
		for _, n := range rows {
			titles = append(titles, map[string]any{"id": n["id"], "title": n["title"]})
		}
		return titles, nil
	case "note_read", "note_delete":
		var n domain.Note
		if err := engine.Store.Load("note", id, &n); err != nil {
			return nil, err
		}
		if n.AgentID != agentRecord.ID {
			return nil, errors.New("note belongs to another agent")
		}
		if action == "note_delete" {
			return true, engine.Store.Remove("note", id)
		}
		return n, nil
	case "note_save":
		if id == "" {
			if len(rows) >= 10 {
				return nil, errors.New("maximum 10 notes")
			}
			id = NewID()
		} else {
			var n domain.Note
			if err := engine.Store.Load("note", id, &n); err != nil {
				return nil, err
			}
			if n.AgentID != agentRecord.ID {
				return nil, errors.New("note belongs to another agent")
			}
		}
		n := domain.Note{
			ID:      id,
			AgentID: agentRecord.ID,
			Title:   stringArg(args, "title"),
			Content: stringArg(args, "content"),
		}
		if utf8.RuneCountInString(n.Title) > 30 || n.Title == "" || utf8.RuneCountInString(n.Content) > 350 {
			return nil, errors.New("note limit: title 1–30, content 350 characters")
		}
		return n, engine.Store.Save("note", id, agentRecord.ID, n)
	}
	return nil, errors.New("unknown note action")
}
