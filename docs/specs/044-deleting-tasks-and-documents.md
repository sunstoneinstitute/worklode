---
status: draft
requires:
- 004-execution-backbone.md
- 025-documents-in-the-backbone.md
- 039-worklode-prod-in-the-admin-cluster.md
---
# Spec 044 — Deleting tasks and documents

## 0. Purpose & scope {#sec-0}

`abandon` is how work stops in worklode, and it stays the preferred close: it
records that a decision was taken, by whom, leaving the task in the corpus as
`abandoned`. That is the right answer for real work.

It is the wrong answer for work that never existed. A dev instance re-seeds and
discards tasks all day; a mistyped `lode task new` produces a row nobody wants a
decision record about; an imported batch lands twice. Demanding a justification
for abandoning each of those trains people to write "test" in the field that is
supposed to hold a reason.

This spec adds **delete** as a second, narrower close for tasks and design
documents — a tombstone, not a state — and makes the justification requirement
depend on the instance environment 039 §3 defines. It does not change the task
state machine (004 §5), the document lifecycle (025 §15), or `abandon`.

## 1. Delete is not abandon {#sec-1}

The two answer different questions, and neither substitutes for the other.

| | `abandon` | `delete` |
|---|---|---|
| Says | this work was considered and dropped | this row should not have existed |
| Is | a task state (004 §5) | a tombstone, orthogonal to state |
| Applies to | tasks | tasks and design documents |
| Leaves behind | a visible `abandoned` row | a hidden row and its events |
| Reversible | reopen (`abandoned → ready`) | undelete |

Delete is **not** a new value in the task state enum. A tombstoned task keeps
whatever state it had, and the transition set is untouched: adding `deleted` as
a state would need a transition from every other state, would collide with the
roll-up rules (004 §6), and would make "deleted" and "abandoned" look like
alternatives on one axis when they are two axes.

Prefer `abandon`. Reach for `delete` when the row is noise rather than history.

## 2. The tombstone {#sec-2}

Delete never removes a row. The events log is append-only (004 §2) and every
event, `state_log` row, edge, and artifact referencing a task or document stays
valid; a hard delete would either orphan them or cascade into the provenance
record, and both are worse than a hidden row.

Tasks and design documents each carry three columns, all null on a live row and
all set together:

| Column | Meaning |
|---|---|
| `deleted_at timestamptz` | when it was tombstoned; null means live |
| `deleted_by text` | the actor id that deleted it |
| `delete_justification text` | the reason, required per §3 |

`deleted_at IS NULL` is the whole predicate — `deleted_by` and
`delete_justification` are payload, never a filter. Undelete clears all three
in one statement.

Both operations emit through the events log like every other mutation:
`task.deleted` / `task.undeleted`, `doc.deleted` / `doc.undeleted`, carrying the
justification in the payload. The tombstone is a fact about the row; the event
is the record of who made it.

Three things move with the tombstone, all in the deleting transaction, because
each of them would otherwise leave the row in a state undelete cannot get out
of:

- **The lease is released**, and an `in_progress` task goes back to `ready`. A
  hidden task cannot be worked, so keeping the lease would leave the sweeper
  tending a row nothing can see; but releasing it alone would strand the task
  `in_progress` with no lease, which the sweeper never revisits and `Claim`
  refuses. Undelete has to hand back something claimable.
- **The parent's roll-up is recomputed**, on delete and on undelete. Roll-up
  only re-runs on a child's transition (004 §6.1), so deleting the last open
  child would otherwise leave a parent sitting at `in_progress` with no sibling
  left to trigger the move.
- **The document's slug and corpus number are released.** The uniqueness that
  makes them identities is over live rows only. A deleted document keeps both
  and stays addressable by them, but stops reserving them, so the correction
  that motivated the delete — a wrong corpus number, a duplicate import — can
  actually be created. Where a slug now names both a live document and a
  tombstone, the live one wins; a tombstone never shadows a live row.

Deleting a task or document does **not** cascade. A parent with children, or a
document with covering plans, tombstones alone; the references stay resolvable
and keep naming a row that still exists. Cascading deletion is a bigger decision
than this spec makes.

## 3. Justification, by instance environment {#sec-3}

`LODE_INSTANCE_ENV` (039 §3) decides whether a justification is required:

| Instance | `justification` |
|---|---|
| `prod` | required — a delete without a non-blank one is refused |
| `dev` | optional — accepted when given, omitted without complaint |

Nothing else varies. Both instances tombstone, both stamp `deleted_by`, both
emit the event, and a justification given on a dev instance is stored exactly as
one given on prod. The environment gates the *demand*, not the mechanism, so a
row deleted on dev and a row deleted on prod are the same kind of row.

The requirement is enforced server-side, at the API boundary, because the server
is the only party that knows which instance it is. A client may prompt for a
justification, and must not decide the rule.

Undelete requires no justification on either instance. The asymmetry is
deliberate: deleting hides the record, undeleting restores it, and only the
first is worth making someone stop and type.

## 4. What a tombstone hides {#sec-4}

A tombstoned row is excluded from every list, query, ranking and pickup path by
default. `deleted_at IS NULL` joins the `WHERE` clause everywhere a live row is
what the caller means: task lists and boards, `claim`/`claim next` and the
ranking that feeds them, roll-up and delivery resolution, the cockpit, document
lists and searches, and the projections built from any of them.

Fetching a tombstoned row **by id** succeeds and renders the tombstone — the
actor, the time, the justification. An id an agent already holds should not
report "not found" when the truth is "deleted, by this person, for this reason";
that answer is what makes the mistake recoverable rather than mysterious.

Two deliberate consequences:

- A deleted task's events stay in the log and in `lode event tail`. The log is
  the provenance record and is not filtered by the visibility of its subjects.
- Deleting a task does not retract its edges. `WL-9 blocks WL-12` with `WL-9`
  deleted stops blocking, because the blocking check reads live tasks, and the
  edge itself stays for the undelete.

**Deleted is not closed.** A tombstone changes no answer about the task's own
state, `closed` included: 004 §1.3's predicate stays "delivered, or abandoned",
so a deleted `draft` reports `closed: false` and says so on the wire. What the
tombstone changes is which rows a *query* considers at all. Every place that
would otherwise treat a deleted task as unfinished work — the blocking check,
the plan's open-task set, a container's children — filters it out where it
reads, rather than by calling it closed. The distinction matters because
`closed` is a claim about work and a tombstone is a claim about the row, and
one predicate answering both would make "this was delivered" and "this never
should have existed" indistinguishable to everything downstream.

A tombstone hides a row; it does not freeze it. An edit or a state change
addressed to a deleted row by id still applies, because the caller naming an id
it already holds is being explicit, and refusing would mean teaching every
mutation a rule that only `undelete` then repeats. What a tombstone does stop is
everything that *finds* the row for you — pickup, ranking, roll-up, and the
delivery resolver that would otherwise advance a task nobody can see.

## 5. Surfaces {#sec-5}

**API.** `DELETE /api/v1/tasks/{id}` and `DELETE /api/v1/docs/{id}`, each taking
an optional JSON body `{"justification": "..."}`; undelete is `POST
…/{id}/undelete`. All four are guarded through `routeGuards` and need the
permission the task's and document's other mutations need — delete is not an
admin-only act, and a per-role delete permission would be the first of a real
RBAC model this repo does not have yet (001 §9.2).

A prod instance refusing a justification-less delete answers `422`, the status
this repo already gives a field that failed validation, and names the instance
environment in the message — the request is well-formed, and would have been
accepted by the same server configured the other way.

**CLI.** `lode task delete <id> [--justification …]` and `lode doc delete
<ref> [--justification …]`, with `lode task undelete` / `lode doc undelete`
alongside. `--justification` is spelled the same on both, and `-j` is not
taken as a shorthand: the flag should cost a moment's typing.

`lode task list` and `lode doc list` gain `--deleted` to show tombstoned rows
instead of live ones — the flag is a switch, not an addition, because a list
mixing the two invites acting on a row that is not there.

## 6. Metrics {#sec-6}

`worklode_deletes_total{entity, op, outcome}` in `internal/api`, following 022
§8: `entity` is `task` or `doc`, `op` is `delete` or `undelete`, and `outcome`
is `ok`, `justification_required`, `not_found`, or `error`. The
`justification_required` outcome is the one worth a dashboard — a prod instance
counting them is watching a client that has not learned the rule.
