package agent

import (
	"strings"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func TestMainAnswersChildQuestionsDurablyWithoutOverridingUserPause(t *testing.T) {
	engine, parent := testEngine(t, nil)
	child, err := engine.Create(parent.ProjectID, "Child", parent.ProviderID, "approver-provider", parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{parent.ID, child.ID} {
		if err := engine.Stop(id); err != nil {
			t.Fatal(err)
		}
	}
	result, err := engine.question(child, "question_ask", map[string]any{"title": "Which implementation?", "options": []any{"A", "B"}})
	if err != nil {
		t.Fatal(err)
	}
	question := result.(domain.Question)
	listed, err := engine.listQuestions(parent)
	if err != nil || len(listed) != 1 || listed[0]["id"] != question.ID {
		t.Fatal(listed, err)
	}
	history, err := engine.loadHistory(parent.ID)
	if err != nil || len(history) != 1 || !strings.Contains(history[0].Content, "subagent_question") || !strings.Contains(history[0].Content, question.ID) {
		t.Fatal(history, err)
	}
	args := map[string]any{"id": question.ID, "answer": "Use A within the existing task."}
	for range 2 {
		answer, err := engine.answerChildQuestion(parent, args)
		if err != nil || answer.Status != "answered" || answer.Answer != args["answer"] {
			t.Fatal(answer, err)
		}
	}
	history, err = engine.loadHistory(child.ID)
	if err != nil || len(history) != 1 || history[0].Role != "event" || !strings.Contains(history[0].Content, "not new user authorization") {
		t.Fatal("answer was duplicated or misattributed as user input", history, err)
	}
	engine.mu.Lock()
	stopped := engine.stopRequested[child.ID]
	active := engine.running[child.ID] != nil
	engine.mu.Unlock()
	if !stopped || active {
		t.Fatal("answer resumed a user-paused child")
	}
	if _, err := engine.answerChildQuestion(parent, map[string]any{"id": question.ID, "answer": "Different answer"}); err == nil {
		t.Fatal("changed a closed answer")
	}
	if _, err := engine.answerChildQuestion(child, args); err == nil {
		t.Fatal("child answered a question")
	}
	foreign := parent
	foreign.ID = "other-main"
	if _, err := engine.answerChildQuestion(foreign, args); err == nil {
		t.Fatal("unrelated main answered a question")
	}
	foreign = parent
	foreign.ProjectID = "other-project"
	if _, err := engine.answerChildQuestion(foreign, args); err == nil {
		t.Fatal("answer crossed project boundary")
	}
	listed, err = engine.listQuestions(parent)
	if err != nil || len(listed) != 0 {
		t.Fatal("closed child question remains pending", listed, err)
	}
	if _, ok := reviewedTools["question_answer"]; !ok {
		t.Fatal("parent answers must be independently reviewed")
	}
}
