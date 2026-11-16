package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/lehlud/zentrale/domain"
)

// Validate the entire batch before starting. Only independent read-only actions
// are admitted; private notes/context mutations and recursive batches stay out.
func (engine *Engine) inspectBatch(ctx context.Context, record domain.Agent, args map[string]any) (any, error) {
	var batch struct {
		Items []struct {
			Action    string         `json:"action"`
			Arguments map[string]any `json:"arguments"`
		} `json:"items"`
	}
	if err := decode(JSON(args), &batch); err != nil {
		return nil, err
	}
	if len(batch.Items) < 1 || len(batch.Items) > 8 {
		return nil, errors.New("inspection batch requires 1..8 items")
	}
	for _, item := range batch.Items {
		switch item.Action {
		case "read_file", "list_files", "history", "instructions", "goal", "job_status", "job_logs", "resource_list", "storage_quota":
		default:
			return nil, errors.New("inspection batch contains an unsupported action")
		}
	}
	results := make([]map[string]any, len(batch.Items))
	var workers sync.WaitGroup
	// Two concurrent reads bound host pressure and share the review deadline.
	gate := make(chan struct{}, 2)
	for index, item := range batch.Items {
		workers.Add(1)
		go func() {
			defer workers.Done()
			select {
			case gate <- struct{}{}:
			case <-ctx.Done():
				results[index] = map[string]any{"index": index + 1, "action": item.Action, "error": ctx.Err().Error()}
				return
			}
			defer func() { <-gate }()
			value, err := engine.Inspect(ctx, record, item.Action, item.Arguments)
			result := map[string]any{"index": index + 1, "action": item.Action, "result": value}
			if err != nil {
				result["error"] = err.Error()
			}
			results[index] = result
		}()
	}
	workers.Wait()
	return map[string]any{"items": results}, ctx.Err()
}
