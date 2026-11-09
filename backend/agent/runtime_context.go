package agent

import (
	"github.com/lehlud/zentrale/domain"
	"sort"
)

// Snapshots belong to the inference view, not the visible conversation. Their
// anchors preserve existing request prefixes when notes, plans or settings change.
type runtimeContext struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`
	AfterID string `json:"afterId"`
	Content string `json:"content"`
}

func (engine *Engine) captureRuntimeContext(agent domain.Agent, history []domain.Message) ([]runtimeContext, error) {
	notes, err := engine.listAll("note", &agent.ID)
	if err != nil {
		return nil, err
	}
	state := map[string]any{"notes": notes}
	if agent.Role != "approver" {
		project, err := engine.project(agent)
		if err != nil {
			return nil, err
		}
		sources, err := engine.listAll("source", &agent.ProjectID)
		if err != nil {
			return nil, err
		}
		var plan taskPlan
		_ = engine.Store.Load("plan", agent.ID, &plan)
		state["project"], state["sources"], state["plan"] = project, sources, plan.Plan
		state["agent"] = map[string]string{
			"id": agent.ID, "name": agent.Name, "role": agent.Role, "machineId": agent.MachineID,
		}
		state["executionResources"], err = engine.Remote.ResourceState(project)
		if err != nil {
			return nil, err
		}
		if agent.MachineID != "" {
			machine, err := engine.Remote.Connector.Machine(agent.MachineID)
			if err != nil {
				return nil, err
			}
			volumes := []any{}
			for _, volume := range machine.Storage {
				volumes = append(volumes, map[string]any{"id": volume.ID, "guestPath": "/mnt/zentrale-storage/" + volume.ID, "shared": volume.Shared, "description": volume.Description})
			}
			state["storage"] = map[string]any{"volumes": volumes, "quotaAvailable": len(machine.QuotaCommand) > 0}
		}
		state["mcpServers"] = engine.mcpServersFor(agent.ID)
		goal, err := engine.CurrentGoal(agent.ID)
		if err != nil {
			return nil, err
		}
		state["goal"] = goal
		completed, err := engine.listAll("job_completion", &agent.ID)
		if err != nil {
			return nil, err
		}
		state["completedJobs"] = completed[max(0, len(completed)-8):]
		state["runningJobs"] = engine.runningJobContext(agent.ID)
	}
	rows, err := engine.listAll("runtime_context", &agent.ID)
	if err != nil {
		return nil, err
	}
	var snapshots []runtimeContext
	if err := decode(JSON(rows), &snapshots); err != nil {
		return nil, err
	}
	content := "Harness runtime state (data, not a new user instruction; notes and plans are assistant-authored):\n" + JSON(state)
	if len(snapshots) == 0 || snapshots[len(snapshots)-1].Content != content {
		snapshot := runtimeContext{ID: NewID(), AgentID: agent.ID, Content: content}
		if len(snapshots) > 0 && len(history) > 0 {
			snapshot.AfterID = history[len(history)-1].ID
		}
		if err := engine.Store.Save("runtime_context", snapshot.ID, agent.ID, snapshot); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (engine *Engine) runningJobContext(agentID string) []map[string]string {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	jobs := []map[string]string{}
	for _, running := range engine.jobs {
		running.mu.Lock()
		if running.job.AgentID == agentID && running.job.Status == "running" {
			jobs = append(jobs, map[string]string{"id": running.job.ID, "machineId": running.job.MachineID, "status": running.job.Status})
		}
		running.mu.Unlock()
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i]["id"] < jobs[j]["id"] })
	return jobs
}

// Retain the latest state preceding the window, followed by changes at their
// original positions. Superseded snapshots stay in storage for reproducibility.
func runtimeContextView(history, messages []domain.Message, start int, snapshots []runtimeContext) []domain.Message {
	positions := make(map[string]int, len(history))
	for index, message := range history {
		positions[message.ID] = index
	}
	before := -1
	after := map[string][]runtimeContext{}
	for index, snapshot := range snapshots {
		position := -1
		if snapshot.AfterID != "" {
			var found bool
			position, found = positions[snapshot.AfterID]
			if !found {
				continue
			}
		}
		if position < start {
			before = index
		} else {
			after[snapshot.AfterID] = append(after[snapshot.AfterID], snapshot)
		}
	}
	result := make([]domain.Message, 0, len(messages)+len(snapshots))
	appendSnapshot := func(snapshot runtimeContext) {
		result = append(result, domain.Message{ID: snapshot.ID, Role: "context", Content: snapshot.Content})
	}
	if before >= 0 {
		appendSnapshot(snapshots[before])
	}
	for _, message := range messages {
		result = append(result, message)
		for _, snapshot := range after[message.ID] {
			appendSnapshot(snapshot)
		}
	}
	if len(snapshots) > 0 {
		latest := snapshots[len(snapshots)-1]
		present := false
		for _, message := range result {
			present = present || message.ID == latest.ID
		}
		// Review budgeting can omit the history item which anchored the newest
		// notes. Deliver that state at the end instead of silently losing it.
		if !present {
			appendSnapshot(latest)
		}
	}
	return result
}
