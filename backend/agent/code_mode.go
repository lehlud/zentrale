package agent

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strings"

	"github.com/lehlud/zentrale/codemode"
	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func codeModeTools() []string {
	names := []string{
		"exec_command",
		"write_stdin",
		"read_file",
		"list_files",
		"apply_patch",
		"update_plan",

		"instructions_read",
		"note_save",
		"note_list",
		"note_read",
		"note_delete",

		"job_status",
		"job_logs",
		"job_wait",
		"job_stop",
		"tool_output",

		"create_goal",
		"get_goal",
		"update_goal",

		"question_ask",
		"question_list",
		"question_withdraw",
		"venv_cleanup",

		"mcp_list",
		"mcp_disconnect",
	}

	for name := range reviewedTools {
		names = append(names, name)
	}

	sort.Strings(names)

	return slices.Compact(names)
}

func codeModeCall(name string, arguments map[string]any) providers.Call {
	switch name {
	case "exec_command", "write_stdin", "read_file", "list_files", "apply_patch", "update_plan":
		return providers.Call{Name: name, Arguments: arguments}
	}

	parameters := make(map[string]any, len(arguments))

	for key, value := range arguments {
		if key != "network" && key != "justification" {
			parameters[key] = value
		}
	}

	return providers.Call{
		Name: "harness",
		Arguments: map[string]any{
			"action":    name,
			"arguments": JSON(parameters),

			"network":       boolArg(arguments, "network"),
			"justification": stringArg(arguments, "justification"),
		},
	}
}

func (engine *Engine) runCodeMode(
	ctx context.Context,
	agent domain.Agent,
	arguments map[string]any,
) (any, error) {
	activities := []domain.ToolActivity{}
	changes := []domain.FileChange{}
	failures := []string{}
	var connectionFailure *ProjectConnectionError
	result, err := codemode.Run(ctx, stringArg(arguments, "code"), codeModeTools(), func(name string, args map[string]any) (any, error) {
		if connectionFailure != nil {
			return nil, connectionFailure
		}

		call := codeModeCall(name, args)
		value, failure := engine.dispatch(ctx, agent, call)
		fileChanges, extractionError := extractFileChanges(value)
		if extractionError != nil {
			return nil, extractionError
		}

		changes = append(changes, fileChanges...)
		content := JSON(map[string]any{"result": value})

		if failure != nil {
			content = JSON(map[string]any{"error": failure.Error()})
			preview, _ := outputPreview(failure.Error(), 500)
			failures = append(failures, name+": "+preview)
			_ = errors.As(failure, &connectionFailure)
		}

		preview := modelHistory([]domain.Message{{Role: "tool", Content: content}}, 128000)
		request, decodeError := decodeInteraction(call)
		if decodeError == nil {
			name, args = request.Action, request.Arguments
		}

		activities = append(activities, domain.ToolActivity{
			Name:      name,
			Arguments: JSON(args),
			Content:   preview[0].Content,

			Network:       request.Network,
			Justification: request.Justification,
		})

		return value, failure
	})

	if connectionFailure != nil {
		err = connectionFailure
	}

	return map[string]any{
		"output":    result.Output,
		"calls":     result.Calls,
		"truncated": result.Truncated,

		"failures": failures,

		"_activities": activities,
		"_changes":    changes,
	}, err
}

func searchToolCatalog(query string) []string {
	terms := strings.Fields(strings.ToLower(query))
	result := []string{}
	for _, line := range strings.Split(toolHelp, "\n") {
		if !strings.Contains(line, "{") {
			continue
		}

		for _, term := range terms {
			if strings.Contains(strings.ToLower(line), term) {
				result = append(result, line)
				break
			}
		}

		if len(result) == 8 {
			break
		}
	}

	return result
}
