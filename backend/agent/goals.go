package agent

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

type goalStoppedError struct{ Status, Reason string }

func (err *goalStoppedError) Error() string { return err.Reason }

func (engine *Engine) CurrentGoal(agentID string) (*domain.Goal, error) {
	var goal domain.Goal
	if err := engine.Store.Load("goal", agentID, &goal); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &goal, nil
}

func goalWrites(goal domain.Goal) []datastore.ResourceWrite {
	return []datastore.ResourceWrite{
		{Kind: "goal", ID: goal.AgentID, Parent: goal.AgentID, Value: goal},
		{Kind: "goal_history", ID: goal.ID, Parent: goal.AgentID, Value: goal},
	}
}

func (engine *Engine) saveGoal(goal domain.Goal) error {
	if err := engine.Store.SaveResources(goalWrites(goal)); err != nil {
		return err
	}
	engine.Emit("goal", goal.AgentID, goal)
	return nil
}

func (engine *Engine) createGoalLocked(agentID, objective, origin string) (*domain.Goal, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" || len(objective) > 100000 || strings.ContainsRune(objective, 0) {
		return nil, errors.New("goal objective must contain 1–100000 bytes without NUL")
	}
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return nil, err
	}
	if record.Role == "approver" {
		return nil, errors.New("approvers cannot create work goals")
	}
	current, err := engine.CurrentGoal(agentID)
	if err != nil {
		return nil, err
	}
	if current != nil && current.Status != "complete" && current.Status != "cancelled" {
		return nil, errors.New("an unfinished goal already exists; continue it or ask the user to change it")
	}
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return nil, err
	}
	sources := []string{}
	for _, input := range inputs {
		if input.Status == "active" {
			sources = append(sources, input.ID)
		}
	}
	now := time.Now().UTC()
	goal := domain.Goal{ID: NewID(), AgentID: agentID, Objective: objective, Origin: origin, Status: "active", SourceInputIDs: sources, Revision: 1, CreatedAt: now, UpdatedAt: now}
	if err := engine.saveGoal(goal); err != nil {
		return nil, err
	}
	return &goal, nil
}

func (engine *Engine) goalTool(record domain.Agent, action string, args map[string]any) (any, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if action == "create_goal" {
		return engine.createGoalLocked(record.ID, stringArg(args, "objective"), "agent")
	}
	goal, err := engine.CurrentGoal(record.ID)
	if err != nil || action == "get_goal" {
		return goal, err
	}
	if goal == nil {
		return nil, errors.New("create a goal before updating it")
	}
	if goal.Status != "active" {
		return nil, errors.New("only the user can resume a stopped goal or replace a terminal goal")
	}
	status := stringArg(args, "status")
	if status != "complete" && status != "blocked" && status != "paused" {
		return nil, errors.New("goal status must be complete, blocked or paused")
	}
	reason := strings.TrimSpace(stringArg(args, "reason"))
	if len(reason) > 8000 {
		return nil, errors.New("goal reason exceeds 8000 bytes")
	}
	goal.RequestedStatus, goal.RequestReason = status, reason
	goal.Revision++
	goal.UpdatedAt = time.Now().UTC()
	if err := engine.saveGoal(*goal); err != nil {
		return nil, err
	}
	return map[string]any{"goal": goal, "reviewRequired": true, "notice": "The independent auto-approver must verify this request when your turn ends. The goal remains active until that review succeeds."}, nil
}

// Called at the same safe boundary that admits steering into model history.
func (engine *Engine) goalInputWrites(agentID string, inputIDs []string) ([]datastore.ResourceWrite, error) {
	goal, err := engine.CurrentGoal(agentID)
	if err != nil || goal == nil || goal.Status != "active" {
		return nil, err
	}
	known := map[string]bool{}
	for _, id := range goal.SourceInputIDs {
		known[id] = true
	}
	for _, id := range inputIDs {
		if !known[id] {
			goal.SourceInputIDs = append(goal.SourceInputIDs, id)
		}
	}
	goal.RequestedStatus, goal.RequestReason = "", ""
	goal.Blocker, goal.BlockedAttempts = "", 0
	goal.Revision++
	goal.UpdatedAt = time.Now().UTC()
	return goalWrites(*goal), nil
}

func (engine *Engine) resumeGoalWrites(agentID string) ([]datastore.ResourceWrite, error) {
	goal, err := engine.CurrentGoal(agentID)
	if err != nil || goal == nil || goal.Status != "paused" && goal.Status != "blocked" {
		return nil, err
	}
	goal.Status = "active"
	goal.RequestedStatus, goal.RequestReason = "", ""
	goal.Blocker, goal.BlockedAttempts = "", 0
	goal.Revision++
	goal.UpdatedAt = time.Now().UTC()
	return goalWrites(*goal), nil
}

// User-only cancellation preserves the goal and all evidence for inspection.
func (engine *Engine) CancelGoal(agentID, goalID string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	goal, err := engine.CurrentGoal(agentID)
	if err != nil {
		return err
	}
	if goal == nil || goal.ID != goalID {
		return errors.New("goal changed; reload before cancelling it")
	}
	if goal.Status == "cancelled" {
		return nil
	}
	if goal.Status == "complete" {
		return errors.New("a completed goal cannot be cancelled")
	}
	engine.mu.Lock()
	running := engine.running[agentID] != nil
	engine.mu.Unlock()
	if running {
		return errors.New("pause the agent before cancelling its goal")
	}
	goal.Status, goal.Reason = "cancelled", "Cancelled by the user"
	goal.RequestedStatus, goal.RequestReason = "", ""
	goal.Revision++
	goal.UpdatedAt = time.Now().UTC()
	writes := goalWrites(*goal)
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return err
	}
	record.Status, record.Error = "stopped", ""
	writes = append(writes,
		datastore.ResourceWrite{Kind: "agent", ID: agentID, Parent: record.ProjectID, Value: record},
		datastore.ResourceWrite{Kind: "agent_stop", ID: agentID, Parent: agentID, Value: agentStopRecord{AgentID: agentID, Requested: true}},
	)
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input.Status == "active" || input.Status == "blocked" || input.Status == "paused" {
			input.Status = "cancelled"
			writes = append(writes, datastore.ResourceWrite{Kind: "agent_input", ID: input.ID, Parent: agentID, Value: input})
		}
	}
	if err := engine.Store.SaveResources(writes); err != nil {
		return err
	}
	engine.mu.Lock()
	engine.stopRequested[agentID] = true
	engine.mu.Unlock()
	engine.setRunState(agentID, "stopped", nil)
	engine.Emit("agent", agentID, record)
	engine.Emit("goal", agentID, goal)
	engine.Emit("input", agentID, nil)
	return nil
}

func (engine *Engine) goalForCompletion(agentID string) (*domain.Goal, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	goal, err := engine.CurrentGoal(agentID)
	if err != nil {
		return nil, err
	}
	if goal != nil && goal.Status == "active" {
		return goal, nil
	}
	if goal != nil && (goal.Status == "paused" || goal.Status == "blocked") {
		return goal, &goalStoppedError{Status: goal.Status, Reason: goal.Reason}
	}
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return nil, err
	}
	for _, input := range inputs {
		if input.Status == "active" {
			return engine.createGoalLocked(agentID, input.Text, "task")
		}
	}
	return nil, nil
}
