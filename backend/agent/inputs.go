package agent

import (
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
)

var inputIDPattern = regexp.MustCompile("^[A-Za-z0-9_-]{1,128}$")

type agentStopRecord struct {
	AgentID   string `json:"agentId"`
	Requested bool   `json:"requested"`
}

// SubmitInput acknowledges a database commit, never an SSH handshake. Its worker
// belongs to the engine; cancellation of the HTTP request cannot cancel it.
// Retrying the same receipt ID is safe even if the first response was lost.
func (engine *Engine) SubmitInput(agentID, id, text, mode string) (domain.AgentInput, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	if !inputIDPattern.MatchString(id) || strings.TrimSpace(text) == "" || len(text) > 100000 {
		return domain.AgentInput{}, errors.New("instruction must contain 1–100000 bytes and a valid receipt ID")
	}
	if mode != "steer" && mode != "queue" {
		return domain.AgentInput{}, errors.New("input mode must be steer or queue")
	}
	var previous domain.AgentInput
	err := engine.Store.Load("agent_input", id, &previous)
	if err == nil {
		if previous.AgentID != agentID || previous.Text != text || previous.Mode != mode {
			return domain.AgentInput{}, errors.New("receipt ID already belongs to another input")
		}
		return previous, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return previous, err
	}
	if err := engine.ctx.Err(); err != nil {
		return previous, err
	}
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return previous, err
	}
	if record.Role == "approver" {
		return previous, errors.New("send instructions to the main agent")
	}
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return previous, err
	}
	if len(inputs) >= 100 {
		return previous, errors.New("message queue is full (100 pending messages)")
	}
	status := "pending"
	if mode == "queue" {
		status = "queued"
	}
	input := domain.AgentInput{
		ID: id, AgentID: agentID, ProjectID: record.ProjectID, Text: text,
		Mode: mode, Status: status, CreatedAt: time.Now().UTC(),
	}
	if err := engine.Store.SaveResources([]datastore.ResourceWrite{
		{Kind: "agent_input", ID: id, Parent: agentID, Value: input},
		{Kind: "agent_stop", ID: agentID, Parent: agentID, Value: agentStopRecord{AgentID: agentID}},
	}); err != nil {
		return input, err
	}
	engine.Emit("input", agentID, input)
	engine.mu.Lock()
	active := engine.running[agentID] != nil
	wasStopped := engine.stopRequested[agentID]
	engine.stopRequested[agentID] = false
	engine.mu.Unlock()
	if mode == "steer" || !active || wasStopped {
		if !active {
			if err := engine.resumeBlockedInputs(agentID); err != nil {
				engine.setRunState(agentID, "error", err)
				return input, nil
			}
		}
		// A committed receipt remains accepted if starting a worker fails. It can
		// be resumed from the UI or recovered after a service restart.
		if err := engine.startOrWakeAgent(record); err != nil {
			engine.setRunState(agentID, "error", err)
		}
	}
	return input, nil
}

// Caller holds configMu. Steering is delivered between whole tool batches.
// Exactly one queued message starts when a turn ends; it cannot leak into the
// current model context or its auto-approver's user instruction history.
func (engine *Engine) deliverInputs(agentID string, allowQueued bool) (bool, error) {
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return false, err
	}
	selected := []domain.AgentInput{}
	for _, input := range inputs {
		if input.Status == "pending" {
			selected = append(selected, input)
		}
	}
	if len(selected) == 0 && allowQueued {
		for _, input := range inputs {
			if input.Status == "active" {
				return false, nil
			}
		}
		for _, input := range inputs {
			if input.Status == "queued" {
				selected = append(selected, input)
				break
			}
		}
	}
	if len(selected) == 0 {
		return false, nil
	}
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return false, err
	}
	if err := engine.nameFirstConversation(&record, selected[0].Text); err != nil {
		return false, err
	}
	writes := []datastore.ResourceWrite{{Kind: "agent", ID: record.ID, Parent: record.ProjectID, Value: record}}
	messages := []domain.Message{}
	for _, input := range selected {
		input.Status, input.DeliveredAt = "active", time.Now().UTC()
		message := domain.Message{ID: input.ID, AgentID: agentID, Role: "user", Content: input.Text, CreatedAt: input.DeliveredAt}
		messages = append(messages, message)
		writes = append(writes,
			datastore.ResourceWrite{Kind: "agent_input", ID: input.ID, Parent: agentID, Value: input},
			datastore.ResourceWrite{Kind: "message", ID: input.ID, Parent: agentID, Value: message},
			datastore.ResourceWrite{Kind: "instruction", ID: input.ID, Parent: input.ProjectID, Value: message},
		)
	}
	ids := make([]string, 0, len(selected))
	for _, input := range selected {
		ids = append(ids, input.ID)
	}
	goalUpdates, err := engine.goalInputWrites(agentID, ids)
	if err != nil {
		return false, err
	}
	writes = append(writes, goalUpdates...)
	if err := engine.Store.SaveResources(writes); err != nil {
		return false, err
	}
	engine.invalidateProjectGrants(record.ProjectID)
	engine.Emit("agent", agentID, record)
	for _, message := range messages {
		engine.Emit("message", agentID, message)
	}
	engine.Emit("input", agentID, nil)
	if len(goalUpdates) > 0 {
		engine.Emit("goal", agentID, nil)
	}
	return true, nil
}

func (engine *Engine) setInputStatus(agentID, from, to string) error {
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return err
	}
	writes := []datastore.ResourceWrite{}
	for _, input := range inputs {
		if input.Status != from {
			continue
		}
		input.Status = to
		writes = append(writes, datastore.ResourceWrite{Kind: "agent_input", ID: input.ID, Parent: agentID, Value: input})
	}
	if len(writes) == 0 {
		return nil
	}
	if err := engine.Store.SaveResources(writes); err != nil {
		return err
	}
	engine.Emit("input", agentID, nil)
	return nil
}

// The final answer and completion receipts share a commit. Recovery must not
// repeat a completed turn if the service stops immediately after its response.
func (engine *Engine) persistFinalResponse(message domain.Message) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	inputs, err := engine.Store.AgentInputs(message.AgentID)
	if err != nil {
		return err
	}
	writes := []datastore.ResourceWrite{{Kind: "message", ID: message.ID, Parent: message.AgentID, Value: message}}
	for _, input := range inputs {
		if input.Status != "active" {
			continue
		}
		input.Status = "completed"
		writes = append(writes, datastore.ResourceWrite{Kind: "agent_input", ID: input.ID, Parent: input.AgentID, Value: input})
	}
	if err := engine.Store.SaveResources(writes); err != nil {
		return err
	}
	engine.Emit("message", message.AgentID, message)
	engine.Emit("input", message.AgentID, nil)
	return nil
}

func (engine *Engine) CancelInput(agentID, id string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	var input domain.AgentInput
	if err := engine.Store.Load("agent_input", id, &input); err != nil {
		return err
	}
	if input.AgentID != agentID {
		return errors.New("input belongs to another agent")
	}
	if input.Status == "cancelled" {
		return nil
	}
	if !input.DeliveredAt.IsZero() {
		return errors.New("input is already being processed")
	}
	input.Status = "cancelled"
	if err := engine.Store.Save("agent_input", id, agentID, input); err != nil {
		return err
	}
	engine.Emit("input", agentID, input)
	return nil
}

func (engine *Engine) ResumeInputs(agentID string) error {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()
	var record domain.Agent
	if err := engine.Store.Load("agent", agentID, &record); err != nil {
		return err
	}
	if record.Role == "approver" {
		return errors.New("send instructions to the main agent")
	}
	if err := engine.resumeBlockedInputs(agentID); err != nil {
		return err
	}
	engine.mu.Lock()
	engine.stopRequested[agentID] = false
	engine.mu.Unlock()
	return engine.startOrWakeAgent(record)
}

func (engine *Engine) resumeBlockedInputs(agentID string) error {
	inputs, err := engine.Store.AgentInputs(agentID)
	if err != nil {
		return err
	}
	writes := []datastore.ResourceWrite{{Kind: "agent_stop", ID: agentID, Parent: agentID, Value: agentStopRecord{AgentID: agentID}}}
	goalUpdates, err := engine.resumeGoalWrites(agentID)
	if err != nil {
		return err
	}
	writes = append(writes, goalUpdates...)
	for _, input := range inputs {
		if input.Status != "blocked" && input.Status != "paused" {
			continue
		}
		input.Status = "active"
		if input.DeliveredAt.IsZero() {
			input.Status = "pending"
		}
		writes = append(writes, datastore.ResourceWrite{Kind: "agent_input", ID: input.ID, Parent: agentID, Value: input})
	}
	if err := engine.Store.SaveResources(writes); err != nil {
		return err
	}
	if len(goalUpdates) > 0 {
		engine.Emit("goal", agentID, nil)
	}
	return nil
}

func (engine *Engine) RunState(agentID string) domain.AgentRunState {
	state := domain.AgentRunState{AgentID: agentID}
	_ = engine.Store.Load("agent_run", agentID, &state)
	return state
}

func (engine *Engine) setRunState(agentID, phase string, failure error) {
	state := domain.AgentRunState{AgentID: agentID, Phase: phase}
	if failure != nil {
		state.Error = failure.Error()
		if challenge := remote.HostKeyChallenge(failure); challenge.PublicKey != "" {
			state.HostKey = challenge
		}
		var connectionError *ProjectConnectionError
		if errors.As(failure, &connectionError) && connectionError.HostKey.PublicKey != "" {
			state.HostKey = connectionError.HostKey
		}
	}
	previous := engine.RunState(agentID)
	if JSON(previous) == JSON(state) {
		return
	}
	if engine.Store.Save("agent_run", agentID, agentID, state) == nil {
		engine.Emit("agent_run", agentID, state)
	}
}

func (engine *Engine) recoverInputs() error {
	records, err := engine.listAll("agent", nil)
	if err != nil {
		return err
	}
	for _, value := range records {
		id := stringArg(value, "id")
		var stopped agentStopRecord
		if err := engine.Store.Load("agent_stop", id, &stopped); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if stopped.Requested {
			engine.mu.Lock()
			engine.stopRequested[id] = true
			engine.mu.Unlock()
			continue
		}
		goal, err := engine.CurrentGoal(id)
		if err != nil {
			return err
		}
		if goal != nil && (goal.Status == "paused" || goal.Status == "blocked") {
			continue
		}
		inputs, err := engine.Store.AgentInputs(id)
		if err != nil {
			return err
		}
		started := false
		for _, input := range inputs {
			if input.Status != "pending" && input.Status != "queued" && input.Status != "active" {
				continue
			}
			if input.Status == "queued" && stringArg(value, "status") == "stopped" {
				continue
			}
			var record domain.Agent
			if err := engine.Store.Load("agent", id, &record); err != nil {
				return err
			}
			if err := engine.startOrWakeAgent(record); err != nil {
				return err
			}
			started = true
			break
		}
		if !started && goal != nil && goal.Status == "active" && stringArg(value, "status") != "error" {
			var record domain.Agent
			if err := engine.Store.Load("agent", id, &record); err != nil {
				return err
			}
			if err := engine.startOrWakeAgent(record); err != nil {
				return err
			}
		}
	}
	return nil
}
