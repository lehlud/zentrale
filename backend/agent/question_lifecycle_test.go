package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func TestAdvisoryQuestionAllowsIndependentWorkAndCompletion(t *testing.T) {
	var turns atomic.Int32
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		switch turns.Add(1) {
		case 1:
			return responseCalls(providers.Call{ID: "ask", Name: "question_ask", Arguments: map[string]any{
				"title": "Do you prefer a different label?",
			}}), nil
		case 2:
			return responseCalls(providers.Call{ID: "continue-work", Name: "harness", Arguments: map[string]any{
				"action": "note_save", "arguments": JSON(map[string]any{
					"title": "Independent work", "content": "Completed without choosing the optional label.",
				}),
			}}), nil
		default:
			return providers.Result{Text: "Independent work completed.", UsageKnown: true}, nil
		}
	}))
	if err := engine.Prompt(record.ID, "Complete independent work; ask about the optional label without waiting."); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" {
		t.Fatal(final)
	}
	questions, err := engine.openQuestions(record)
	if err != nil || len(questions) != 1 || questions[0].Mode != domain.QuestionAdvisory {
		t.Fatal("default advisory question did not stay open", questions, err)
	}
	notes, err := engine.listAll("note", &record.ID)
	if err != nil || len(notes) != 1 || turns.Load() != 3 {
		t.Fatal("open advisory question prevented independent work", notes, err, turns.Load())
	}
	goal, err := engine.CurrentGoal(record.ID)
	if err != nil || goal == nil || goal.Status != "complete" {
		t.Fatal("advisory question alone prevented completion", goal, err)
	}
	completedGoalID := goal.ID
	if err := engine.AnswerQuestion(questions[0].ID, "Keep the current label", "answered", ""); err != nil {
		t.Fatal(err)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" || turns.Load() != 4 {
		t.Fatal("answer after advisory completion was lost or replayed old work", final, turns.Load())
	}
	goal, err = engine.CurrentGoal(record.ID)
	if err != nil || goal == nil || goal.ID == completedGoalID || goal.Status != "complete" {
		t.Fatal("late answer reopened the completed task instead of its own durable input", goal, err)
	}
}

func TestBlockingQuestionWaitsAndLateAnswerResumesExactlyOnce(t *testing.T) {
	var turns atomic.Int32
	engine, record := testEngine(t, providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
		if turns.Add(1) == 1 {
			return responseCalls(
				providers.Call{ID: "ask", Name: "question_ask", Arguments: map[string]any{
					"title": "Which required destination?", "mode": "blocking", "options": []any{"A", "B"},
				}},
				providers.Call{ID: "must-not-run", Name: "harness", Arguments: map[string]any{
					"action": "note_save", "arguments": JSON(map[string]any{"title": "Unsafe continuation", "content": "No answer yet"}),
				}},
			), nil
		}
		answers := 0
		for _, message := range request.Messages {
			if message.Role == "user" && strings.Contains(message.Content, "user_question_answer") {
				answers++
				if !strings.Contains(message.Content, "Which required destination?") || !strings.Contains(message.Content, "Use A") {
					t.Error("answer lost the original question or complete reply", message.Content)
				}
			}
		}
		if answers != 1 {
			t.Errorf("answer appeared %d times in model context", answers)
		}
		return providers.Result{Text: "Completed at the explicitly selected destination.", UsageKnown: true}, nil
	}))
	if _, err := engine.goalTool(record, "create_goal", map[string]any{"objective": "Complete work at the destination chosen by the user."}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Prompt(record.ID, "Ask for the required destination before proceeding."); err != nil {
		t.Fatal(err)
	}
	if waiting := waitForAgent(t, engine, record.ID); waiting.Status != "waiting" || turns.Load() != 1 {
		t.Fatal("necessary answer did not gate further inference", waiting, turns.Load())
	}
	if notes, err := engine.listAll("note", &record.ID); err != nil || len(notes) != 0 {
		t.Fatal("a later action in the blocking question's batch still ran", notes, err)
	}
	// finishAgent publishes the agent status before the matching RunState. Its
	// caller holds configMu through both writes; observe that completed boundary.
	engine.configMu.Lock()
	phase := engine.RunState(record.ID).Phase
	engine.configMu.Unlock()
	if phase != "waiting_question" {
		t.Fatal("missing explicit question-wait phase", phase)
	}
	completed, err := engine.checkGoalCompletion(context.Background(), record, 0, domain.Message{ID: "premature", AgentID: record.ID})
	var waiting *questionWaitingError
	if completed || !errors.As(err, &waiting) {
		t.Fatal("required question allowed premature completion", completed, err)
	}
	goal, err := engine.CurrentGoal(record.ID)
	if err != nil || goal == nil || goal.Status != "active" {
		t.Fatal("waiting silently completed or paused the goal", goal, err)
	}
	questions, err := engine.openQuestions(record)
	if err != nil || len(questions) != 1 {
		t.Fatal(questions, err)
	}
	question := questions[0]
	if err := engine.AnswerQuestion(question.ID, "Use A", "answered", ""); err != nil {
		t.Fatal(err)
	}
	engine.mu.Lock()
	revision := engine.inputRevisions[record.ID]
	engine.mu.Unlock()
	if err := engine.AnswerQuestion(question.ID, "Use A", "answered", ""); err != nil {
		t.Fatal("lost-response retry was not idempotent", err)
	}
	engine.mu.Lock()
	retriedRevision := engine.inputRevisions[record.ID]
	engine.mu.Unlock()
	if revision != retriedRevision {
		t.Fatal("retried answer woke the agent twice", revision, retriedRevision)
	}
	if final := waitForAgent(t, engine, record.ID); final.Status != "idle" || turns.Load() != 2 {
		t.Fatal("late answer did not resume exactly once", final, turns.Load())
	}
	var receipt domain.AgentInput
	if err := engine.Store.Load("agent_input", "question_answer:"+question.ID, &receipt); err != nil || receipt.Status != "completed" || receipt.DeliveredAt.IsZero() {
		t.Fatal("answer did not retain its durable completion receipt", receipt, err)
	}
	if err := engine.AnswerQuestion(question.ID, "Use B", "answered", ""); err == nil {
		t.Fatal("a different retry changed the committed answer")
	}
}

func TestQuestionAnswerPersistsWithoutUndoingUserPause(t *testing.T) {
	var turns atomic.Int32
	engine, record := testEngine(t, providerFunction(func(context.Context, providers.Request) (providers.Result, error) {
		turns.Add(1)
		return providers.Result{Text: "Unexpected inference", UsageKnown: true}, nil
	}))
	if err := engine.Stop(record.ID); err != nil {
		t.Fatal(err)
	}
	result, err := engine.question(record, "question_ask", map[string]any{"title": "Required selection", "mode": "blocking"})
	if err != nil {
		t.Fatal(err)
	}
	question := result.(domain.Question)
	engine.mu.Lock()
	engine.grants[record.ID] = map[string]grant{"stale": {}}
	engine.mu.Unlock()
	instructionRevision := engine.instructionRevision(record.ProjectID)
	for range 2 {
		if err := engine.AnswerQuestion(question.ID, "Keep waiting until I resume", "answered", ""); err != nil {
			t.Fatal(err)
		}
	}
	history, err := engine.loadHistory(record.ID)
	if err != nil || len(history) != 1 || history[0].Role != "user" || !strings.Contains(history[0].Content, question.Title) {
		t.Fatal("human question/answer pair was duplicated or misattributed", history, err)
	}
	inputs, err := engine.Store.AgentInputs(record.ID)
	if err != nil || len(inputs) != 1 || inputs[0].Status != "active" {
		t.Fatal("paused answer lost its recoverable receipt", inputs, err)
	}
	engine.mu.Lock()
	stopped, active := engine.stopRequested[record.ID], engine.running[record.ID] != nil
	remainingGrants := len(engine.grants[record.ID])
	engine.mu.Unlock()
	if !stopped || active || turns.Load() != 0 {
		t.Fatal("answer overrode an explicit user stop", stopped, active, turns.Load())
	}
	if remainingGrants != 0 || engine.instructionRevision(record.ProjectID) != instructionRevision+1 {
		t.Fatal("new answer kept stale permissions, or an idempotent retry invalidated context twice")
	}
}

func TestRequiredChildQuestionPreventsGoalCompletionButNotParentWork(t *testing.T) {
	for _, mode := range []domain.QuestionMode{domain.QuestionAdvisory, domain.QuestionBlocking, ""} {
		t.Run(string(mode), func(t *testing.T) {
			engine, record := testEngine(t, nil)
			child, err := engine.Create(record.ProjectID, "Child", record.ProviderID, "approver-provider", record.ID)
			if err != nil {
				t.Fatal(err)
			}
			question := domain.Question{ID: "required-child", AgentID: child.ID, Title: "Necessary selection", Status: "open", Mode: mode}
			if err := engine.Store.Save("question", question.ID, child.ID, question); err != nil {
				t.Fatal(err)
			}
			if err := engine.questionGate(record.ID); err != nil {
				t.Fatal("child question stopped the parent from answering it", err)
			}
			if _, err := engine.goalTool(record, "create_goal", map[string]any{"objective": "Finish the original task and its necessary delegated work."}); err != nil {
				t.Fatal(err)
			}
			engine.Provider = providerFunction(func(_ context.Context, request providers.Request) (providers.Result, error) {
				if !strings.Contains(JSON(request.Messages), question.ID) {
					t.Error("completion reviewer did not receive the open child question")
				}
				return goalVerdict("complete", "Candidate appears done", "Fixture evidence", "", ""), nil
			})
			completed, err := engine.checkGoalCompletion(context.Background(), record, 0, domain.Message{ID: "candidate", AgentID: record.ID, Content: "Finished"})
			wantComplete := mode == domain.QuestionAdvisory
			if err != nil || completed != wantComplete {
				t.Fatal("goal completion did not honor question mode", completed, err)
			}
			goal, err := engine.CurrentGoal(record.ID)
			if err != nil || goal == nil || !wantComplete && goal.Status != "active" {
				t.Fatal(goal, err)
			}
		})
	}
}

func TestQuestionModesAreExplicitInPromptSchemaAndValidation(t *testing.T) {
	prompt := (&Engine{}).buildSystemPrompt(domain.Agent{Role: "main"})
	if !strings.Contains(prompt, "not a turn-ending signal") || !strings.Contains(prompt, "silence as consent") || !strings.Contains(toolHelp, "mode?:advisory|blocking") {
		t.Fatal("missing unattended-work or authorization guidance")
	}
	found := false
	for _, tool := range mainTools() {
		if tool.Name == "question_ask" {
			found = true
			properties := tool.Schema["properties"].(map[string]any)
			mode := properties["mode"].(map[string]any)
			if mode["default"] != "advisory" || !strings.Contains(JSON(mode["enum"]), "blocking") {
				t.Fatal("native question tool does not expose both modes", tool.Schema)
			}
		}
	}
	if !found || toolPolicies["question_answer"].Review != reviewRequired {
		t.Fatal("question schema disappeared or parent-answer approval changed")
	}
	engine, record := testEngine(t, nil)
	for _, mode := range []any{"", "guess", true} {
		if _, err := engine.question(record, "question_ask", map[string]any{"title": "Test", "mode": mode}); err == nil {
			t.Fatal("invalid explicit mode was silently treated as advisory", mode)
		}
	}
}
