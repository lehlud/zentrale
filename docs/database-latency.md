# Conversation query latency

Zentrale currently uses embedded DuckDB. A GraphQL request is not one large SQL
join: collection roots issue bounded batch queries; transcript activities and
file changes are loaded in two additional batches, not per message. Authentication
also queries the session table. The small `projects { id name }` and
`agents { ... }` queries each perform two read statements including authentication.
`TestConversationQueriesDoNotGrowSQLCountWithHistory` checks those counts and that
the complete bootstrap count does not increase with history size. Its diagnostic
counter is process-wide; concurrent request deltas must not be interpreted as
per-request counts.

## Connection contention

Previously all operations used a single SQL connection. A small list could wait
behind a large history scan or an agent transaction before its SQL even started.
There is now a four-connection reader pool sharing the exact native database
connector, including for in-memory stores. The separate writer pool remains at
one connection: simply increasing its limit would introduce concurrent DuckDB
write conflicts. Readers use committed MVCC snapshots; they do not see partially
committed mutations. The reader pool closes before the owning writer pool.

The regression test retains the sole writer connection and verifies that session,
project list/load and transcript reads still finish. Exhausting the reader pool
must conversely not reserve or block the writer connection.

## JSON projection and indexes

Selecting only GraphQL fields does not make a query cheap when each field invokes
`json_extract(data, path)` separately. Large hidden agent summaries were repeatedly
parsed. Projection now uses one list-of-paths extraction and reconstructs the
JSON object, retaining nested values, missing/null fields and parameter binding.

The CI comparison fixture (300 agents with large hidden summaries) measured
166.8 ms per legacy projection versus 28.2 ms for single-parse projection. A full
bootstrap fixture with 300 agents, 3,000 messages and 1,000 jobs measured 173.9 ms;
the two supplied small-list selections measured 3.4 ms (projects) and 19.0 ms
(agents). These are controlled CI measurements, not production before/after
measurements or guaranteed response times under load.

`BenchmarkResourceLookupIndexes` compares existing compound indexes with
additional single-column ID and parent indexes over 100,000 records and prints
the actual `EXPLAIN` plans. In the initial comparison, the ID lookup changed
1.60→1.08 ms and the parent page 2.68→2.63 ms. Extra permanent indexes were not
added on this evidence: they do not fix seconds of connection queueing or repeated
JSON parsing, and they add storage, startup and mutation costs. DuckDB ART indexes
should not be treated as interchangeable with PostgreSQL/MySQL B-tree indexes.

The bounded benchmark steps in `.github/workflows/tests.yml` cover the complete
production bootstrap selection, including wire, attachments, activities and diffs,
as well as the supplied projects and agents queries. Response shrinking or
omitting requested details is not used to obtain the improvements. Live updates
remain server-pushed WebSocket subscriptions, without HTTP or WebSocket polling.
