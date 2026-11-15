package agent

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) CurrentTeamwork(agentID string) (*domain.Teamwork, error) {
	var member domain.Agent
	if err := engine.Store.Load("agent", agentID, &member); err != nil {
		return nil, err
	}

	root, err := engine.publicThreadRoot(member)
	if err != nil {
		return nil, err
	}

	var board domain.Teamwork
	if err := engine.Store.Load("teamwork", root.ID, &board); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}

		return nil, err
	}

	if board.AgentID != root.ID || board.ProjectID != root.ProjectID {
		return nil, errors.New("teamwork belongs to a different public thread")
	}

	return &board, nil
}

func (engine *Engine) teamwork(member domain.Agent, arguments map[string]any) (any, error) {
	root, err := engine.publicThreadRoot(member)
	if err != nil {
		return nil, err
	}

	engine.teamworkMu.Lock()
	defer engine.teamworkMu.Unlock()

	board, err := engine.CurrentTeamwork(member.ID)
	if err != nil {
		return nil, err
	}

	action := stringArg(arguments, "action")
	if action == "" || action == "get" {
		return teamworkView(board, arguments)
	}

	if action == "create" {
		if member.ID != root.ID {
			return nil, errors.New("only the main agent can create teamwork")
		}

		if board != nil && board.Status == "active" {
			return nil, errors.New("finish or cancel the current board first")
		}

		board, err = newTeamwork(root, arguments)
		if err != nil {
			return nil, err
		}
	} else {
		if board == nil || board.Status != "active" {
			return nil, errors.New("no active teamwork board")
		}

		revision, err := outputInteger(arguments, "revision", 0, 1, 1000000000)
		if err != nil || revision != board.Revision || stringArg(arguments, "id") != board.ID {
			return nil, errors.New("teamwork revision conflict; get the current board before updating")
		}

		if err := engine.updateTeamwork(root, member, board, action, arguments); err != nil {
			return nil, err
		}
	}

	board.Revision++
	board.UpdatedAt = time.Now().UTC()
	result, err := teamworkView(board, map[string]any{"taskId": stringArg(arguments, "taskId")})
	if err != nil {
		return nil, err
	}

	if err := engine.Store.Save("teamwork", root.ID, root.ID, board); err != nil {
		return nil, err
	}

	engine.Emit("teamwork", root.ID, map[string]any{"revision": board.Revision, "status": board.Status})
	return result, nil
}

func newTeamwork(root domain.Agent, arguments map[string]any) (*domain.Teamwork, error) {
	objective := strings.TrimSpace(stringArg(arguments, "objective"))
	if objective == "" || len(objective) > 4000 {
		return nil, errors.New("objective must contain 1..4000 bytes")
	}

	pattern := stringArg(arguments, "pattern")
	if pattern != "solve_verify" && pattern != "parallel" && pattern != "hypotheses" {
		return nil, errors.New("pattern must be solve_verify, parallel or hypotheses")
	}

	workers, err := outputInteger(arguments, "maxWorkers", 2, 1, 4)
	if err != nil {
		return nil, err
	}

	var tasks []domain.TeamTask
	if err := decode(JSON(arguments["tasks"]), &tasks); err != nil {
		return nil, errors.New("tasks must be a list of structured tasks")
	}

	if err := validateTeamTasks(tasks, pattern); err != nil {
		return nil, err
	}

	return &domain.Teamwork{
		ID: NewID(), AgentID: root.ID, ProjectID: root.ProjectID,
		Objective: objective, Pattern: pattern, MaxWorkers: workers, Status: "active", Tasks: tasks,
	}, nil
}

func (engine *Engine) updateTeamwork(root, member domain.Agent, board *domain.Teamwork, action string, arguments map[string]any) error {
	if action == "finish" || action == "cancel" {
		if member.ID != root.ID {
			return errors.New("only the main agent can finish or cancel teamwork")
		}

		evidence := strings.TrimSpace(stringArg(arguments, "evidence"))
		if evidence == "" || len(evidence) > 4000 {
			return errors.New("finish/cancel requires verification evidence or a cancellation reason (1..4000 bytes)")
		}

		if action == "finish" {
			for _, task := range board.Tasks {
				if task.Status != "done" {
					return errors.New("all tasks must be done before final verification")
				}
			}
			board.Status = "done"
		} else {
			board.Status = "cancelled"
		}

		board.Evidence = evidence

		return nil
	}

	var task *domain.TeamTask

	for index := range board.Tasks {
		if board.Tasks[index].ID == stringArg(arguments, "taskId") {
			task = &board.Tasks[index]
			break
		}
	}

	if task == nil {
		return errors.New("unknown taskId")
	}

	if action == "claim" {
		ownerID := stringArg(arguments, "ownerId")
		if ownerID == "" {
			ownerID = member.ID
		}

		if member.ID != root.ID && ownerID != member.ID {
			return errors.New("workers can only claim for themselves")
		}

		if _, err := engine.threadMember(root, ownerID); err != nil {
			return err
		}

		if err := claimTeamTask(board, task, ownerID); err != nil {
			return err
		}

		task.OwnerID, task.Status = ownerID, "claimed"
		return nil
	}

	if task.OwnerID == "" || (member.ID != root.ID && task.OwnerID != member.ID) {
		return errors.New("only the owner or main agent may update a claimed task")
	}

	if task.Status != "claimed" && task.Status != "blocked" {
		return errors.New("only claimed or blocked tasks can be updated")
	}

	result := strings.TrimSpace(stringArg(arguments, "result"))
	evidence := strings.TrimSpace(stringArg(arguments, "evidence"))
	if len(result) > 4000 || len(evidence) > 4000 {
		return errors.New("result and evidence must each fit in 4000 bytes")
	}

	switch action {
	case "release":
		task.OwnerID, task.Status = "", "pending"
	case "block":
		if result == "" {
			return errors.New("blocking requires a reason")
		}
		task.Status = "blocked"
	case "complete":
		if task.Status != "claimed" || result == "" || evidence == "" {
			return errors.New("completion requires a claimed task, result and concrete evidence")
		}
		task.Status = "done"
	default:
		return errors.New("unknown teamwork action")
	}

	task.Result, task.Evidence = result, evidence
	return nil
}

func teamworkView(board *domain.Teamwork, arguments map[string]any) (any, error) {
	if board == nil {
		return nil, nil
	}

	if taskID := stringArg(arguments, "taskId"); taskID != "" {
		for _, task := range board.Tasks {
			if task.ID == taskID {
				return map[string]any{"id": board.ID, "revision": board.Revision, "task": task}, nil
			}
		}

		return nil, errors.New("unknown taskId")
	}

	start, err := outputInteger(arguments, "offset", 0, 0, 32)
	if err != nil {
		return nil, err
	}

	limit, err := outputInteger(arguments, "limit", 10, 1, 32)
	if err != nil {
		return nil, err
	}

	start = min(start, len(board.Tasks))
	end := min(start+limit, len(board.Tasks))
	tasks := []map[string]any{}

	for _, task := range board.Tasks[start:end] {
		tasks = append(tasks, map[string]any{
			"id": task.ID, "title": task.Title, "kind": task.Kind, "status": task.Status,
			"ownerId": task.OwnerID, "dependsOn": task.DependsOn, "writePaths": task.WritePaths,
		})
	}

	return map[string]any{
		"id": board.ID, "agentId": board.AgentID, "status": board.Status, "revision": board.Revision,
		"objective": board.Objective, "pattern": board.Pattern, "maxWorkers": board.MaxWorkers,
		"tasks": tasks, "totalTasks": len(board.Tasks), "nextOffset": end, "hasMore": end < len(board.Tasks),
		"readTask": "Use taskId for objective, acceptance and evidence. Declared paths coordinate work; they are not permissions or filesystem locks.",
	}, nil
}
