package agent

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/remote"
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
		state["sources"], state["plan"] = sources, plan.Plan
		state["agent"] = map[string]string{
			"id":        agent.ID,
			"name":      agent.Name,
			"role":      agent.Role,
			"machineId": agent.MachineID,
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

			level := machine.Sandbox
			if status := engine.SandboxStatus(machine.ID); status != nil && status.Error == "" {
				level = status.Level
			}

			paths := remote.PathsForExecution(machine, level, project.Directory, "")
			project.Directory = paths.Workspace

			volumes := []any{}
			for _, volume := range machine.Storage {
				volumes = append(
					volumes,
					map[string]any{
						"id":          volume.ID,
						"guestPath":   "/mnt/zentrale-storage/" + volume.ID,
						"shared":      volume.Shared,
						"description": volume.Description,
					},
				)
			}

			state["storage"] = map[string]any{
				"volumes":        volumes,
				"quotaAvailable": len(machine.QuotaCommand) > 0,
			}
		}

		state["project"] = project
		state["mcpServers"] = engine.mcpServersFor(agent.ID)
		goal, err := engine.CurrentGoal(agent.ID)
		if err != nil {
			return nil, err
		}

		state["goal"] = goal
		board, err := engine.CurrentTeamwork(agent.ID)
		if err != nil {
			return nil, err
		}

		if board != nil && board.Status == "active" {
			state["teamwork"], err = teamworkView(board, map[string]any{})
			if err != nil {
				return nil, err
			}
		}

		state["questions"], err = engine.openQuestions(agent)
		if err != nil {
			return nil, err
		}
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
			jobs = append(
				jobs,
				map[string]string{
					"id":        running.job.ID,
					"machineId": running.job.MachineID,
					"status":    running.job.Status,
				},
			)
		}

		running.mu.Unlock()
	}

	sort.Slice(jobs, func(i, j int) bool { return jobs[i]["id"] < jobs[j]["id"] })

	return jobs
}

// Retain the latest state preceding the window, followed by changes at their
// original positions. Superseded snapshots stay in storage for reproducibility.
func runtimeContextView(
	history, messages []domain.Message,
	start int,
	snapshots []runtimeContext,
) []domain.Message {
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
	var previousState map[string]json.RawMessage
	appendSnapshot := func(snapshot runtimeContext) {
		content, state := runtimeContextUpdate(snapshot.Content, previousState)
		result = append(result, domain.Message{ID: snapshot.ID, Role: "context", Content: content})
		previousState = state
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

// Stored snapshots remain complete. In a model window, send a full baseline
// followed by smaller field or item updates. Appending a snapshot never rewrites
// earlier messages; a rotated/compacted window gets a new full baseline, so no
// update depends on state outside that window.
func runtimeContextUpdate(content string, previous map[string]json.RawMessage) (string, map[string]json.RawMessage) {
	_, encoded, found := strings.Cut(content, "\n")
	var current map[string]json.RawMessage
	if !found || json.Unmarshal([]byte(encoded), &current) != nil || current == nil {
		return content, nil
	}

	if previous == nil {
		return content, current
	}

	changes := map[string]json.RawMessage{}
	for key, value := range current {
		if string(previous[key]) != string(value) {
			changes[key] = value
		}
	}

	for key := range previous {
		if _, exists := current[key]; !exists {
			changes[key] = json.RawMessage("null")
		}
	}

	update := "Harness runtime state update (data, not user authorization; replace listed top-level fields, keep all others; null removes a field):\n" + JSON(changes)
	fields := make(map[string]json.RawMessage, len(changes))
	for key, value := range changes {
		fields[key] = value
	}
	collections := map[string]runtimeItemUpdate{}
	for _, key := range []string{"completedJobs", "notes", "runningJobs"} {
		value, changed := changes[key]
		if !changed {
			continue
		}

		items, ok := runtimeItemsUpdate(previous[key], value)
		if !ok || len(JSON(items)) >= len(value) {
			continue
		}

		delete(fields, key)
		collections[key] = items
	}

	if len(collections) > 0 {
		itemUpdate := "Harness runtime item update (data, not user authorization): fields replaces listed fields (null removes); collections upserts items by id, then retains/orders exactly ids. Keep unlisted fields.\n" + JSON(map[string]any{
			"fields": fields, "collections": collections,
		})
		if len(itemUpdate) < len(update) {
			update = itemUpdate
		}
	}

	if len(update) >= len(content) {
		return content, current
	}

	return update, current
}

type runtimeItemUpdate struct {
	Upsert []json.RawMessage `json:"upsert"`
	IDs    []string          `json:"ids"`
}

// IDs provide both membership and ordering, so removed or reordered records do
// not require resending unchanged contents. Unknown/ambiguous arrays fall back
// to a complete field replacement instead of risking an incomplete update.
func runtimeItemsUpdate(previous, current json.RawMessage) (runtimeItemUpdate, bool) {
	before, _, ok := runtimeItems(previous)
	if !ok {
		return runtimeItemUpdate{}, false
	}
	after, ids, ok := runtimeItems(current)
	if !ok {
		return runtimeItemUpdate{}, false
	}

	update := runtimeItemUpdate{Upsert: []json.RawMessage{}, IDs: ids}
	for _, id := range ids {
		if string(before[id]) != string(after[id]) {
			update.Upsert = append(update.Upsert, after[id])
		}
	}

	return update, true
}

func runtimeItems(encoded json.RawMessage) (map[string]json.RawMessage, []string, bool) {
	var items []json.RawMessage
	if json.Unmarshal(encoded, &items) != nil || items == nil {
		return nil, nil, false
	}

	indexed := make(map[string]json.RawMessage, len(items))
	ids := make([]string, 0, len(items))
	for _, item := range items {
		var identity struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(item, &identity) != nil || identity.ID == "" || indexed[identity.ID] != nil {
			return nil, nil, false
		}
		indexed[identity.ID] = item
		ids = append(ids, identity.ID)
	}

	return indexed, ids, true
}
