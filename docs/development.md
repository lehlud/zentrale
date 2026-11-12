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
| `backend/datastore` | DuckDB resources, key ownership, and current schema     |
| `backend/gitforge`  | Git accounts and repository bindings                    |
| `backend/webapi`    | Authenticated API, events, and editor routes            |
| `backend/vscode`    | Pinned browser workbench distribution                   |
| `frontend/src`      | React interface and generated API types                 |
| `frontend/tests`    | Browser workflows and isolated UI fixtures              |

Configuration and agent state live in DuckDB; project files and commands belong
to SSH execution environments. The tool JavaScript runtime cannot start local
Zentrale processes or access its filesystem.

The unreleased application uses one transactional
[current schema](../backend/datastore/schema.go). Compatible databases reopen;
incompatible development schemas fail without being rewritten or deleted.
Historical migration and downgrade chains are intentionally absent.

## Context and usage

System instructions and core tools are stable. Runtime state is appended as
deduplicated snapshots. Normal replay preserves native messages, reasoning,
signatures, and phases; interrupted tool batches get explicit missing-result
messages before steering is applied.

Context budgets include instructions and tool definitions. Long tool results
have bounded model previews, with exact data recoverable through `tool_output`.
Compaction starts around 85% of the configured window. Supported OpenAI routes
use native `/responses/compact`; opaque checkpoints stay tied to their model
and account. Other routes use semantic summaries with protected user instructions
and complete recent tool groups. Summaries never become permission.

The approver gets one complete current context snapshot before its history.
Events reference their snapshot instead of rewriting previously sent messages.
Budgeting happens after projection, with current instructions and inspections
mandatory. Stored audit records remain complete.

Usage distinguishes reported counts from estimates and records cache reads and
writes. Cache writes are part of total input tokens; pricing accounts for reported
OpenAI write lifetimes. Unknown provider write costs remain marked incomplete.
The [token/cache audit](token-cache-audit.md) documents the measured baseline,
Codex comparison, and remaining gaps.

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
