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
	// exec_command is the native transport alias for the canonical exec action.
	names := []string{"exec_command"}
	for name, policy := range toolPolicies {
		if !policy.HideInCodeMode {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names
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

type codeModeReceipt struct {
	Call         int    `json:"call"`
	Activity     int    `json:"activity,omitempty"`
	Tool         string `json:"tool"`
	Status       string `json:"status"`
	JobID        string `json:"jobId,omitempty"`
	ChangedFiles int    `json:"changedFiles,omitempty"`
}

type toolMessageIDKey struct{}

func (engine *Engine) runCodeMode(
	ctx context.Context,
	agent domain.Agent,
	arguments map[string]any,
) (any, error) {
	activities := []domain.ToolActivity{}
	changes := []domain.FileChange{}
	failures := []string{}
	receipts := []codeModeReceipt{}
	var connectionFailure *ProjectConnectionError
	names := codeModeTools()
	if engine.humanizerEnabled() {
		names = append(names, humanizerAction)
	}
	result, err := codemode.Run(ctx, stringArg(arguments, "code"), names, func(ctx context.Context, name string, args map[string]any) (any, error) {
		if connectionFailure != nil {
			return nil, connectionFailure
		}

		call := codeModeCall(name, args)
		receipts = append(receipts, codeModeReceipt{Call: len(receipts) + 1, Tool: name, Status: "started"})
		receipt := &receipts[len(receipts)-1]
		value, failure := engine.dispatch(ctx, agent, call)
		receipt.Status = "ok"
		if failure != nil {
			receipt.Status = "error"
		}

		fileChanges, extractionError := extractFileChanges(value)
		if extractionError != nil {
			receipt.Status = "result_error"
			preview, _ := outputPreview(extractionError.Error(), 500)
			failures = append(failures, name+": "+preview)
			return nil, extractionError
		}

		receipt.ChangedFiles = len(fileChanges)
		changes = append(changes, fileChanges...)
		content := JSON(map[string]any{"result": value})

		if failure != nil {
			content = JSON(map[string]any{"error": failure.Error(), "result": value})
			preview, _ := outputPreview(failure.Error(), 500)
			failures = append(failures, name+": "+preview)
			_ = errors.As(failure, &connectionFailure)
		}

		preview := modelHistory([]domain.Message{{Role: "tool", Content: content}}, 128000)
		request, decodeError := decodeInteraction(call)
		if decodeError == nil {
			name, args = request.Action, request.Arguments
		}

		switch value := value.(type) {
		case domain.Job:
			receipt.JobID = value.ID
		case jobOutputResult:
			receipt.JobID = value.ID
		case map[string]any:
			if job, ok := value["job"].(domain.Job); ok {
				receipt.JobID = job.ID
			} else if request.Action == "exec" {
				receipt.JobID = stringArg(value, "jobId")
			}
		}

		activities = append(activities, domain.ToolActivity{
			Name:      name,
			Arguments: JSON(args),
			Content:   preview[0].Content,

			Network:       request.Network,
			Justification: request.Justification,
		})
		receipt.Activity = len(activities)

		return value, failure
	})

	if connectionFailure != nil {
		err = connectionFailure
	}
	if err != nil && result.Calls == 0 && len(receipts) == 0 && strings.Contains(err.Error(), "SyntaxError") {
		err = structuralToolFeedback(ctx, interaction{Action: "code_mode", Arguments: arguments}, err)
	}

	response := map[string]any{
		"output":    result.Output,
		"calls":     result.Calls,
		"truncated": result.Truncated,

		"failures": failures,

		"_activities": activities,
		"_changes":    changes,
	}
	if messageID, _ := ctx.Value(toolMessageIDKey{}).(string); messageID != "" {
		response["messageId"] = messageID
	}
	if err != nil || len(failures) > 0 {
		response["receipts"] = receipts
		response["execution"] = "tool_calls_attempted"
		response["recovery"] = "Inspect receipts before retrying; tool_output {messageId,activity} reads a recorded inner result. Errors or missing receipts may still have effects; ok confirms a tool return, not completion of a background job."
		if result.Calls == 0 && len(receipts) == 0 {
			response["execution"] = "no_tool_calls_started"
			response["recovery"] = "No tool calls started; resolve the reported error before retrying."
		}
	}

	return response, err
}

func searchToolCatalog(query string, additionalEntries ...string) []string {
	terms := strings.Fields(strings.ToLower(query))
	exact := []string{}
	result := []string{}
	for _, line := range append(strings.Split(toolHelp, "\n"), additionalEntries...) {
		if !strings.Contains(line, "{") {
			continue
		}

		lower := strings.ToLower(line)
		name, _, _ := strings.Cut(lower, " ")
		if slices.Contains(terms, name) {
			exact = append(exact, line)
			continue
		}
		if len(result) == 8 {
			continue
		}

		for _, term := range terms {
			if strings.Contains(lower, term) {
				result = append(result, line)
				break
			}
		}
	}

	result = append(exact, result...)
	return result[:min(8, len(result))]
}
