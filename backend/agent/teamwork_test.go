package agent

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lehlud/zentrale/domain"
)

func teamFixture(t *testing.T) (*Engine, domain.Agent, []domain.Agent) {
	t.Helper()
	engine, root := testEngine(t, nil)
	workers := []domain.Agent{}
	for index := range 3 {
		worker := domain.Agent{ID: fmt.Sprintf("worker-%d", index), ProjectID: root.ProjectID, ParentID: root.ID, Role: "subagent"}
		if err := engine.Store.Save("agent", worker.ID, root.ProjectID, worker); err != nil {
			t.Fatal(err)
		}
		workers = append(workers, worker)
	}

	return engine, root, workers
}

func teamTasks() []domain.TeamTask {
	return []domain.TeamTask{
		{ID: "build", Title: "Build", Objective: "Implement the contract", Acceptance: "Regression test passes", WritePaths: []string{"backend"}},
		{ID: "check", Title: "Check", Objective: "Try to falsify the solution", Acceptance: "Independent test evidence", Kind: "verify", DependsOn: []string{"build"}},
	}
}

func createTeam(t *testing.T, engine *Engine, root domain.Agent, tasks []domain.TeamTask, pattern string, workers int) *domain.Teamwork {
	t.Helper()
	_, err := engine.teamwork(root, map[string]any{"action": "create", "objective": "Deliver and verify", "pattern": pattern, "maxWorkers": workers, "tasks": tasks})
	if err != nil {
		t.Fatal(err)
	}

	board, err := engine.CurrentTeamwork(root.ID)
	if err != nil || board == nil {
		t.Fatal(board, err)
	}

	return board
}

func teamUpdate(engine *Engine, member domain.Agent, board *domain.Teamwork, action, task string, extra map[string]any) error {
	arguments := map[string]any{"id": board.ID, "revision": board.Revision, "action": action, "taskId": task}
	for key, value := range extra {
		arguments[key] = value
	}

	_, err := engine.teamwork(member, arguments)
	if err == nil {
		board.Revision++
	}

	return err
}

func TestTeamworkOwnershipDependenciesEvidenceAndPersistence(t *testing.T) {
	engine, root, workers := teamFixture(t)
	board := createTeam(t, engine, root, teamTasks(), "solve_verify", 2)
	update := func(member domain.Agent, action, task string, extra map[string]any, succeeds bool) {
		t.Helper()
		err := teamUpdate(engine, member, board, action, task, extra)
		if (err == nil) != succeeds {
			t.Fatalf("%s %s by %s: %v", action, task, member.ID, err)
		}
	}

	update(workers[1], "claim", "check", nil, false)
	update(workers[0], "claim", "build", nil, true)
	update(workers[1], "complete", "build", map[string]any{"result": "stolen", "evidence": "none"}, false)
	update(workers[0], "complete", "build", map[string]any{"result": "implemented"}, false)
	update(root, "finish", "", map[string]any{"evidence": "premature"}, false)
	update(workers[0], "complete", "build", map[string]any{"result": "implemented", "evidence": "go test contract: PASS"}, true)
	update(workers[0], "claim", "check", nil, false)
	update(workers[1], "claim", "check", nil, true)
	update(workers[1], "complete", "check", map[string]any{"result": "verified", "evidence": "race regression: PASS"}, true)
	update(workers[1], "finish", "", map[string]any{"evidence": "worker cannot finish"}, false)
	update(root, "finish", "", map[string]any{"evidence": "integration and acceptance checks: PASS"}, true)

	loaded, err := engine.CurrentTeamwork(workers[2].ID)
	if err != nil || loaded.Status != "done" || loaded.Revision != board.Revision || loaded.Tasks[1].OwnerID != workers[1].ID || loaded.Evidence == "" {
		t.Fatal(loaded, err)
	}

	// Coordination never creates agents, jobs, goals, main inputs or messages.
	for _, kind := range []string{"job", "goal", "agent_input", "message"} {
		rows, err := engine.Store.List(kind, nil, 100, 0)
		if err != nil || len(rows) != 0 {
			t.Fatalf("unexpected %s write: %v %v", kind, rows, err)
		}
	}
}

func TestTeamworkRejectsInvalidGraphsAndLiteralPaths(t *testing.T) {
	for _, scope := range []string{"../escape", "foo/../bar", "/absolute", "foo//bar", "foo/./bar", "a\\b", "foo/*", "foo\x00bar", ""} {
		if err := validateTeamWritePath(scope); err == nil {
			t.Errorf("accepted unsafe path %q", scope)
		}
	}

	for _, scope := range []string{".", "backend", "frontend/src/a.tsx", "a b"} {
		if err := validateTeamWritePath(scope); err != nil {
			t.Errorf("rejected literal path %q: %v", scope, err)
		}
	}

	for _, mutation := range []func([]domain.TeamTask){
		func(tasks []domain.TeamTask) { tasks[0].DependsOn = []string{"missing"} },
		func(tasks []domain.TeamTask) { tasks[0].DependsOn = []string{"check"} },
		func(tasks []domain.TeamTask) { tasks[1].ID = tasks[0].ID },
		func(tasks []domain.TeamTask) { tasks[1].DependsOn = nil },
		func(tasks []domain.TeamTask) { tasks[0].Acceptance = "" },
	} {
		tasks := teamTasks()
		mutation(tasks)
		if err := validateTeamTasks(tasks, "solve_verify"); err == nil {
			t.Fatal("accepted invalid task graph")
		}
	}
}

func TestTeamworkScopesBudgetsReleaseAndPrivateAgents(t *testing.T) {
	engine, root, workers := teamFixture(t)
	tasks := teamTasks()
	tasks[1].Kind, tasks[1].DependsOn, tasks[1].WritePaths = "work", nil, []string{"backend/test.go"}
	board := createTeam(t, engine, root, tasks, "parallel", 1)

	if err := teamUpdate(engine, workers[0], board, "claim", "build", map[string]any{"ownerId": workers[1].ID}); err == nil {
		t.Fatal("worker assigned another worker")
	}

	if err := teamUpdate(engine, workers[0], board, "claim", "build", nil); err != nil {
		t.Fatal(err)
	}

	if err := teamUpdate(engine, workers[1], board, "claim", "check", nil); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatal("overlapping write scope was not rejected", err)
	}

	if err := teamUpdate(engine, root, board, "release", "build", nil); err != nil {
		t.Fatal("main could not recover abandoned work", err)
	}

	if err := teamUpdate(engine, workers[1], board, "claim", "check", nil); err != nil {
		t.Fatal(err)
	}

	// Non-overlapping work is still subject to the worker budget.
	var stored domain.Teamwork
	if err := engine.Store.Load("teamwork", root.ID, &stored); err != nil {
		t.Fatal(err)
	}
	stored.Tasks[0].WritePaths = []string{"frontend"}
	if err := engine.Store.Save("teamwork", root.ID, root.ID, stored); err != nil {
		t.Fatal(err)
	}

	if err := teamUpdate(engine, workers[0], board, "claim", "build", nil); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatal("worker budget was not enforced", err)
	}

	private := domain.Agent{ID: "private", ParentID: root.ID, ProjectID: root.ProjectID, Role: "approver"}
	foreign := domain.Agent{ID: "foreign", ProjectID: "elsewhere", Role: "main"}
	for _, member := range []domain.Agent{private, foreign} {
		if err := engine.Store.Save("agent", member.ID, member.ProjectID, member); err != nil {
			t.Fatal(err)
		}

		if err := teamUpdate(engine, root, board, "claim", "build", map[string]any{"ownerId": member.ID}); err == nil {
			t.Fatal("assigned outside the public thread")
		}
	}

	if _, err := engine.CurrentTeamwork(private.ID); err == nil {
		t.Fatal("private approver read public teamwork")
	}
}

func TestTeamworkConcurrentClaimsHaveOneWinner(t *testing.T) {
	engine, root, workers := teamFixture(t)
	board := createTeam(t, engine, root, teamTasks(), "parallel", 3)
	var successes atomic.Int32
	var group sync.WaitGroup
	for _, worker := range workers {
		group.Go(func() {
			_, err := engine.teamwork(worker, map[string]any{"id": board.ID, "revision": board.Revision, "action": "claim", "taskId": "build"})
			if err == nil {
				successes.Add(1)
			}
		})
	}
	group.Wait()

	if successes.Load() != 1 {
		t.Fatal("claim was not exclusive", successes.Load())
	}
}

func TestTeamworkBoundedViewsAndOldBoardCannotUpdateReplacement(t *testing.T) {
	engine, root, _ := teamFixture(t)
	tasks := []domain.TeamTask{}
	for index := range 23 {
		tasks = append(tasks, domain.TeamTask{
			ID: fmt.Sprintf("task-%d", index), Title: "Title", Objective: "Detailed objective",
			Acceptance: "Detailed acceptance criteria",
		})
	}
	board := createTeam(t, engine, root, tasks, "hypotheses", 2)
	view, err := engine.teamwork(root, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	page := view.(map[string]any)
	if len(page["tasks"].([]map[string]any)) != 10 || page["hasMore"] != true || strings.Contains(JSON(view), "Detailed acceptance") {
		t.Fatal("default board view was not compact", view)
	}

	detail, err := engine.teamwork(root, map[string]any{"taskId": "task-22"})
	if err != nil || !strings.Contains(JSON(detail), "Detailed acceptance") {
		t.Fatal(detail, err)
	}

	if err := teamUpdate(engine, root, board, "cancel", "", map[string]any{"evidence": "Superseded hypothesis set"}); err != nil {
		t.Fatal(err)
	}
	oldID := board.ID
	board = createTeam(t, engine, root, tasks, "hypotheses", 2)
	if _, err := engine.teamwork(root, map[string]any{"id": oldID, "revision": board.Revision, "action": "claim", "taskId": "task-0"}); err == nil {
		t.Fatal("an old board reference updated its replacement")
	}
}
