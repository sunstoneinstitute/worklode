---
status: draft
issued: 2026-08-03
requires:
  - 004-execution-backbone.md
  - 006-knowledge-graph.md
  - 012-agent-sessions.md
  - 022-prometheus-metrics.md
  - 025-documents-in-the-backbone.md
---
# Spec 027 — Event watchers

## 0. Why {#sec-0}

Two steps in the document lifecycle are reliably forgotten because nothing asks for them. A
spec is written and sits unreviewed because no one minted the review work. A spec is accepted
and sits unplanned because deciding to decompose it is a separate act that no row prompts.
Both are cheap to mint and expensive to miss, and both are deterministic functions of a state
change that the backbone already witnesses.

The backbone has an append-only `events` table (004 §1.5) that everything is derived from, and
nothing reads it. It is written in the same transaction as the change it records, and then it
is only ever a provenance trail. This spec turns it into a log with **subscribers**: ordered,
offset-tracked, at-least-once, so a state change can have consequences beyond the transaction
that caused it, and so the second consumer costs a row rather than an edit to the producer.

The first subscriber is `doc-lifecycle`, with two hardcoded rules (§5). The rules being
hardcoded is deliberate and temporary: the log, the ordering guarantee, the offsets and the
event vocabulary are the durable part, and they are what a rule stored in the backbone or in
the graph would need underneath it either way.

**On 025 §3.** Acceptance and spec→plan decomposition are deliberate human acts, and this spec
does not automate either. It mints a task *asking for* the work; deciding whether spec N
becomes one plan or four, and writing them, remains entirely the assignee's. A prompt is not
the act, and 025 §3 is amended with a sentence saying so. 025 is still `draft`, so that
sentence and the `lode doc submit` verb of §6 are folded into it directly rather than recorded
as amendment edges — the amendment machinery exists for published documents.

## 1. The log is totally ordered {#sec-1}

`events.id` is assigned at `INSERT` and becomes visible at `COMMIT`, and those are not the
same instant. A reader tracking a `last_seen_id` will see id 105 from a fast transaction,
advance past it, and never see id 104 from a slow one that started earlier and committed
later. The event is not lost — it is in the table, behind the cursor, forever unread. Nothing
detects this; the subscriber simply misses a document acceptance now and then.

The fix is to read only what is below the **commit horizon**:

```sql
ALTER TABLE events ADD COLUMN txid xid8 NOT NULL DEFAULT pg_current_xact_id();
CREATE INDEX events_txid_id ON events (txid, id);
```

```sql
SELECT id, source, type, payload, received_at
  FROM events
 WHERE id > $1                                          -- the subscriber's offset
   AND txid < pg_snapshot_xmin(pg_current_snapshot())   -- the horizon
 ORDER BY id
 LIMIT $2;
```

`pg_snapshot_xmin` is the oldest transaction still in flight anywhere in the database. A row
below it was written by a transaction that has finished — and since an aborted one leaves no
visible row, a visible row below the horizon is committed for good. So no transaction can ever
again produce an id lower than one the subscriber has read. **The visible log grows only at
its tail, which is what makes an offset meaningful.**

Three consequences, stated rather than discovered later:

- **Aborted transactions leave holes** in the id sequence. Offsets are monotonic positions,
  not counts, and nothing depends on contiguity.
- **Writers are not serialized.** The alternative — assigning the log position under a lock
  held to commit — gives gapless ids at the cost of funnelling every webhook, claim and task
  edit through one lock. The horizon costs one column and one predicate.
- **Any long transaction anywhere holds the horizon back**, not just a slow event writer. An
  idle-in-transaction session or a long analytical query stalls every subscriber for its
  duration. That is real, it is the price of not serializing writes, and §8's lag metric is
  how it becomes visible instead of mysterious.

`xid8` rather than `xid`: 64-bit, so the comparison needs no wraparound handling. The migration
adds the column with a volatile default, so every pre-existing row takes the migration's own
transaction id and drops below the horizon the moment it commits.

## 2. Subscribers hold offsets {#sec-2}

```sql
CREATE TABLE event_subscribers (
    name              text PRIMARY KEY,
    last_read_offset  bigint NOT NULL DEFAULT 0,
    last_acked_offset bigint NOT NULL DEFAULT 0,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT event_subscribers_acked_le_read CHECK (last_acked_offset <= last_read_offset)
);
```

The model is Kafka's, with the durable parts of a consumer group in one row:

- **Read** takes the batch after `last_read_offset` below the horizon, in id order, and
  advances `last_read_offset` to the last id in the batch.
- **Ack** advances `last_acked_offset` to the newest offset the subscriber has *completely*
  processed, and only forward (`WHERE $1 > last_acked_offset`), so a late or replayed ack
  cannot rewind the stream.
- **Restart** resumes at `last_acked_offset`, not `last_read_offset`. Everything read but
  unacked is redelivered.

That is at-least-once with in-order delivery, and it makes handler idempotency a requirement
rather than a nicety (§5). Events are never deleted or compacted — the log is provenance
(004 §1.5) and outlives every subscriber's interest in it.

**One active consumer per subscriber.** In-order delivery means exactly one process may hold
the stream, the same constraint a Kafka partition has. The consuming loop takes a dedicated
pool connection and `pg_try_advisory_lock(hashtext('wl:subscriber:' || name))` on it for its
lifetime; a second `lode serve` replica fails the lock, idles, and retries on an interval. A
crashed process drops its connection, Postgres releases the lock, and the standby takes over
on its next attempt — failover with no lease table and no heartbeat.

**Releasing is not `Close()`.** A session-scoped advisory lock outlives `(*sql.Conn).Close()`,
which returns the session to the pool still holding it — the lock would then leak into an
unrelated query's connection and no replica could ever take over. Release therefore
`pg_advisory_unlock`s explicitly and then *destroys* the session rather than returning it, so
a failed unlock dies with the connection instead of poisoning the pool. Any error on the lock
path is treated as "lock not held", never as "probably still held".

The loop polls (default 1s). `LISTEN`/`NOTIFY` would cut the latency and is deliberately not
used: a poll is correct when a notification is missed, and nothing here is latency-sensitive
enough to justify a second delivery path.

A subscriber row is created by the code that owns the subscriber, at startup, with
`ON CONFLICT DO NOTHING` — starting at offset 0 means a new subscriber replays the whole log,
which is the right default for a rule set that should have been running all along, and §6's
offset verb is how an operator chooses otherwise.

## 3. Events are RDF-shaped {#sec-3}

Domain events carry a curie in `events.type` and JSON-LD in `events.payload`:

```json
{ "@context": "https://worklode.io/ns/ontology#",
  "@type":    "wl:DocumentAccepted",
  "@id":      "wlid:event/4711",
  "prov:atTime":            "2026-08-03T09:12:00Z",
  "prov:wasAssociatedWith": "wlid:actor/stig",
  "wl:subject":             "wlid:doc/spec-025",
  "wl:fromStatus":          "wlc:draft",
  "wl:toStatus":            "wlc:accepted" }
```

`ns/ontology.ttl` gains `wl:Event rdfs:subClassOf prov:Activity`, one subclass per event type,
`wl:subject` (the thing the event is about) and the per-type properties. Go constants and the
emit-time validation table are **generated** from `ns/` by 025 §9's codegen step, so the
vocabulary and the code cannot drift.

**Subclasses, not a SKOS kind scheme.** `wl:RuntimeEvent` takes its kind from
`wlc:RuntimeEventKind` because crashloops and OOM kills are structurally identical rows that
differ only in a label. Backbone event types differ in *shape* — `wl:DocumentAccepted` carries
a status transition, `wl:DocumentSubmitted` carries none — and differing shapes under a shared
supertype is what a class hierarchy is for. It is also what lets a SHACL shape constrain one
event type without constraining all of them, and what subscribers match on.

**One log, two populations.** Webhook deliveries keep their vendor payloads and dotted types
(`push`, `issues.opened`); they are ingest facts, not domain facts, and they are not RDF. No
`CHECK` is added to `events.type` — it must accept both — and the generated set is enforced in
Go at emit time instead. Keeping them in one table keeps one offset space and preserves the
provenance chain from a webhook to what it caused, which two logs and a projection between
them would break for no gain.

## 4. Emitting {#sec-4}

A domain event is emitted **in the transaction that makes the change it describes**, through
`RecordEvent`'s existing `apply` seam. An event that could commit without its change, or a
change that could commit without its event, is a log that lies; there is no second path.

`external_id` is deterministic for domain events — `<type>:<subject>:<version>` — where CLI
events today use `randomExternalID()`. The `(source, external_id)` unique constraint then makes
a retried request idempotent at the log, so a client that resends after a timeout gets the
existing event rather than a duplicate acceptance.

Emission is a thin typed helper per event type rather than a generic one: the payload is
generated-code-checked against `ns/`, so a missing property fails at compile time.

The payload's `@id` is `wlid:event/<id>`, which the row does not know at `INSERT` under an
identity column. The id is therefore **reserved before the insert** — `nextval` on the
identity's sequence, inserted with `OVERRIDING SYSTEM VALUE` — so the JSON-LD node names the
row that carries it. Without that the event's own IRI would have to be patched in afterwards,
which is a second write to an append-only table.

## 5. The `doc-lifecycle` subscriber {#sec-5}

Two rules, hardcoded in Go behind `Evaluate(event) → []Action`:

| Event | Action | Guard |
|---|---|---|
| `wl:DocumentSubmitted` | mint `kind = 'review'`, state `ready`, referencing the document | no open review task references it |
| `wl:DocumentAccepted` where the document is a spec | mint `kind = 'design'`, state `ready` — *decide how to decompose this spec into plans, and write them* | no open design task references it |

"Referencing the document" needs a column, and `plan_doc` (025 §5) is not it — that one says
*this task was minted by that plan*, whereas a review or design task is *about* a document it
has not executed. So tasks gain a nullable **`about_doc`**, and both guards are queries over
open tasks carrying it — a query, not stored state (025 §1). Minted tasks take the document's
project and carry `prov:wasInformedBy` back to the event that caused them, so "why does this
task exist" is answerable from the task.

**Idempotency** has two layers, because it defends against two different things. Redelivery of
one event is a no-op through the log's own key: an action is itself recorded as an event with
`external_id = <subscriber>:<rule>:<event-id>`, so `(source, external_id)` refuses the second
attempt and no side-effect table is needed to remember what was done. A *legitimate*
repeat — 014 §5's revision flow accepting the same document a second time — is handled by the
guard: while the planning task is still open the acceptance is absorbed and noted on it;
once it has closed, a further acceptance mints a fresh task, which is correct, because
sections accepted since the last plan need planning.

**Submission is an event, not a status.** `lode doc submit` emits `wl:DocumentSubmitted` and
changes no column. The document lifecycle stays `draft → accepted → superseded` (025 §3) and
the open review task *is* "under review" — exactly 025 §3's definition, which minting the
review task at `doc new` would have collapsed by making every draft permanently under review.

**No cascades.** A watcher action must not emit an event its own subscriber consumes. With two
reviewed rules that is a rule rather than a mechanism; making rules configurable (§12) is what
would require a cycle check, and it is out of scope here precisely because the check is not
yet worth building.

## 6. Surfaces {#sec-6}

```
lode doc submit <id>                   emit wl:DocumentSubmitted; mints the review task (§5)
lode event tail [--type] [--since]     read the log, newest last
lode event subscribers                 name, read/acked offset, lag, holder
lode event seek <name> --to <offset>   admin: move a subscriber's offset (replay or skip)
```

`lode doc submit` joins 025 §10's document surface. `lode event seek` is admin-gated and the
only way an offset moves backwards; it is how a rule fixed after the fact gets applied to
events it already skipped, and it is safe precisely because handlers are idempotent.

## 7. Planning cost lands on the planning task {#sec-7}

No schema change. `agent_sessions` hangs off `leases`, a lease binds a task to a worktree, and
`hookrun` bills each turn to the worktree it ran in (012 §4) — so the chain from tokens to task
already exists. What is missing is that planning is done in the main checkout, which holds no
lease, and those tokens are dropped.

So a planning session claims its task like any other work: the lode plugin's planning skill
runs `lode task claim` on the minted `design` task into `wt/<task-id>-<slug>` before it writes
anything, and `lode task cost` then answers for planning exactly as it does for a feature. This
also gives planning the brief, the secrets and the hook wiring every other kind of work gets,
which is the stronger reason.

Tokens spent before the claim — the exploration that decided which task to pick up — stay
unattributed. Attributing them would mean billing a session to a task it had not yet chosen.

## 8. Metrics {#sec-8}

Per 022's conventions, each in its owning package — the first three in `internal/eventbus`,
the last in `internal/watcher`:

| Metric | Type | Labels |
|---|---|---|
| `worklode_event_subscriber_lag` | gauge | `subscriber` — horizon offset minus `last_acked_offset` |
| `worklode_events_processed_total` | counter | `subscriber`, `type`, `outcome` ∈ `applied\|suppressed\|error` |
| `worklode_event_batch_duration_seconds` | histogram | `subscriber` |
| `worklode_watcher_actions_total` | counter | `rule`, `outcome` ∈ `applied\|suppressed\|error` |

`type` is bounded by the generated event-type set; an unknown type is counted as `other`. The
lag gauge is the one that matters operationally: it rises both when a subscriber is stuck and
when a long transaction holds the horizon back (§1), and those are distinguishable because the
first affects one subscriber and the second affects all of them.

## 9. Testing {#sec-9}

- **The ordering trap, directly.** Open transaction A, insert an event, leave it uncommitted;
  in transaction B insert and commit a later event; read as a subscriber and assert **neither**
  is delivered; commit A; read again and assert both arrive, A's first. A `last_seen_id` cursor
  fails this test, which is the point of writing it.
- Aborted transactions leave a hole and the subscriber advances past it without stalling.
- Ack is monotonic: a replayed lower ack does not rewind `last_acked_offset`.
- Restart after read-without-ack redelivers exactly the unacked window, in order.
- Two loops on one subscriber name: the second acquires no lock and consumes nothing; killing
  the first lets the second take over.
- Redelivering one `wl:DocumentAccepted` mints one task, not two.
- The suppression guard across the full cycle: accept → mint; accept again while open →
  suppressed and noted; close; accept again → fresh task.
- `wl:DocumentSubmitted` on a document that already has an open review task is suppressed.
- Emit-time validation rejects a payload whose properties are not in the generated set.
- A vendor webhook event passes through the subscriber untouched and is not treated as RDF.
- e2e: submit and accept a document through the HTTP API only, and assert the tasks appear —
  no direct store writes, per the `e2e/` contract.

## 10. Implementation {#sec-10}

| Unit | Holds |
|---|---|
| `internal/store/events.go` | the horizon read, offset read/ack, `pg_try_advisory_lock` acquisition |
| `internal/eventbus/loop.go` | the polling loop, batch/ack cycle, lock lifecycle, metrics |
| `internal/eventbus/emit.go` | typed emit helpers, deterministic `external_id`, payload validation |
| `internal/watcher/doclifecycle.go` | `Evaluate(event) → []Action` and the two rules of §5 |
| `internal/cmd/event.go` | `lode event tail\|subscribers\|seek` |
| `ns/ontology.ttl` | `wl:Event`, its subclasses, `wl:subject` and the per-type properties |
| `deploy/base/migrations` | `events.txid`, `event_subscribers`, `tasks.about_doc` (§5) |

`internal/watcher` takes an event and returns actions, with no store handle and no HTTP, so the
rules are testable as a pure function and the loop is testable without rules.

**Two plans, not one.** §1–§4, §6's `lode event` verbs, §8 and their tests depend on nothing
and can ship immediately against the existing log. §5 needs 025's `docs` rows to exist —
until then there is no `wl:DocumentAccepted` to emit — so the doc-lifecycle subscriber is a
second plan, `blockedBy` the first and by 025's own. §7 rides with the second, since it is one
line in a skill.

## 11. Changes to other specs {#sec-11}

| Spec | Change |
|---|---|
| 025 | §3 gains a sentence: minting a task that asks for review or planning is not the act it asks for, so it does not breach "acceptance and decomposition are deliberate human acts". §10 gains `lode doc submit`. Both are direct edits — 025 is `draft` (§0). |
| 004 | §1.5's `events` table gains `txid`; the log gains subscribers, and stops being write-only |
| `ns/` | `wl:Event` and subclasses, per §3; generated Go constants per 025 §9 |
| lode plugin | the planning skill claims its task before writing (§7) |

## 12. Out of scope {#sec-12}

- **Rules stored in the backbone or the graph.** The destination, and the reason the log,
  ordering, offsets and vocabulary are built as they are here. Storing a rule needs a
  predicate language, an action vocabulary and cycle detection — none of which is answerable
  before two hardcoded rules have run for a while.
- **Subscribers beyond `doc-lifecycle`.** Adding one is a row and a handler; none is needed
  yet.
- **Graph projection of events.** The projector's contract (006 §6), unchanged by this spec.
- **Retention and compaction.** The log is append-only and kept; when it stops being free,
  that is its own decision.
- **Ordering partitions.** One totally ordered stream, one active consumer per subscriber. A
  throughput problem would be solved by partitioning on subject, and there is no throughput
  problem.
- **`wl:DocumentAccepted` on plans.** 025 §5's accept transaction already mints a plan's tasks
  directly; a watcher duplicating that would give the invariant two owners.

## 13. Acceptance criteria {#sec-13}

1. A subscriber reading concurrently with an out-of-order commit pair delivers both events in
   id order and skips neither; the equivalent `last_seen_id` implementation fails the same
   test.
2. `last_acked_offset ≤ last_read_offset` holds always; restart resumes from
   `last_acked_offset`; a lower ack never rewinds.
3. Exactly one process consumes a subscriber at a time, and killing it hands over without an
   operator step.
4. `lode doc submit` mints one `ready` review task and changes no document column; a second
   submit while that task is open mints nothing.
5. Accepting a spec mints one `ready` design task in the document's project, carrying
   `prov:wasInformedBy` to the event; re-accepting while it is open mints nothing; re-accepting
   after it closes mints one more.
6. Every domain event's `type` and payload properties come from the generated `ns/` set, and CI
   fails on drift between `ns/` and the generated code.
7. Vendor webhook events remain in the same log with their dotted types, are readable by
   `lode event tail`, and no `CHECK` on `events.type` rejects either population.
8. `worklode_event_subscriber_lag` rises when a subscriber is stopped and returns to zero when
   it resumes; the four metrics of §8 are registered and tested.
9. A planning session that claims its design task has its tokens reported by
   `lode task cost <design-task>`.
