package agent

import (
	"encoding/json"

	"github.com/lehlud/zentrale/domain"
	"github.com/lehlud/zentrale/providers"
)

func objectSchema(properties map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}

	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

func toolsForModel(model domain.LanguageModel) []providers.Tool {
	if model.ContextWindow > 0 && model.ContextWindow < 8192 {
		return []providers.Tool{mainTool()}
	}

	return mainTools()
}

func mainTool() providers.Tool {
	return providers.Tool{
		Name:        "harness",
		Description: "Use Zentrale's tools. Environmental actions require approval. Request network=true for each interaction that needs internet access.",
		Schema: objectSchema(map[string]any{
			"action": map[string]any{"type": "string"},
			"arguments": map[string]any{
				"type":        "string",
				"description": "Action-specific JSON object encoded as a string",
			},
			"justification": map[string]any{"type": "string"},
			"network":       map[string]any{"type": "boolean"},
		}, "action", "arguments"),
	}
}

// Keep frequent operations explicit and typed. The compact dispatcher also
// exposes less common operations without sending the entire catalog each turn.
func mainTools() []providers.Tool {
	return []providers.Tool{
		{
			Name: "code_mode",
			Description: "Run JavaScript to combine reviewed tools and print only relevant results, avoiding extra model rounds. " +
				"Use await tools.read_file({path}), tools.exec_command({cmd}), tools.apply_patch({input}), " +
				"tools.update_plan({...}) and other tool actions as tools.<action>({arguments,network?,justification?}). " +
				"Use print(value) or text(value) for model-visible output. Tool results are objects; failures contain error. " +
				"Calls run in invocation order, including Promise.all. No filesystem/network/process access in JavaScript. " +
				"32 calls, 64 KiB code, 16 KB output, 2 minutes. Context changes and recursive code_mode are unavailable.",
			Schema: objectSchema(map[string]any{
				"code": textProperty("JavaScript body; top-level await is supported."),
			}, "code"),
		},
		{
			Name:        "tool_search",
			Description: "Find less common tool actions and their argument forms. Use returned actions through tools.<action> in code_mode or the harness dispatcher.",

			Schema: objectSchema(map[string]any{
				"query": textProperty("Terms such as packages, git, job or machine."),
			}, "query"),
		},
		executionTool(),
		stdinTool(),
		fileReadTool(),
		fileListTool(),
		patchTool(),
		{
			Name:        "steer_agent",
			Description: "Send durable steering to your own subagent in this project, waking a running child or resuming an errored/idle child. Main agents only; never target unrelated agents or approvers. Guidance must stay within the user's existing permissions and scope; it grants no new authorization. Independently reviewed. Reuse id with identical text to retry safely; use a new id for a new instruction. Returns the durable input receipt, not a completion guarantee.",
			Schema: objectSchema(map[string]any{
				"agentId": textProperty("ID of your direct child subagent in the same project."),
				"id":      textProperty("Idempotency key scoped to this parent/child pair: 1–128 letters, digits, underscores or hyphens."),
				"text":    textProperty("Explicit task steering within the original user's scope and permissions. The durable message, including harness provenance, must fit within 100000 bytes."),
			}, "agentId", "id", "text"),
		},
		planTool(),
		{
			Name:        "create_goal",
			Description: "Create a persistent goal for the user's task. One unfinished goal per conversation. Preserve the full original scope; this never authorizes new work.",
			Schema: objectSchema(
				map[string]any{
					"objective": textProperty("Concrete objective covering the complete user task."),
				},
				"objective",
			),
		},
		{
			Name:        "get_goal",
			Description: "Read the current persistent goal, its status, review evidence and blocked-audit progress.",
			Schema:      objectSchema(map[string]any{}),
		},
		{
			Name:        "update_goal",
			Description: "Request completion, a genuine blocker, or an explicit user-requested pause. An independent audit verifies the request when the turn ends; the goal stays active until then. Blocked requires the same impasse in three consecutive goal turns.",
			Schema: objectSchema(map[string]any{
				"status": map[string]any{"type": "string", "enum": []string{"complete", "blocked", "paused"}},
				"reason": textProperty("Evidence for completion, the blocker, or the user's explicit pause request."),
			}, "status"),
		},
		mainTool(),
	}
}

func textProperty(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integerProperty(minimum, maximum int) map[string]any {
	return map[string]any{"type": "integer", "minimum": minimum, "maximum": maximum}
}

func executionTool() providers.Tool {
	return providers.Tool{
		Name: "exec_command",
		Description: "Run a POSIX shell command in the remote project and sandbox. " +
			"Choose an explicit finite timeout_ms appropriate for this command, including background and interactive jobs. " +
			"Network and exceptional execution are reviewed independently for each call. " +
			"Long commands return a jobId. Set interactive=true to keep stdin open, or tty=true for a real terminal. " +
			"Use write_stdin for input or polling. job_wait/job_logs return only unread output with nextCursor.",
		Schema: objectSchema(map[string]any{
			"cmd":       textProperty("Shell command. Batch independent inspections when their inputs are already known."),
			"machineId": textProperty("Optional exact assigned machine for this job; does not change the agent's default machine."),
			"gpu": map[string]any{
				"type":        "boolean",
				"description": "Reserve a configured GPU host for this job. Without machineId, choose a free GPU host within pool limits; never use CPU fallback.",
			},
			"background": map[string]any{
				"type":        "boolean",
				"description": "Return immediately with a jobId so independent jobs can overlap, including on different GPU hosts.",
			},
			"yield_time_ms":    integerProperty(1000, 30000),
			"timeout_ms":       integerProperty(1, 604800000),
			"interactive":      map[string]any{"type": "boolean"},
			"tty":              map[string]any{"type": "boolean"},
			"max_output_bytes": integerProperty(256, 64000),
			"network":          map[string]any{"type": "boolean"},
			"outsideSandbox":   map[string]any{"type": "boolean"},
			"guestRoot":        map[string]any{"type": "boolean"},
			"justification":    textProperty("Why exceptional access or network is needed, when applicable."),
		}, "cmd", "timeout_ms"),
	}
}

func stdinTool() providers.Tool {
	return providers.Tool{
		Name: "write_stdin",
		Description: "Send reviewed input to your running interactive job, or poll with empty chars. " +
			"Use its original network setting. Text, control characters and EOF are reviewed against the exact command and sandbox privileges. " +
			"Returns only unread output and nextCursor; no process is relaunched.",
		Schema: objectSchema(map[string]any{
			"id":               textProperty("Job ID returned by exec_command."),
			"chars":            textProperty("UTF-8 input, including newline to submit, or control characters such as \\u0003 for Ctrl+C. At most 16 KB."),
			"eof":              map[string]any{"type": "boolean"},
			"yield_time_ms":    integerProperty(0, 30000),
			"cursor":           textProperty("Optional output continuation cursor; omit to read from your saved position."),
			"max_output_bytes": integerProperty(256, 64000),
			"network":          map[string]any{"type": "boolean"},
			"justification":    textProperty("Why this input is needed."),
		}, "id"),
	}
}

func fileReadTool() providers.Tool {
	return providers.Tool{
		Name: "read_file",
		Description: "Read an exact, reviewed file excerpt. offset is a 1-based line number; " +
			"limit defaults to 200 lines. hasMore and totalLines identify additional content. " +
			"Request only relevant ranges.",
		Schema: objectSchema(map[string]any{
			"path":          textProperty("Project-relative or absolute project file path."),
			"machineId":     textProperty("Optional exact assigned job machine to inspect; does not change the default connection."),
			"offset":        integerProperty(1, 10000000),
			"limit":         integerProperty(1, 2000),
			"justification": textProperty("Reason for this read, if needed."),
		}, "path"),
	}
}

func fileListTool() providers.Tool {
	return providers.Tool{
		Name: "list_files",
		Description: "List names in a remote project directory. " +
			"Prefer exec_command with rg --files for a targeted recursive file search.",
		Schema: objectSchema(map[string]any{
			"path":          textProperty("Directory path, relative to the project or absolute."),
			"machineId":     textProperty("Optional exact assigned job machine to inspect."),
			"justification": textProperty("Reason for this inspection, if needed."),
		}, "path"),
	}
}

func patchTool() providers.Tool {
	return providers.Tool{
		Name: "apply_patch",
		Description: "Edit project files with a focused patch instead of resending whole files. " +
			"Format: *** Begin Patch; *** Add File: path with +lines; " +
			"*** Update File: path with @@ context, space context lines, -old and +new lines; " +
			"optional *** Move to: path; *** Delete File: path; *** End Patch. " +
			"Each marker is on its own line. A failed context or invalid path leaves files unchanged.",
		Schema: objectSchema(map[string]any{
			"input":         textProperty("The complete patch in the *** Begin Patch / *** End Patch format."),
			"justification": textProperty("Reason for the change, if needed."),
		}, "input"),
	}
}

func planTool() providers.Tool {
	step := objectSchema(map[string]any{
		"step": textProperty("Concrete task step."),
		"status": map[string]any{
			"type": "string",
			"enum": []string{"pending", "in_progress", "completed"},
		},
	}, "step", "status")

	return providers.Tool{
		Name: "update_plan",
		Description: "Maintain a short persistent plan for a multi-step task. Skip this for simple tasks. " +
			"Mark finished steps completed, keep at most one step in_progress " +
			"and update the plan when the approach changes.",
		Schema: objectSchema(map[string]any{
			"explanation": textProperty("Brief reason for this plan or change."),
			"plan": map[string]any{
				"type": "array", "minItems": 1, "maxItems": 20, "items": step,
			},
		}, "plan"),
	}
}

func inspectTool() providers.Tool {
	return providers.Tool{
		Name: "inspect",
		Description: `Read-only inspection: read_file {path,machineId?,offset?:1-based line,limit?:lines}, list_files {path,machineId?}, history {search,agentId}, instructions {}, grants {}, goal {}, job_status {id}, job_logs {id}, storage_quota {machineId?} (fixed configured host query), resource_list {}.
Private context tools: tool_output {messageId,offset,limit} for exact slices of your own abbreviated tool results, note_save {id?,title,content}, note_list {}, note_read {id}, note_delete {id}, context_clear {}, context_rotate {start}, context_compact {}.
The arguments field contains a JSON object string. Commands and project mutations are unavailable.`,
		Schema: objectSchema(map[string]any{
			"action":    map[string]any{"type": "string"},
			"arguments": map[string]any{"type": "string"},
		}, "action", "arguments"),
	}
}

func decode(encoded string, destination any) error {
	return json.Unmarshal([]byte(encoded), destination)
}

func stringArg(arguments map[string]any, key string) string {
	value, _ := arguments[key].(string)
	return value
}

func boolArg(arguments map[string]any, key string) bool {
	value, _ := arguments[key].(bool)
	return value
}
