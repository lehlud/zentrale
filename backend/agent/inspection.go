package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/lehlud/zentrale/domain"
)

func (engine *Engine) Inspect(ctx context.Context, agentRecord domain.Agent, action string, args map[string]any) (any, error) {
	switch action {
	case "instructions":
		return engine.listAll("instruction", &agentRecord.ProjectID)
	case "goal":
		return engine.CurrentGoal(agentRecord.ID)
	case "storage_quota":
		return engine.storageQuota(ctx, agentRecord, args)
	case "resource_list":
		project, err := engine.project(agentRecord)
		if err != nil {
			return nil, err
		}
		return engine.Remote.ResourceState(project)
	case "job_status", "job_logs":
		var job domain.Job
		if err := engine.Store.Load("job", stringArg(args, "id"), &job); err != nil {
			return nil, err
		}
		if job.AgentID != agentRecord.ID {
			return nil, errors.New("job belongs to another agent")
		}
		engine.mu.Lock()
		running := engine.jobs[job.ID]
		engine.mu.Unlock()
		if running != nil {
			running.mu.Lock()
			job = running.job
			if action == "job_logs" && running.output != nil {
				window := running.output.Window()
				start := uint64(0)
				if window.Total > 8000 {
					start = window.Total - 8000
				}
				job.Output, _, _ = readJobWindow(window, start, 8000, job.Status != "running")
			}
			running.mu.Unlock()
		}
		if action == "job_status" {
			job.Output = ""
		} else if len(job.Output) > 8000 {
			job.Output = job.Output[len(job.Output)-8000:]
		}
		return job, nil
	case "history":
		id := stringArg(args, "agentId")
		if id == "" {
			id = agentRecord.ID
		}
		var target domain.Agent
		if err := engine.Store.Load("agent", id, &target); err != nil {
			return nil, err
		}
		if target.ProjectID != agentRecord.ProjectID {
			return nil, errors.New("history is outside project")
		}
		messages, err := engine.loadHistory(id)
		if err != nil {
			return nil, err
		}
		query := stringArg(args, "search")
		if query == "" {
			return messages, nil
		}
		result := []domain.Message{}
		for _, m := range messages {
			if strings.Contains(strings.ToLower(m.Content+m.Wire), strings.ToLower(query)) {
				result = append(result, m)
			}
		}
		return result, nil
	case "grants":
		return engine.activeGrants(ctx, agentRecord)
	case "read_file", "list_files":
		return engine.file(ctx, agentRecord, action, args, false, false)
	default:
		return nil, errors.New("unknown read-only inspection")
	}
}
