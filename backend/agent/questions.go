package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) question(agentRecord domain.Agent, action string, args map[string]any) (any, error) {
	if action == "question_list" {
		return engine.listAll("question", &agentRecord.ID)
	}

	if action == "question_withdraw" {
		return true, engine.AnswerQuestion(stringArg(args, "id"), "", "withdrawn", agentRecord.ID)
	}

	options, err := stringsArg(args, "options")
	if err != nil {
		options = []string{}
	}

	q := domain.Question{
		ID:      NewID(),
		AgentID: agentRecord.ID,
		Title:   stringArg(args, "title"),
		Options: options,
		Status:  "open",
	}
	if q.Title == "" || len(q.Title) > 2000 || len(options) > 10 {
		return nil, errors.New("invalid question")
	}

	if err := engine.Store.Save("question", q.ID, agentRecord.ID, q); err != nil {
		return nil, err
	}

	engine.Emit("question", agentRecord.ID, q)

	return q, nil
}

func (engine *Engine) AnswerQuestion(id, answer, status, owner string) error {
	var q domain.Question
	if err := engine.Store.Load("question", id, &q); err != nil {
		return err
	}

	if owner != "" && q.AgentID != owner {
		return errors.New("question belongs to another agent")
	}

	if q.Status != "open" {
		return errors.New("question is already closed")
	}

	if status != "answered" && status != "dismissed" && status != "withdrawn" {
		return errors.New("invalid question state")
	}

	if status == "answered" && strings.TrimSpace(answer) == "" {
		return errors.New("answer is required")
	}

	if owner == "" {
		if err := engine.requireConnection(engine.ctx, q.AgentID); err != nil {
			return err
		}
	}

	q.Status = status
	q.Answer = answer

	if err := engine.Store.Save("question", q.ID, q.AgentID, q); err != nil {
		return err
	}

	engine.Emit("question", q.AgentID, q)

	if owner == "" {
		return engine.Prompt(q.AgentID, fmt.Sprintf("Question %q was %s. Answer: %s", q.Title, status, answer))
	}

	return nil
}
