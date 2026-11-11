package agent

import (
	"context"
	"errors"
	"time"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

type toolPolicy struct {
	RequiresNetwork bool
}

var reviewedTools = map[string]toolPolicy{
	"apply_patch":        {},
	"read_file":          {},
	"list_files":         {},
	"write_file":         {},
	"delete_file":        {},
	"exec":               {},
	"python":             {},
	"venv_create":        {},
	"packages_install":   {RequiresNetwork: true},
	"microvm_reset":      {},
	"microvm_info":       {},
	"container":          {},
	"websearch":          {RequiresNetwork: true},
	"webfetch":           {RequiresNetwork: true},
	"github":             {RequiresNetwork: true},
	"clone_source":       {RequiresNetwork: true},
	"source_git":         {},
	"machine_list":       {},
	"machine_properties": {},
	"machine_select":     {},
	"resource_list":      {},
	"storage_list":       {},
	"storage_quota":      {},
	"subagent":           {},
	"steer_agent":        {},
	"inspect_agent":      {},
	"question_answer":    {},
	"mcp_connect":        {},
	"mcp_tools":          {},
	"mcp_call":           {},
}

func (engine *Engine) dispatch(ctx context.Context, agentRecord domain.Agent, call providers.Call) (any, error) {
	request, err := decodeInteraction(call)
	if err != nil {
		return nil, err
	}
	if request.Action == "microvm_reset" && (agentRecord.Role != "main" || agentRecord.ParentID != "") {
		return nil, errors.New("only the main agent can reset the shared environment")
	}

	switch request.Action {
	case "write_stdin":
		return engine.writeStdin(ctx, agentRecord, request)
	case "mcp_connect", "mcp_list", "mcp_tools", "mcp_call", "mcp_disconnect":
		return engine.mcpTool(ctx, agentRecord, request)
	}

	if result, handled, err := engine.internalTool(ctx, agentRecord, request); handled {
		return result, err
	}

	policy, exists := reviewedTools[request.Action]
	if !exists {
		return nil, errors.New("unknown action")
	}

	if policy.RequiresNetwork && !request.Network {
		return nil, errors.New("request network=true for this individual interaction")
	}

	if boolArg(request.Arguments, "outsideSandbox") && request.Justification == "" {
		return nil, errors.New("outsideSandbox requires justification and script inspection")
	}

	if request.Action == "read_file" {
		return engine.approvedRead(ctx, agentRecord, request.Arguments, request.Justification, request.Network)
	}

	if _, err := engine.approve(ctx, agentRecord, request); err != nil {
		return nil, err
	}

	return engine.reviewedTool(ctx, agentRecord, request)
}

func decodeInteraction(call providers.Call) (interaction, error) {
	if call.Name != "harness" {
		return decodeNativeInteraction(call)
	}

	var arguments map[string]any
	if err := decode(stringArg(call.Arguments, "arguments"), &arguments); err != nil || arguments == nil {
		return interaction{}, errors.New("arguments must encode a JSON object")
	}

	return interaction{
		Action:        stringArg(call.Arguments, "action"),
		Arguments:     arguments,
		Justification: stringArg(call.Arguments, "justification"),
		Network:       boolArg(call.Arguments, "network"),
	}, nil
}

func decodeNativeInteraction(call providers.Call) (interaction, error) {
	arguments := make(map[string]any, len(call.Arguments))

	for key, value := range call.Arguments {
		if key != "justification" && key != "network" {
			arguments[key] = value
		}
	}

	action := call.Name
	requiredArgument := "path"

	switch call.Name {
	case "exec_command":
		action, requiredArgument = "exec", "command"
		arguments["command"] = stringArg(arguments, "cmd")
		delete(arguments, "cmd")

		if _, exists := arguments["yield_time_ms"]; !exists {
			arguments["yield_time_ms"] = float64(10000)
		}

		if _, err := outputInteger(arguments, "yield_time_ms", 10000, 1000, 30000); err != nil {
			return interaction{}, err
		}
	case "read_file":
		if _, exists := arguments["limit"]; !exists {
			arguments["limit"] = float64(200)
		}
	case "list_files":
	case "apply_patch":
		requiredArgument = "input"
	case "update_plan":
		requiredArgument = ""
	case "create_goal":
		requiredArgument = "objective"
	case "get_goal", "update_goal":
		requiredArgument = ""
	case "code_mode":
		requiredArgument = "code"
	case "tool_search":
		requiredArgument = "query"
	case "write_stdin":
		requiredArgument = "id"
	case "steer_agent":
		requiredArgument = "agentId"
	default:
		return interaction{}, errors.New("unknown tool")
	}

	if requiredArgument != "" && stringArg(arguments, requiredArgument) == "" {
		return interaction{}, errors.New(requiredArgument + " is required")
	}

	return interaction{
		Action:        action,
		Arguments:     arguments,
		Justification: stringArg(call.Arguments, "justification"),
		Network:       boolArg(call.Arguments, "network"),
	}, nil
}

func (engine *Engine) internalTool(ctx context.Context, agentRecord domain.Agent, request interaction) (result any, handled bool, err error) {
	switch request.Action {
	case "code_mode":
		result, err = engine.runCodeMode(ctx, agentRecord, request.Arguments)
	case "tool_search":
		result = searchToolCatalog(stringArg(request.Arguments, "query"))
	case "instructions_read":
		result, err = engine.ownInstructions(agentRecord)
	case "update_plan":
		result, err = engine.updatePlan(agentRecord, request.Arguments)
	case "create_goal", "get_goal", "update_goal":
		result, err = engine.goalTool(agentRecord, request.Action, request.Arguments)
	case "tool_output":
		result, err = engine.toolOutput(agentRecord, request.Arguments)
	case "note_save", "note_list", "note_read", "note_delete":
		result, err = engine.note(agentRecord, request.Action, request.Arguments)
	case "context_clear", "context_rotate", "context_compact":
		result, err = engine.manageContext(ctx, agentRecord, request.Action, request.Arguments)
	case "question_ask", "question_list", "question_withdraw":
		result, err = engine.question(agentRecord, request.Action, request.Arguments)
	case "job_status", "job_logs", "job_wait", "job_stop":
		result, err = engine.jobToolArguments(ctx, agentRecord, request.Action, request.Arguments)
	case "venv_cleanup":
		result, err = engine.cleanupVenv(agentRecord, stringArg(request.Arguments, "id"))
	default:
		return nil, false, nil
	}

	return result, true, err
}

func (engine *Engine) reviewedTool(ctx context.Context, agentRecord domain.Agent, request interaction) (any, error) {
	arguments := request.Arguments

	switch request.Action {
	case "list_files", "write_file", "delete_file", "apply_patch":
		if request.Action != "list_files" {
			engine.invalidate(agentRecord.ID)
		}

		return engine.file(ctx, agentRecord, request.Action, arguments, false, false)
	case "exec":
		yield, err := outputInteger(
			arguments,
			"yield_time_ms",
			int(foregroundCommandLimit.Milliseconds()),
			1000,
			int(foregroundCommandLimit.Milliseconds()),
		)
		if err != nil {
			return nil, err
		}

		outputLimit, err := outputInteger(arguments, "max_output_bytes", 8000, 256, 64000)
		if err != nil {
			return nil, err
		}

		return engine.executeRemote(ctx, agentRecord, executionRequest{
			MachineID:      stringArg(arguments, "machineId"),
			GPU:            boolArg(arguments, "gpu"),
			Background:     boolArg(arguments, "background"),
			Command:        stringArg(arguments, "command"),
			Network:        request.Network,
			OutsideSandbox: boolArg(arguments, "outsideSandbox"),
			GuestRoot:      boolArg(arguments, "guestRoot"),
			YieldTime:      time.Duration(yield) * time.Millisecond,
			Interactive:    boolArg(arguments, "interactive"),
			TTY:            boolArg(arguments, "tty"),
			OutputBytes:    outputLimit,
		})
	case "packages_install":
		return engine.installPackages(ctx, agentRecord, arguments)
	case "microvm_reset", "microvm_info":
		return engine.microVMTool(ctx, agentRecord, request.Action)
	case "python":
		return engine.runPython(ctx, agentRecord, arguments, request.Network)
	case "venv_create":
		return engine.createVenv(ctx, agentRecord, arguments, request.Network)
	case "container":
		return engine.runContainer(ctx, agentRecord, request)
	case "websearch":
		return engine.searchWeb(ctx, agentRecord, stringArg(arguments, "query"))
	case "webfetch":
		return engine.research(ctx, agentRecord, stringArg(arguments, "url"), boolArg(arguments, "raw"))
	case "github":
		return engine.github(ctx, arguments)
	case "clone_source":
		return engine.cloneSource(ctx, agentRecord, stringArg(arguments, "sourceId"))
	case "source_git":
		return engine.sourceGitTool(ctx, agentRecord, request)
	case "machine_list":
		return engine.listProjectMachines(agentRecord, arguments)
	case "machine_properties":
		return engine.listMachineProperties(agentRecord)
	case "resource_list":
		project, err := engine.project(agentRecord)
		if err != nil {
			return nil, err
		}

		return engine.Remote.ResourceState(project)
	case "storage_list":
		return engine.storageList(ctx, agentRecord)
	case "storage_quota":
		return engine.storageQuota(ctx, agentRecord, arguments)
	case "machine_select":
		return engine.selectProjectMachine(ctx, agentRecord, stringArg(arguments, "machineId"))
	case "subagent":
		return engine.spawn(agentRecord, arguments)
	case "steer_agent":
		return engine.steerChildAgent(agentRecord, arguments)
	case "inspect_agent":
		return engine.inspectChildAgent(agentRecord, stringArg(arguments, "agentId"))
	case "question_answer":
		return engine.answerChildQuestion(agentRecord, arguments)
	default:
		return nil, errors.New("unknown reviewed tool")
	}
}
