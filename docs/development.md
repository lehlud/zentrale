# Development

[← Documentation](README.md)

## Local workflow

Install the [build prerequisites](getting-started.md#build-and-run) and frontend
dependencies. Build once, then start the backend:

```sh
make build
make dev
```

In another terminal:

```sh
cd frontend
npm run dev
```

Vite proxies GraphQL, events, and editor routes to the backend. `make generate`
regenerates the frontend schema and TypeScript types from Go. Edit the source
types rather than generated files.

## Find your way around

| Directory           | Responsibility                                          |
| ------------------- | ------------------------------------------------------- |
| `backend/agent`     | Conversations, goals, review, context, tools, and jobs  |
| `backend/remote`    | SSH, workspaces, pools, storage, and VMs                |
| `backend/providers` | Provider protocols, streaming, native replay, and usage |
| `backend/datastore` | SQLite resources, key ownership, and current schema     |
| `backend/gitforge`  | Git accounts and repository bindings                    |
| `backend/webapi`    | Authenticated API, events, and editor routes            |
| `backend/vscode`    | Pinned browser workbench distribution                   |
| `frontend/src`      | React interface and generated API types                 |
| `frontend/tests`    | Browser workflows and isolated UI fixtures              |

Configuration and agent state live in SQLite; project files and commands belong
to SSH execution environments. The tool JavaScript runtime cannot start local
Zentrale processes or access its filesystem.

The unreleased application uses one transactional
[current schema](../backend/datastore/schema.go). Compatible databases reopen;
incompatible development schemas fail without being rewritten or deleted.
Historical migration and downgrade chains are intentionally absent.
The storage-engine change has an explicit, read-only DuckDB import command;
see [database operation and concurrency](database-latency.md).

## Execution environment

Managed Fedora commands, VS Code and terminals use `/home/zentrale/project`
and the project's shared `/tmp/zentrale` scratch directory on that host. Runtime
context reports this execution view; persisted project/cleanup records describe
host-backed storage. `remote.PathsForExecution` selects paths from the actual
sandbox level. VM execution rejects `outsideSandbox`; explicitly selected
non-VM levels retain their documented host contract. Never rewrite arbitrary command strings to translate between views.

Internal file, Git, download, attachment and Python-environment helpers use the
process working directory and `TMPDIR`. Git inventories store project-relative
paths, and existing storage coordinates are validated against the configured
project root before use. Checkout data and Fedora disks are retained. Old host
path mount aliases, receipt-less clone compatibility and worker-owned scratch
fallbacks are not supported. Shared Fedora provisioning owns baseline packages;
starting VS Code must not install a separate environment.

## Shared application flows

- **Tools:** `backend/agent/tools.go` owns the supported-action policy catalog
  and the decode → authorize → route pipeline. Action-wide network requirements,
  command lifetimes and code-mode visibility come from that catalog, not parallel lists.
  New policies require approval by default. File snapshots, interactive input
  and MCP retain their explicit, context-specific review paths. Implementations
  stay in their owning domains; native/harness differences stay in wire decoding.
- **Resource mutations:** `resourceMutation` in `backend/webapi/schema.go` derives
  input/output types from the saved Go value and shares authentication, decoding,
  request-context propagation and serialization. Engine methods own validation;
  non-resource operations such as YAML pool import remain explicit.
- **Resource pages:** `frontend/src/resource-page.tsx` shares query/action state
  and initial/loading/error/empty/list rendering across management pages. An
  initial failure is not an empty list; a refresh failure must not hide existing
  data. Page actions use the shared error channel, while editor saves use the raw
  API so a rejected save keeps its dialog open. Pagination state stays with the
  page that owns its lifetime, including tab and responsive-layout changes.

Prefer these existing boundaries over a new helper per action or a universal
registry of page options. Domain-specific editors and authorization rules should
remain visible where their decisions are made.

## Code standards

- **Optimize for the next reader.** Use domain names, early returns and explicit
  control flow. Keep an action's related values together rather than duplicating
  nested conditionals. Comments explain invariants and trade-offs, not syntax.
- **Use whitespace to show semantic groups, in every language.** Keep only
  tightly related statements adjacent. Separate independent guard clauses and
  distinct setup, processing and result-building steps with a blank line.
  Single-line groups are valid; do not merge unrelated statements to avoid
  them. Choose boundaries by meaning, not a fixed line count. Cohesive data
  literals and call/error-check pairs belong together. Split long expressions
  across lines or use well-named intermediate values when that helps. Review
  grouping yourself: formatters do not decide semantic grouping.
- **Share boundaries, not incidental similarities.** Reuse typed request,
  resource and persistence flows where they own a real invariant. Keep domain
  validation visible; avoid one helper per action or a universal registry.
- **Make failure part of the contract.** Propagate storage and decode errors;
  distinguish a missing record from a broken read. Publish success events only
  after a successful save. Collection reads close their result sets and do not
  return partially decoded data alongside an error.
- **Separate authentication from connectivity.** GraphQL authentication errors
  carry `extensions.code: UNAUTHENTICATED`; protected HTTP requests return 401.
  Established event sockets close with application code 4401 when their session
  expires. The API service switches the app to its existing login screen and
  stops reconnecting, preserving the requested hash route for a later sign-in.
  Browsers hide rejected WebSocket handshake statuses, so failed handshakes get
  a bounded session-status check. This is not a resource-data polling fallback.
  Network, permission and ordinary application errors must not log users out.
- **Protect behavior while refactoring.** Test observable success and failure
  paths, including cancellation and recovery when relevant. Keep generated
  schema/types generated. Run the formatters, static checks and affected tests;
  do not replace small verified changes with a cosmetic rewrite.

## Recovery and unattended work

Background agents retry transient SSH, managed-VM startup and recognized VM
interruption failures during harness-owned, idempotent environment/repository
preparation. Immediate attempts are bounded; an active worker then backs off up
to one minute between preparation cycles and publishes `retrying_connection`.
Stopping the agent or shutting down cancels the wait. Unknown host keys,
authentication, permission and ordinary command errors remain explicit failures,
not reasons to guess from error text or bypass approval.

The native runner reports an interrupted invocation with exit 77 and a structured
marker. This is evidence that the supervisor or VM generation was lost, **not**
that the command never ran. Only trusted preparation consumes it for retry;
user commands, mutations and terminal input are never automatically replayed.
Recovery retains the VM disk and invalidates stale connection/readiness receipts.

New `question_ask` requests default to `mode: advisory`: the question stays open
while independent, already authorized work continues. Use `mode: blocking` only
when a required answer or authorization is missing and no safe independent work
remains. Legacy questions without a mode remain blocking. Only an agent's own
blocking questions gate its worker; a main agent can still answer a child's
question or do independent work. Required child answers also prevent premature
goal completion. Neither asking nor silence grants permission.

Answers atomically persist the question, conversation message and durable input
receipt before waking a worker. Retried answers do not duplicate context; an
explicit user stop or paused/blocked goal is not overridden. The HTTP answer path
does not wait for an SSH connection or VM boot.

## Resource references

New opaque resource IDs use 96 cryptographically random bits encoded as 17
URL-safe characters (including a letter prefix for VM scope compatibility),
shared by the Go domain generator and frontend helper.
Saved IDs are never rewritten or truncated: older long IDs remain valid. These
references are not credentials; session tokens, editor secrets, CSP nonces and
integrity/ownership fingerprints retain their separate generators and lengths.

Approval denials contain the concrete reason and `approvalId`, without repeating
tool-use warnings in every error. The separate clarification tool and independent
approval checks keep their existing behavior.

## Context and usage

System instructions and core tools are stable. Runtime state is appended as
deduplicated snapshots. Model windows start with a complete state and then
receive replacements of changed top-level fields, preserving earlier prefixes.
Rotation and compaction establish a new complete baseline; stored snapshots
remain complete. Normal replay preserves native messages, reasoning,
signatures, and phases; interrupted tool batches get explicit missing-result
messages before steering is applied.

Context budgets include instructions and tool definitions. Long tool results
have bounded model previews, with exact data recoverable through `tool_output`.
Compaction starts around 85% of the configured window. Supported OpenAI routes
use native `/responses/compact`; opaque checkpoints stay tied to their model
and account. Other routes use semantic summaries with protected user instructions
and complete recent tool groups. Summaries never become permission.
Responses estimates count content rather than JSON envelopes and use Codex's
coarse heuristics for encrypted reasoning/checkpoints. They remain estimates;
reported high context usage still triggers compaction.
Runtime state starts with a full baseline. Later updates replace changed fields
or, when smaller, update notes and job lists by ID. Item updates carry changed
records and the complete current ID order, removing stale entries without
resending unchanged contents. Ambiguous collections use full field replacements;
rotated windows begin with a complete baseline and stored snapshots stay intact.

The approver gets one complete current context snapshot before its history.
Events reference their snapshot instead of rewriting previously sent messages.
Budgeting happens after projection, with current instructions and inspections
mandatory. Optional older review groups are capped at 8,192 estimated tokens,
even on large-context models. Completed reviews fill fixed bounded sections,
preserving earlier prefixes between section rollovers instead of shifting a
sliding history tail on each new review. Stored audit records remain complete.
Reviewer history inspection uses the shared bounded message pages/exact slices,
after its own project access check. It does not return opaque provider replay.
Structural patch errors and invalid file-read bounds fail before model review;
the remote helper still owns filesystem and complete patch validation.

ChatGPT account Responses reuse the server's first routing token only within the
current turn. Human input revisions, independent action/goal reviews and summary
tasks establish separate routing lifetimes. Account, endpoint, model and thread
changes invalidate state; it is neither persisted nor shared with other protocols.
Thread/session headers identify Zentrale's own agent, keeping its client identity.

Code Mode allows two minutes of JavaScript execution, excluding host-tool waits,
and ten minutes for the complete batch. Parent cancellation and the batch deadline
propagate to inner tool calls. Individual approval/file/command deadlines still
apply; cancellation never replays actions already performed.
Failed batches return small per-call receipts, including known job IDs and
confirmed change counts. Zero tool calls are reported explicitly. A successful
tool return does not mean a background job has finished; failed or interrupted
calls may already have effects. Successful batches keep their selected output.
Batch results identify their persisted message; failure receipts also identify
the corresponding inner activity. `tool_output {messageId,activity,offset?,limit?}`
reads that existing activity without another model review or remote execution.
Both message and activity must belong to the caller. Output remains bounded;
`recordingTruncated` identifies a saved preview, not a recoverable full original.

Usage distinguishes reported counts from estimates and records cache reads and
writes. Cache writes are part of total input tokens; pricing accounts for reported
OpenAI write lifetimes. Unknown provider write costs remain marked incomplete.
The [token/cache audit](token-cache-audit.md) documents the measured baseline,
Codex comparison, and remaining gaps.
The [November 16 follow-up](codex-efficiency.md#nachprüfung-vom-16-november-2026)
records a real agent's consumption, bounded review history, runtime updates,
checkpoint sizing and Code Mode deadline corrections. `xhigh` remains the
main-agent default.

Inference diagnostics persist one `inference_trace` per logical model round,
including failed rounds, with separate purposes for work, approval, goal review,
summaries, native compaction, side questions and connection tests. Attempts record
HTTP/WebSocket status, duration, bytes and available reported usage; missing counts
remain unknown. Prepared-prefix comparisons store hashes in bounded memory, never
prompts, credentials or reasoning. `inferenceTraces(agentId,limit,offset)` returns
up to 100 recent records; the usage details load the latest 30 on demand.

OpenAI providers expose `responsesTransport` (`auto`, `http`, `websocket`) and
`promptCacheMode` (`auto`, `off`, `explicit`). Automatic WebSockets target official
OpenAI addresses; unsupported handshakes fall back to HTTP. Turn-local sockets
chain only when options and the entire prior input/output prefix match. Account,
token, model and thread changes reconnect; missing server state replays full
prepared input. Native states and `store:false` are preserved. Optional features
rejected by a route are remembered for 15 minutes per account/token/model.

Automatic additional cache boundaries apply to known modern public API models.
ChatGPT account routes can opt in explicitly. At most the first two eligible
user/developer messages are marked; markers stay fixed when messages are appended,
and implicit caching remains enabled for the tail. Only rejected cache parameters
fall back to the unmarked request, before any visible output was published.

Approvers can request `inspect batch` with 1–8 independent read-only actions and
two concurrent readers. Private context mutations and recursive batches are
excluded. Identical successful tool outputs may reference an earlier same-owner,
same-tool result inside the current window; errors and explicit recovery reads
stay visible. Local repeated argument/syntax failures get concrete repair advice
without weakening approval, replaying actions or changing reasoning defaults.

## Checks

```sh
make test         # Go, Python tool/runner tests, TypeScript
make check        # go vet and frontend formatting
make race         # Go race detector
make e2e          # Chromium browser workflows
make build        # Production frontend and Go binary
```

Local SSH tests need Python 3. Fish-specific tests run when available. Before the
first browser run, use `cd frontend && npx playwright install chromium`, or set
`CHROMIUM_PATH` to an installed browser. Tests use a separate in-memory server on
port 18080. Provider protocols and login flows use local fixtures.

`cd frontend && npm run test:ui` creates a local screenshot gallery at
`frontend/artifacts/ui/index.html` across languages, themes, and viewport sizes.
Generated test artifacts stay out of source control. Selected public demo images
live in `docs/images`.

## Dependency updates and continuous integration

`.github/dependabot.yml` checks Go modules, npm dependencies/lockfiles, Docker
base images and GitHub Actions every Monday at 06:17 UTC. Minor/patch npm updates
are grouped separately for runtime and development dependencies; major updates
remain separate PRs. Updates are proposed as pull requests, **not auto-merged**.

The `Tests` workflow runs for every PR (including Dependabot and forks), pushes
to `master`, manual dispatches and a weekly scheduled run. It needs no repository
secrets and has only read access to repository contents. Actions are pinned to
commit hashes and are themselves maintained by Dependabot.

CI and the frontend Docker build use Node 26, with matching Node type definitions.
The frontend uses TypeScript 7 and Vite 8; `@vitejs/plugin-react` 6 requires Vite 8,
so update those together rather than bypassing npm's peer-dependency checks.

Three independent checks cover:

- **Backend and Python:** module integrity, Go formatting, `go vet`, all Go tests
  with the race detector, all three Python test directories, and backend builds.
- **Frontend and browser integration:** `npm ci`, formatting, TypeScript,
  generated-type consistency, the production build and the complete Playwright
  suite against an isolated in-memory backend with local SSH/provider fixtures.
  HTML reports, failure screenshots and UI artifacts are retained for seven days.
- **Container build and smoke test:** build the actual Dockerfile without pushing
  an image, start its non-root runtime and check the HTTP UI and GraphQL setup
  status. This also checks updates to the Docker-only Go/Node/runtime images.

For CI, `ZENTRALE_E2E_SERVER_COMMAND` lets Playwright supervise an already-built
backend instead of rebuilding it during server startup. Local `make e2e` keeps
its existing build-and-run behavior. CI rejects accidentally committed
`test.only` calls. Tests requiring real model accounts, external machines, KVM or
GPU passthrough remain explicit live checks, not prerequisites for Dependabot PRs.

To prevent merging failing updates, configure these three job names as required
status checks in the repository's branch protection/ruleset. The workflow itself
does not change repository settings or grant Dependabot merge permissions.

## Live checks and model evaluations

The preview browser check uses isolated SSH, HTTP services and a local TLS
terminating proxy, with no external machines or accounts:

```sh
cd backend
ZENTRALE_PREVIEW_BROWSER=1 go test ./webapi -run '^TestPreview' -count=1
```

It exercises root imports, localhost API requests, POST bodies, XHR, SSE,
WebSockets, cookie isolation, navigation and service-worker scope in Chromium.

Live tests need the actual infrastructure. Use disposable database snapshots and
test directories, and read each test's setup before running it:

- [Managed VM and GPUs](../backend/remote/microvm_live_test.go):
  `ZENTRALE_MICROVM_TEST_SNAPSHOT` and `ZENTRALE_MICROVM_TEST_MACHINE`.
  Optional storage and quota variables exercise extra mounts.
- [Browser editor over SSH](../backend/webapi/editor_live_test.go): matching
  extracted releases via `ZENTRALE_VSCODE_SMOKE_WEB` and
  `ZENTRALE_VSCODE_SMOKE_SERVER`.
- [Browser editor inside a VM](../backend/webapi/editor_microvm_live_test.go):
  a separate `ZENTRALE_VSCODE_MICROVM_TEST_SNAPSHOT`, a test machine, and the
  matching browser release.

Pinned artifacts are described by the runtime and editor `release.json` files.
GPU passthrough additionally requires [host preparation](sandbox-and-editor.md#gpu-passthrough);
CPU VM tests do not demonstrate working physical GPU passthrough.

The [model evaluation runner](../backend/cmd/harness-eval) compares configured
models through the same harness. Its
[example plan](../backend/harnesseval/example-plan.json) specifies models,
machines, repeats, limits, and verification commands. These runs use real accounts
and remote resources and may incur costs. Repeated comparable tasks are needed;
a single fixture run does not establish quality or cache improvements.
