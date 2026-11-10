package agent

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lehlud/zentrale/datastore"
	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) Create(projectID, name, providerID, approverProviderID, parentID string) (domain.Agent, error) {
	return engine.CreateWithModels(projectID, name,
		domain.ModelChoice{ProviderID: providerID},
		domain.ModelChoice{ProviderID: approverProviderID}, parentID)
}

func (engine *Engine) CreateWithModels(
	projectID, name string,
	mainChoice, approverChoice domain.ModelChoice,
	parentID string,
) (domain.Agent, error) {
	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	if strings.TrimSpace(name) == "" {
		return domain.Agent{}, errors.New("agent name is required")
	}

	var project domain.Project
	if err := engine.Store.Load("project", projectID, &project); err != nil {
		return domain.Agent{}, err
	}

	mainChoice, err := engine.resolveChoice(mainChoice, "main")
	if err != nil {
		return domain.Agent{}, err
	}

	approverChoice, err = engine.resolveChoice(approverChoice, "approver")
	if err != nil {
		return domain.Agent{}, err
	}

	role, err := engine.childRole(parentID, projectID)
	if err != nil {
		return domain.Agent{}, err
	}

	mainAgent := domain.Agent{
		ID:        NewID(),
		ProjectID: projectID,
		ParentID:  parentID,
		Name:      name,
		Role:      role,

		ProviderID:     mainChoice.ProviderID,
		ModelID:        mainChoice.ModelID,
		ReasoningLevel: mainChoice.ReasoningLevel,

		ApproverID: NewID(),
		Status:     "idle",
	}

	approver := domain.Agent{
		ID:        mainAgent.ApproverID,
		ProjectID: projectID,
		ParentID:  mainAgent.ID,

		Name:       "Auto-Approver",
		Role:       "approver",
		ProviderID: approverChoice.ProviderID,

		ModelID:        approverChoice.ModelID,
		ReasoningLevel: approverChoice.ReasoningLevel,
		Status:         "idle",
	}
	if err := engine.saveAgent(approver); err != nil {
		return domain.Agent{}, err
	}

	if err := engine.saveAgent(mainAgent); err != nil {
		_ = engine.Store.Remove("agent", approver.ID)
		return domain.Agent{}, err
	}

	return mainAgent, nil
}

func (engine *Engine) childRole(parentID, projectID string) (string, error) {
	if parentID == "" {
		return "main", nil
	}

	var parent domain.Agent
	if err := engine.Store.Load("agent", parentID, &parent); err != nil {
		return "", err
	}

	if parent.Role != "main" || parent.ProjectID != projectID {
		return "", errors.New("only main agents may create subagents in their own project")
	}

	return "subagent", nil
}

// Prompt accepts input durably before any network or sandbox preparation.
func (engine *Engine) Prompt(agentID, text string) error {
	return engine.PromptContext(engine.ctx, agentID, text)
}

func (engine *Engine) PromptContext(ctx context.Context, agentID, text string) error {
	_, err := engine.SubmitInput(agentID, NewID(), text, "steer")
	return err
}

// NotifyAgent delivers a harness event without adding it to the user's instruction
// history. Notifications wake an idle agent just like fresh user input does.
func (engine *Engine) NotifyAgent(agentID, text string) error {
	if engine.ctx.Err() != nil {
		return engine.ctx.Err()
	}

	engine.configMu.Lock()
	defer engine.configMu.Unlock()

	var agentRecord domain.Agent
	if err := engine.Store.Load("agent", agentID, &agentRecord); err != nil {
		return err
	}

	if agentRecord.Role == "approver" {
		return errors.New("send instructions to the main agent")
	}

	if err := engine.appendMessage(agentID, "event", text, "", "", ""); err != nil {
		return err
	}

	engine.mu.Lock()
	stopped := engine.stopRequested[agentID]
	engine.mu.Unlock()

	if stopped {
		return nil
	}

	goal, err := engine.CurrentGoal(agentID)
	if err != nil {
		return err
	}

	if goal != nil && (goal.Status == "paused" || goal.Status == "blocked") {
		return nil
	}

	return engine.startOrWakeAgent(agentRecord)
}

func (engine *Engine) startOrWakeAgent(agentRecord domain.Agent) error {
	agentID := agentRecord.ID
	engine.mu.Lock()
	if err := engine.ctx.Err(); err != nil {
		engine.mu.Unlock()
		return err
	}

	engine.inputRevisions[agentID]++

	if wakeChannel := engine.wake[agentID]; wakeChannel != nil {
		select {
		case wakeChannel <- struct{}{}:
		default:
		}
	}

	if engine.running[agentID] != nil {
		engine.mu.Unlock()
		return nil
	}

	workerContext, cancel := context.WithCancel(engine.ctx)
	engine.running[agentID] = cancel
	engine.wake[agentID] = make(chan struct{}, 1)
	engine.workers.Add(1)
	engine.mu.Unlock()
	agentRecord.Status = "running"
	agentRecord.Error = ""

	if err := engine.saveAgent(agentRecord); err != nil {
		engine.removeWorker(agentID)
		cancel()
		engine.workers.Done()

		return err
	}

	engine.setRunState(agentID, "accepted", nil)
	go engine.runAgent(workerContext, agentID, cancel)

	return nil
}

func (engine *Engine) removeWorker(agentID string) {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	delete(engine.running, agentID)
	delete(engine.wake, agentID)
}

func (engine *Engine) runAgent(ctx context.Context, agentID string, cancel context.CancelFunc) {
	defer engine.workers.Done()
	defer cancel()

	for {
		processedRevision, runError := engine.loop(ctx, agentID)
		engine.configMu.Lock()

		if runError == nil {
			runError = ctx.Err()
		}

		engine.mu.Lock()
		hasNewInput := engine.inputRevisions[agentID] > processedRevision
		engine.mu.Unlock()

		if runError == nil && hasNewInput && ctx.Err() == nil {
			engine.configMu.Unlock()
			continue
		}

		if runError == nil && ctx.Err() == nil {
			if err := engine.setInputStatus(agentID, "active", "completed"); err != nil {
				runError = err
			} else if next, err := engine.deliverInputs(agentID, true); err != nil {
				runError = err
			} else if next {
				engine.configMu.Unlock()
				continue
			}
		}

		if runError != nil && engine.ctx.Err() == nil {
			status := "blocked"

			if errors.Is(runError, context.Canceled) {
				status = "paused"
			}

			var stopped *goalStoppedError
			if errors.As(runError, &stopped) && stopped.Status == "paused" {
				status = "paused"
			}

			_ = engine.setInputStatus(agentID, "active", status)
			_ = engine.setInputStatus(agentID, "pending", status)
		}

		engine.invalidate(agentID)
		engine.removeWorker(agentID)
		engine.releaseConnection(agentID)
		engine.finishAgent(agentID, runError)
		engine.mu.Lock()
		restart := hasNewInput && !engine.stopRequested[agentID] && engine.ctx.Err() == nil
		engine.mu.Unlock()

		if restart {
			var record domain.Agent
			if engine.Store.Load("agent", agentID, &record) == nil {
				_ = engine.resumeBlockedInputs(agentID)
				_ = engine.startOrWakeAgent(record)
			}
		}

		engine.configMu.Unlock()
		engine.cleanupVenvsIfIdle(agentID)
		engine.scheduleVMIdleCheck(agentID)

		return
	}
}

func (engine *Engine) finishAgent(agentID string, runError error) {
	var agentRecord domain.Agent
	if engine.Store.Load("agent", agentID, &agentRecord) != nil {
		return
	}

	agentRecord.Status = "idle"

	if runError != nil {
		agentRecord.Status = "error"
		agentRecord.Error = runError.Error()

		if errors.Is(runError, context.Canceled) {
			agentRecord.Status = "stopped"
			agentRecord.Error = ""
		}

		var stopped *goalStoppedError
		if errors.As(runError, &stopped) {
			agentRecord.Status = "blocked"

			if stopped.Status == "paused" {
				agentRecord.Status, agentRecord.Error = "stopped", ""
			}
		}
	}

	// Process cancellation is not a user stop. Retain a restart marker even
	// for notification-driven turns that have no active input or goal.
	engine.mu.Lock()
	explicitStop := engine.stopRequested[agentID]
	engine.mu.Unlock()
	var goalStop *goalStoppedError
	if engine.ctx.Err() != nil && runError != nil && !explicitStop && !errors.As(runError, &goalStop) {
		agentRecord.Status, agentRecord.Error = "interrupted", "service shut down"
	}

	_ = engine.saveAgent(agentRecord)
	phase := agentRecord.Status
	engine.setRunState(agentID, phase, runError)
}

func (engine *Engine) Stop(agentID string) error {
	engine.configMu.Lock()
	writes := []datastore.ResourceWrite{
		{
			Kind:   "agent_stop",
			ID:     agentID,
			Parent: agentID,
			Value: agentStopRecord{
				AgentID:   agentID,
				Requested: true,
			},
		},
	}

	goal, err := engine.CurrentGoal(agentID)
	if err != nil {
		engine.configMu.Unlock()
		return err
	}

	if goal != nil && goal.Status == "active" {
		goal.Status, goal.Reason = "paused", "Paused by the user"
		goal.RequestedStatus, goal.RequestReason = "", ""
		goal.Revision++
		goal.UpdatedAt = time.Now().UTC()
		writes = append(writes, goalWrites(*goal)...)
	}

	engine.mu.Lock()
	workerRunning := engine.running[agentID] != nil
	engine.mu.Unlock()

	if !workerRunning {
		var record domain.Agent
		if engine.Store.Load("agent", agentID, &record) == nil {
			record.Status, record.Error = "stopped", ""
			writes = append(writes, datastore.ResourceWrite{
				Kind:   "agent",
				ID:     agentID,
				Parent: record.ProjectID,
				Value:  record,
			})
		}
	}

	if err := engine.Store.SaveResources(writes); err != nil {
		engine.configMu.Unlock()
		return err
	}

	engine.mu.Lock()
	engine.stopRequested[agentID] = true

	if cancel := engine.running[agentID]; cancel != nil {
		cancel()
	}

	for _, job := range engine.jobs {
		job.mu.Lock()
		ownsJob := job.job.AgentID == agentID
		job.mu.Unlock()

		if ownsJob {
			job.cancel()
		}
	}

	engine.mu.Unlock()

	if !workerRunning {
		engine.setRunState(agentID, "stopped", nil)
		engine.Emit("agent", agentID, nil)
	}

	engine.configMu.Unlock()
	engine.Emit("goal", agentID, goal)
	engine.closeMCPServers(agentID)

	return nil
}
