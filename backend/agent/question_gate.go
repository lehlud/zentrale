package agent

import (
	"fmt"

	"github.com/lehlud/zentrale/domain"
)

// Waiting for an explicitly required answer is not an error, a user pause or a
// completed task. The durable question remains the gate across worker restarts.
type questionWaitingError struct {
	Questions []domain.Question
}

func (waiting *questionWaitingError) Error() string {
	return fmt.Sprintf("waiting for %d required question answer(s)", len(waiting.Questions))
}

func (engine *Engine) openQuestions(record domain.Agent) ([]domain.Question, error) {
	rows, err := engine.listQuestionRows(record, false)
	if err != nil {
		return nil, err
	}
	var questions []domain.Question
	if err := decode(JSON(rows), &questions); err != nil {
		return nil, err
	}

	open := make([]domain.Question, 0, len(questions))
	for _, question := range questions {
		if question.Status == "open" {
			open = append(open, question)
		}
	}
	return open, nil
}

// Only an agent's own questions gate its model loop. A main agent must remain
// able to answer a child's required question rather than waiting on itself.
func (engine *Engine) questionGate(agentID string) error {
	questions, err := engine.openQuestions(domain.Agent{ID: agentID})
	if err != nil {
		return err
	}
	required := []domain.Question{}
	for _, question := range questions {
		if question.RequiresAnswer() {
			required = append(required, question)
		}
	}
	if len(required) != 0 {
		return &questionWaitingError{Questions: required}
	}
	return nil
}
