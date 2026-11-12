# Shared family auto-approver and instruction provenance

Each main conversation and its subagents use one independent auto-approver.
The approver remains a read-only reviewer; sharing it does **not** share blanket
permissions or allow a working agent to grant permission to another agent.

## Authority is not a chat role or a claim in message text

Delivered parent tasks previously used the same `user` message role as human
input. Project-wide approval instruction lists also included child inputs. This
could confuse a request to wait in one child with a pause of the main work, and
could present agent-authored claims as if they were human authorization.

The backend now:

- Derives the main-family root from persisted ownership and checks project
  membership. Cycles, missing parents and invalid reviewer ownership fail closed.
- Separates original root-user instructions from recipient-scoped agent
  coordination. Sibling tasks and unrelated conversations are not authority for
  the requesting agent.
- Records the origin, author and recipient of new instruction resources outside
  the supplied text. Public human prompting remains restricted to main agents.
- Delivers new parent tasks as `event` messages containing a backend-generated
  JSON envelope. Supplied text is a JSON string, so it cannot replace the outer
  origin, recipient or author fields. Conversation compaction retains the first
  and latest coordination anchors without turning them into human instructions.
- Preserves ambiguous legacy child input and possible historical human
  restrictions at that child's scope; unknown provenance cannot create new
  authorization or globally pause the main agent.

Some provider protocols have no event role and transport these envelopes using
their ordinary input role. That wire role is **not** proof of human authorship.
Approval authority comes from the backend's scoped original-instruction snapshot,
not provider role labels, quoted claims, summaries, goals or agent messages.

An actual human pause remains binding at its stated scope. A parent asking one
child to wait does not pause the main agent, siblings or unrelated conversations.

## Shared review lifecycle

New children reference their root's approver and cannot select a private reviewer
or different approval model. Runtime review routing resolves the persisted root,
not an approver ID offered by a child. The root's configured approval model and
thinking level are used for the family.

Before restart recovery resumes work, legacy children are rebound atomically to
the existing root approver. Their old private approvers are archived. Historical
review messages are preserved for inspection, not replayed, merged into current
authority or deleted.

Action reviews, goal reviews and clarification evidence use a cancellable
per-approver gate. One review owns the shared context through inspection,
decision recording and status changes. A canceled queued review appends nothing
and cannot execute. Different main families can still review concurrently.

Deleting a child follows descendant ownership, so it does not remove the root's
shared approver. Deleting the entire main family removes its owned reviewers.

## Clarification is not an override

`approval_clarify {approvalId, explanation}` lets a main agent record a concrete
misunderstanding about its own denied action. The original denial is unchanged.
The tool executes nothing and returns `permissionGranted: false`.

The unchanged action must be retried through its normal tool and receive a fresh
independent review of its full network, machine, root and sandbox scope. Genuine
missing human authorization still requires the user. Clarifications are
idempotent per denial and persistently limited to two for the identical action,
including across restarts and approver-context compaction.

## Verification

Regression tests cover child message impersonation, scoped waits versus real
human pauses, sibling/conversation isolation, legacy evidence preservation,
shared reviewer routing, migration idempotency, descendant deletion boundaries,
review-gate cancellation and clarification limits. Runtime and race-detector
results must come from the normal full CI; static analysis alone is not a test
pass or evidence that an LLM can never misinterpret text.
