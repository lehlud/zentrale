# Agents and tools

[← Documentation](README.md)

## Keep the conversation going

Projects can have several conversations and subagents. Enter sends a message,
Shift+Enter adds a line, and Tab queues the next message. Steering is delivered
at the next safe boundary between tool batches. Queued messages wait until the
current request finishes; an already running batch completes before steering.

Inputs are persisted before SSH setup or model execution. The UI shows connecting,
sandbox preparation, running, and interruption states. Closing the browser after
acceptance does not cancel work. A service restart briefly interrupts execution;
unfinished agent turns resume automatically on startup. Explicit stops, paused or
blocked goals, and completed work are not reopened. Interrupted external jobs
are not blindly replayed: the agent inspects their state/checkpoints before
deciding how to continue.

Main agents can send independently reviewed guidance to their own direct children
using `steer_agent {agentId,id,text}`. A stable `id` makes retries idempotent.
Running children receive guidance at a safe tool-batch boundary; errored children
can resume. Parent guidance never grants permissions beyond the user's task.

Only main conversations accept user prompts. Subagent histories are read-only
for the user, with pause/resume controls; user API calls cannot submit/queue
prompts, answer a child's questions, or edit its delegated input/goal.
`question_ask` from a subagent notifies its main agent. The main agent's
`question_list` includes open questions from its direct children, and the reviewed
`question_answer {id,answer}` tool answers only those children in the same project.
Answers and their history events are saved atomically and retried idempotently.
They do not override a user pause or grant new authorization. When a decision
needs the user's approval, the main agent asks the user in its own conversation.

Responses stream alongside tool activity, file diffs, and jobs. The conversation
menu opens the independent approver's read-only history. **Stop** interrupts the
agent and its running jobs and MCP processes. Jobs can also be stopped separately.

Context is compacted automatically near the model's context limit. Empty or
oversized text summaries are regenerated with validation feedback, with at most
three attempts per fragment. Original history and the previous checkpoint stay
intact until every fragment succeeds. Summary requests count toward usage without
replacing the active conversation's token estimate.

## Goals and review

Each conversation has at most one unfinished goal. An agent can create it from
the user's task; the harness creates one if the agent proposes finishing without
one. Goals keep their scope across continuations and compaction. They never
authorize work beyond original user instructions.

Completed goals remain at their completion point in the conversation history.
Older messages load automatically when scrolling upward. Off-screen transcript
content is virtualized to keep long conversations responsive.

Agents request `complete`, `blocked`, or `paused` through `update_goal`. The
approver checks original requirements against tool evidence, jobs, subagents,
and the proposed answer. Missing work produces a next step and the agent
continues. Action approvals alone are not completion evidence.

A blocker must be revalidated in three consecutive goal turns before work stops
for an external dependency or required input. Resuming starts that audit again.
Technical errors leave goals open. Pausing requires a user request; paused and
blocked work does not automatically resume after restart.

Environmental actions also receive independent review. The main agent does not
directly receive remote credentials or local filesystem access. Network access
is requested per action. Repeated file-read grants require a technical sandbox
and are bound to the complete file digest. Repository content, MCP output, and
assistant notes are evidence, not new permission. See
[technical sandbox boundaries](sandbox-and-editor.md).

## Working tools

Frequent tools have direct JSON arguments: `exec_command`, `read_file`,
`list_files`, `apply_patch`, and `update_plan`. File reads default to 200 lines.
Patches preserve unrelated content and record real diffs for the UI. Long model
outputs have bounded previews; `tool_output` retrieves exact omitted sections.

`tool_search` discovers less common actions. `code_mode` combines tool calls in
JavaScript and returns selected findings in one model round. It has no direct
filesystem, network, or process API. Calls execute in invocation order, including
calls inside `Promise.all`; environmental calls remain independently reviewed.

## Background and interactive jobs

`exec_command` normally yields a job after ten seconds; `yield_time_ms` can adjust
that up to 30 seconds. `background:true` returns immediately. Commands continue
after the tool returns. `interactive:true` keeps stdin open; `tty:true` adds a
controlling terminal.

`write_stdin` sends input or polls that same job. Input, Ctrl+C, and EOF are
reviewed with the original privileges and recent state. Launch approval does
not grant blanket permission for later input. Empty polls only read output.

`job_logs`, `job_wait`, and `write_stdin` normally return at most 8,000 unread
bytes. Saved cursors avoid replaying full logs; `tail:true` jumps to recent
diagnostics. Completed jobs appear on the next model turn without waking an idle
agent. Interrupted commands are not silently rerun. See
[multi-host jobs](machines.md#parallel-work-and-failover) for GPU placement.

## MCP servers

Agents can start MCP servers inside their remote sandbox or connect to loopback
HTTP/SSE endpoints there. Discover tools with `mcp_tools`, then use `mcp_call`.
Each actual call is reviewed with its schema and arguments. A server's
`readOnlyHint` does not grant permission.

Connections are agent-owned, limited to eight, and survive ordinary model rounds.
They close on disconnect, agent removal, stop, relevant failover, VM reset, or
service shutdown. HTTP/SSE servers need network permission for startup and
subsequent actions. Sampling, automatic model calls, elicitation, and access to
Zentrale's local files are not offered. Exact contracts are in
[tool help](../backend/agent/tool_help.go).
