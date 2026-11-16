# Efficient agent workflows

## Research baseline (2026-11-15)

Public source checkouts were inspected, not installed or executed. They live in
the ignored `frontend/artifacts/harness-research` directory. The implementation
here uses our existing Go/React boundaries, not another harness as a dependency.

| Primary source                                                                                                                                                                                                                                                           | Useful contract                                                                                                                                                        | Decision for Zentrale                                                                                                                                                                                                                            |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| [Codex](https://github.com/openai/codex/tree/ee344ca9458351a6ea102795fa73110ad3ede5dc), especially `codex-rs/core/src/tools/parallel.rs`, `agent/control/budget.rs` and `tui/src/chatwidget/side.rs`                                                                     | Separate admission, cancellation, presentation and durable history; shared resource accounting; side conversations do not commandeer the main composer.                | Keep approval and execution boundaries intact. Add a separate, cancellable side conversation and reduce observation overhead rather than replaying or speculatively executing tools.                                                             |
| [OpenCode](https://github.com/anomalyco/opencode/tree/d432a06b9c07bab0be4e8d642173b1cb14137f11), `packages/core/src/session/compaction.ts`                                                                                                                               | Account for measured input plus new text; reserve response room; preserve a verbatim tail; do not put every historical output into every summary.                      | Retain our existing native/text compaction and original-instruction preservation. Prefer bounded history retrieval before another expensive summary. Do not add speculative background summaries without evidence that their cost is worthwhile. |
| [Pi](https://github.com/badlogic/pi-mono/tree/04fd0897e4df737cd8422bc99085d6953da804ec), `packages/agent/README.md`, `packages/coding-agent/src/core/tools/truncate.ts`                                                                                                  | Distinguish steering from follow-up; explicitly bound output; concurrent work still has deterministic persisted ordering.                                              | Reuse our existing durable steering/queue path. Make compact observations recoverable and bound each retrieval by messages and bytes. Do not parallelize arbitrary side-effecting calls.                                                         |
| [Claude Code best practices](https://code.claude.com/docs/en/best-practices), [agent teams](https://code.claude.com/docs/en/agent-teams)                                                                                                                                 | Verification criteria, deliberate context management, focused workers, shared tasks and ownership. Teams cost more tokens and are unsuitable for tightly coupled work. | Direct work remains the default. Explicit teamwork gets a shared, dependency-aware work board, not an unconditional swarm.                                                                                                                       |
| [Antigravity research](https://research.google/blog/advancing-science-engineering-with-antigravitys-teamwork-feature/), [teamwork patterns](https://antigravity.google/blog/teamwork-when-ai-becomes-a-research-partner), [boost](https://antigravity.google/docs/boost) | Route by problem structure; partition context; use executable falsifiers; separate proposing a solution from establishing that it works.                               | Choose direct solve-and-verify for coupled tasks, parallel ownership for independent work, competing hypotheses for uncertain diagnosis. Stop at evidence, not agreement between models. No automatic model tournaments.                         |
| [A2UI 1.0](https://developers.googleblog.com/a2ui-10-build-delightful-fast-reliable-agentic-experiences/)                                                                                                                                                                | Models describe structured data; the application owns native, accessible, allowlisted components and actions.                                                          | Use typed native views for teamwork and questions. Do not execute model HTML/JavaScript or add the full protocol merely to render a task list. This is an adopted design principle, not a claim of A2UI protocol compatibility.                  |

These sources describe their own products. Their benchmark and marketing claims
are not measurements of Zentrale. No third-party prompts become instructions or
permissions in this project.

## Integration contracts

### Automatic efficiency

- Prefer a direct inspect/change/test loop for ordinary tasks. Select a workflow
  from dependency structure and uncertainty, not keywords or an extra routing
  model call. Independent tool work does not require an extra agent.
- Retrieve history on demand using stable cursors and bounded excerpts, with
  exact slices available by message ID. Never claim that an excerpt is complete.
- Persist original tool results. Compact model-only observations may omit
  repeated launch commands, but must retain errors, status, output, cursors and
  an explicit route to the original record.
- Preserve original user instructions, independent approval, cancellation,
  no-replay guarantees and provider-native reasoning boundaries.

### Composer commands

- Type `/` in the existing composer to open the native HeroUI autocomplete.
  Prefixes filter the menu; arrow keys choose a command, Enter executes it,
  and Escape closes the menu without discarding the draft. Shift+Enter remains
  a newline; ordinary text still uses Enter to send and Tab to queue.
- `/side [question]` opens the temporary side conversation. `/teamwork` opens
  the shared board; `/teamwork <task>` remains a durable request to the main agent.
- `/continue` resumes work and `/stop` stops the current agent through the same
  existing lifecycle mutations. Failed commands preserve the draft for retry.
  Stop is also available in the top-right actions menu, not beside the input.
- Add commands in `frontend/src/composer-commands.ts`: one definition supplies
  matching, the translated menu description and dispatch. The composer does not
  need another button or command-specific keyboard branch.

### `/side`

- A modal owns one ephemeral conversation. It has its own messages and provider
  context, but creates no durable agent, goal, job, VM or main-thread message.
- The only tools are read-only, scoped thread retrieval. The scope contains the
  selected main agent and its subagents in that project, not private approvers,
  unrelated agents, provider credentials, files, shell tools or mutations.
- The full public thread remains accessible through pagination/search, not an
  unconditional full-history prompt. History is untrusted data, not authority.
- Closing the modal/navigation/socket cancels outstanding inference and frees
  the conversation. Tab/browser loss is detected by socket close or a bounded
  heartbeat deadline; browsers cannot guarantee delivery of unload events.
- Use the main agent's configured provider/model, bounded inference rounds and
  context, explicit usage attribution, same-origin/session authentication and
  limits on concurrent sessions. Never silently select a different paid model.

### Explicit teamwork

- A team remains rooted in an existing main agent. Reuse its existing workers
  and approval model; a board is not permission to execute or spawn agents.
- Work items carry a goal, bounded ownership, dependencies, state, result and
  verification evidence. Claims and transitions are atomic and scope checked.
- Failed or blocked dependencies are visible. Only ready work can be claimed;
  conflicting ownership is rejected rather than resolved by concurrent writes.
- The main agent integrates and verifies results. A worker declaring completion
  is not proof that a test passed. The normal goal-completion audit still applies.
- Native UI shows the shared board. An explicit command may request teamwork,
  but normal user requests keep the direct-work default unless parallelism has
  a concrete expected net benefit.

## Verification

### Implemented boundaries

- `agent/side.go`, `webapi/side.go` and the native side modal implement one
  volatile conversation per socket, at most eight concurrent sessions, a
  30-minute socket lease and a two-minute/six-round limit per answer. Context
  is bounded by 12,000 estimated tokens or 60% of the configured model window,
  whichever is smaller. Usage is recorded against the main agent. No main
  context, status, transcript, goal or input is mutated.
- Closing/unmounting/pagehide closes the socket. Unexpected browser/network
  loss is bounded by the 35-second pong deadline, not an unload guarantee.
  `HEAD /side/:agent` diagnoses authentication without opening a side session
  or retrying a question. The browser never reconnects or replays side requests.
- `MessageExcerpts` projects bounded text in SQL, and `thread_history` traverses
  the entire public descendant tree with stable cursors and exact UTF-8 slices.
  Search covers visible content/tool names, **not opaque provider wire**, which
  would otherwise expose private reasoning through a match/no-match oracle.
  Public call arguments remain available through the exact `calls` field.
- `inspect_agent` defaults to ten bounded excerpts rather than a full transcript.
  Job observations omit repeated launch scripts only in the model projection;
  status, errors, output, cursors and the explicit `tool_output` route survive.
  Original records and ordinary execution/approval/no-replay contracts remain.
- Teamwork is one atomic resource per main conversation with 1–32 tasks and
  1–4 concurrently claimed worker identities (the main is separate). Main may
  assign existing public descendants; workers claim only for themselves.
  Mutations require the current board ID **and** revision. Claims reject stale
  updates, unfinished dependencies, cycles, unknown dependencies, conflicting
  literal write scopes and traversal including `a/../b`. `solve_verify` requires
  a dependent verifier for every work task and a different owner per dependency.
  Task completion requires result/evidence; finishing additionally requires all
  tasks done and main integration evidence. Normal goal audits still apply.
- Blocked claims are visible and may be released/reassigned by the main, including
  after a restart. There is no lease that silently reassigns active remote work.
  Declared paths are **coordination, not actual filesystem locks or permission
  grants**; undeclared writes are not prevented by this board. Cancelling a board
  does not stop agents/jobs. Existing stop and approved steering remain explicit.
- The native Teamwork modal subscribes only while open. It renders bounded domain
  data, never model HTML/JavaScript. This adopts A2UI's application-owned rendering
  principle, **not** the complete A2UI wire protocol or a new UI dependency.

### User workflow

`/side <short question>` opens a separate modal; `/side` opens an empty one.
The optional initial question never enters the main agent's input queue. Closing
and reopening starts empty. The main continues independently.

`/teamwork <task>` is an explicit ordinary durable instruction to the main agent,
preserved verbatim rather than rewritten into another language or passed through
a separate routing model. Main chooses the smallest useful `solve_verify`,
`parallel` or `hypotheses` arrangement, declares acceptance and ownership on the
board, and uses existing approved spawning/steering only when beneficial.
The **Teamwork** button inspects the shared board; opening it starts no agents.
Normal tasks retain the direct inspect/change/check/refine default. No subagents
were created for this implementation/research work.

### Reproducible measurements

Three one-second runs per fixture, linux/amd64, Go toolchain, one Go scheduling
thread, Intel Xeon Silver 4216. Command (from `backend`):

```sh
GOMAXPROCS=1 go test -p 1 ./agent ./datastore -run '^$' \
  -bench 'Benchmark(JobObservationProjection|MessageHistoryProjection)$' \
  -benchmem -benchtime=1s -count=3
```

| Fixture                                                                   | Before                 | After               | Interpretation                                                                                                                                                          |
| ------------------------------------------------------------------------- | ---------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Repeated job observation, 1,000-line launch script                        | 12,153 bytes           | 321 bytes           | 97.36% less model-visible text, with evidence and original-record reference retained. Projection itself costs 123–136 µs and about 54 KB of allocation per observation. |
| Same 50 rows of 120 messages, 8 KB public text + 24 KB provider wire each | 1,593,851 output bytes | 34,794 output bytes | 97.82% less materialized output; excerpts are intentionally not semantically complete replacements.                                                                     |
| Go allocation volume per history read                                     | about 4.95 MB          | about 162 KB        | 96.73% less allocation volume, **not** a peak-RSS measurement.                                                                                                          |
| Local history query runtime                                               | 8.86–9.10 ms           | 12.70–13.00 ms      | SQL projection is slower on this fixture; its benefit is bounded transfer/context and memory, not a claimed local-query speedup.                                        |

Raw evidence: `frontend/artifacts/recovery/harness-efficiency-final.log`.
These are deterministic synthetic workloads, not real-task token bills or
end-to-end answer-quality/speed benchmarks. Model tokenization varies. No claim
is made that teamwork always improves quality or reduces cost; independent
workers may cost more and coupled tasks should stay with the main.

### Regression evidence

The full Go suite, `go vet`, deterministic schema generation and targeted race
checks passed. Tests cover public/private/project/cycle scope, 123-message
pagination, UTF-8 exact reads, no private-wire search oracle, no main-thread
writes, cancellation/admission, real authenticated WebSocket disconnect/logout,
concurrent claim exclusivity, ownership, dependency/evidence gates, declared
path conflicts/traversal, worker budgets, compact views and stale replacement
board IDs. The old VM-path test fixture now uses the actual `subagent` role,
not the nonexistent `worker` role; no legacy role alias was introduced.

Backend logs: `frontend/artifacts/recovery/harness-go-final.log`,
`harness-vet.log`, `harness-race.log`. Browser checks exercise real DOM/WebSocket
lifecycles with deterministic provider-free fixtures; no paid live-model calls,
production restart or deployment is part of this verification.

The full browser run passed **222 tests** against a disposable real backend
with an in-memory database (`harness-browser-full.log`). After simplifying
`/teamwork` to preserve the original input and adding DE/EN translations, all
**10 focused side/teamwork/input-queue checks** passed again, including the two
new English cases (`harness-interactions-final.log`). Production frontend build,
TypeScript, formatting and deterministic TypeScript generation also passed;
see `harness-build-final.log` and `harness-format-final.log`.

These are local verification results for this change, not a claim about a
published commit's CI or a deployed production environment.
