# Database operation and concurrency

Zentrale uses one embedded SQLite database, one `datastore.Instance` and one
ordinary Go `database/sql` pool (16 open connections, up to 8 idle). Reads,
authentication and writes all use this pool. There are no separate reader/writer
pools or application mutexes around database queries.

## Why SQLite

The deployment is a single application server with local persistent storage.
SQLite fits that arrangement without a second service to configure or maintain.
[SQLite's recommended uses](https://www.sqlite.org/whentouse.html) include local
application-server storage. [WAL mode](https://www.sqlite.org/wal.html) lets
readers retain committed snapshots while a writer commits. SQLite coordinates
one writer at a time; multiple readers run concurrently. This does not mean
unlimited simultaneous writes or multiple independent Zentrale job supervisors.

The previous DuckDB setup needed a serialized writer to avoid optimistic update
conflicts. Its [concurrency model](https://duckdb.org/docs/stable/connect/concurrency)
is a better fit for analytical workloads than the application's many short state
updates. Merely adding connections would not remove those conflicts or the
application's global locks.

Every connection enables WAL, foreign-key checks, a 5-second busy timeout and
FULL synchronous writes. Explicit write transactions begin immediately, avoiding
read-then-write lock upgrade races. Transactions contain only local SQL work;
SSH, OAuth, model discovery and repository HTTP calls run outside them. Indexes
cover resource identity, parent pages, agent/project ownership and event cursors.
JSON projections preserve booleans, nested data and null values. Insertion and
transcript ordering preserve sub-millisecond timestamps.

## Application concurrency

- Read-only configuration/status snapshots do not acquire the lifecycle mutex.
- Provider token exchanges serialize per provider; catalog discovery serializes
  per provider. Different accounts can proceed independently. Waits for network
  operations' gates honor the request context.
- Git account transitions serialize per connection; public snapshots remain
  independent of in-flight HTTP work. A project reservation protects repository
  changes while the global configuration mutex is released. Other projects
  remain editable.
- Agent connection teardown releases the global mutex while retaining its
  running pin, so new input and unrelated operations remain responsive.
- Each WebSocket can run two independent snapshots. Updates to the same
  subscription coalesce. One process-wide fixed worker pool bounds actual
  executions even if a resolver ignores cancellation. Unsubscribe cancels its
  resolver; stale generations never push data. A query timeout pauses that
  subscription and sends a correlated error without closing the other queries.
  Unsubscribe and subscribe again to retry it.
- Page actions track pending state per resource. A slow action on one row does
  not disable actions on another row; duplicate actions on one row are guarded.

A GraphQL snapshot still uses bounded collection queries and batches for nested
activities and diffs. `ReadQueryCount` is a process-wide diagnostic counter,
not a per-request measurement when requests overlap. Live updates remain server
pushes, without periodic client polling.

## Deletion and remote cleanup

Conversation/project deletion and the VM cleanup outbox commit in the same
transaction. The API reports success after the local deletion; an absent or
unreachable VM cannot prevent it. Runtime handles close asynchronously, outside
the global mutex, with per-agent barriers preventing a new lease from racing old
teardown. A single outbox worker removes VMs with a bounded timeout. Failed work
remains in `resources` under `kind='vm_cleanup'`, with `error` and `retryAt`, and
survives restart. It retries transient failures after a minute. Missing or changed
remote configuration defers cleanup for an hour and logs a warning for review;
it never silently redirects old cleanup to a newly configured machine.

Removing a machine or pool cascades through dependent jump connections and pool
members. Empty dependent pools are removed. Projects lose the corresponding
assignments; conversations, histories, files and SSH keys are retained. Affected
editors close. Running affected agents/jobs or editor startup must finish before
removal; the operation rejects them before changing records. If a project loses
its last target, assign another machine or pool before continuing its work.

## Existing databases and backups

Use the [explicit import](getting-started.md#import-an-existing-duckdb) once.
DuckDB is linked only into the tagged importer, not the server executable. The
importer reads a consistent read-only DuckDB snapshot and publishes a complete
new SQLite database without overwriting existing files. Credentials retain their
original random encryption key; missing keys fail before publication. It also
preserves sequence high-water marks, including IDs whose records were deleted.

Keep the database on a local filesystem. SQLite WAL does not support clients on
different machines sharing a network filesystem. Keep one Zentrale server per
volume because its live workers and event delivery have one process owner.

For a simple backup, stop Zentrale cleanly and copy `zentrale.sqlite` together
with `zentrale.sqlite.credentials.key`. While SQLite is open, the WAL may contain
committed data not yet checkpointed into the main file; copying only that file
is not an online backup. Use SQLite's [backup API](https://www.sqlite.org/backup.html)
for online backups, or stop the server. Never delete a live WAL manually.

## Verification

Regression tests retain four simultaneous reader snapshots while a writer
commits, and run twelve competing writers without losing increments. Existing
multi-process database tests also open the same file without an exclusive
DuckDB process lock. Separate tests block catalog/Git HTTP calls and remote
teardown to check that independent operations still finish. Real local
WebSocket tests verify parallel snapshots, unsubscribe cancellation and request
ID reuse. The tagged import test verifies encrypted credentials, binary blobs,
JSON types, timestamps and ID counters, and rejects missing keys/overwrites.

The CI bootstrap/projection/lookup benchmarks remain available. Older DuckDB
measurements describe the previous engine and are not SQLite or production
latency claims.
