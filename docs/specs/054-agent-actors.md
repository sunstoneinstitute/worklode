---
status: draft
issued: 2026-08-23
requires:
  - 001-identity-and-authentication.md
  - 012-agent-sessions.md
amends:
  "#sec-4":
    - 001-identity-and-authentication.md#sec-2.1
---
# Spec 054 — Agent actors

## 0. Problem {#sec-0}

`actors.kind` has admitted `agent` since the baseline migration, and task-scoped
tokens (001 §2.1, WL-306) now mint against an agent actor by default — but
nothing says what an agent actor *is*, so the first person to provision one
picks the dimension to split along, and every later provisioner picks a
different one. The candidates on offer are all plausible and mostly wrong:

- **by specialty** — `security-agent`, `devops-agent`, `reviewer`;
- **by harness** — `claude`, `codex`, `copilot`;
- **by launch configuration** — `claude-janitor`, a bootstrap prompt and a
  model tier baked into an identity;
- **by model and effort** — `sonnet-worker`, `opus-reviewer`.

Whichever is chosen becomes durable: an actor id is a foreign key from
`tasks.created_by`, `tasks.assignee`, `tokens.actor_id`, `leases.actor_id` and
`project_participants.actor_id`, and unwinding a bad one is the merge procedure
001 §9.5 had to write for the `github:<id>` mistake. The dimension has to be
right before the rows exist.

**Decision.** An actor is an accountable principal with credentials, and
nothing else. Agent actors are provisioned per *authority*, not per harness,
per model or per trade. Three or four exist org-wide, all of them naming a
dispatch mechanism with its own permissions; everything a human launched
attributes to a shared agent identity with the launching human recorded
alongside it.

## 1. What an actor is for {#sec-1}

An actor row does exactly four things. It holds tokens, it can be denied a
permission by the `grants` table, it holds leases and assignments, and costs
roll up to it. Every one of those is about authority and accountability; none
of them is about capability.

So there is a single test for whether a proposed split deserves an actor row:

> Would we ever want to revoke this one's credential without revoking the
> other's, or grant it a permission the other does not have?

If the answer is no, it is one actor and the distinction belongs on some other
column. The test is deliberately about *policy*, not about telemetry: "we would
like to see these apart in a report" is answered by a query over data worklode
already records, and is never a reason to mint an identity.

## 2. Dimensions that are not identities {#sec-2}

### 2.1 Specialty {#sec-2.1}

A "security agent" or "devops agent" encodes into an identity something the
backbone already states in three better places: the task's `kind`, the skills
the task declares (016, 037 §4), and the project's role-labelled participant
rows (029 §6.1). An actor row would be a fourth, unreconciled statement of the
same fact, and the first time a security-labelled agent picks up a chore task
the four disagree with no rule for which wins.

It is also a category error about how the work is actually done. Specialty is a
property of the task, expressed as the skills its executor loads; the executor
is general and becomes specialised for the duration. An identity is permanent,
so encoding a temporary property in one guarantees drift.

Note that 029 §6.1 already forbids the adjacent move — agents are not Crew
members, and the accountable human stays the project lead. Specialist agent
actors would be Crew membership for agents in everything but name.

### 2.2 Harness {#sec-2.2}

`agent_sessions` already records the harness on every session: `agent` (drawn
from `model.KnownAgents` and its CHECK constraint) plus `agent_version`, and
`agent_session_usage` carries the per-day, per-model, per-speed token
breakdown that prices it (012, 052). A `claude` actor and a `codex` actor
would denormalise a column that already exists, and every cost or provenance
query would then have two sources of truth for one fact — with no constraint
keeping them agreeing, because nothing stops a token attributed to `claude`
being used by a session reporting `agent: codex`.

Harness also fails the §1 test outright. There is no permission worklode would
grant Claude Code and withhold from Codex; the two are interchangeable
executors of the same work under the same authority.

### 2.3 Model and effort {#sec-2.3}

The same argument, more so: model is recorded per usage bucket and priced from
effective-dated `model_prices` rows, it is the most volatile attribute in the
system, and identities minted per model would need provisioning every time a
model ships. Which model a task warrants is a decision made *during* execution,
by the executor or by the task's own instructions — after the credential has
already been issued.

## 3. The roster {#sec-3}

### 3.1 Human-dispatched work {#sec-3.1}

Work a human launched — an interactive coding session, a sandbox dispatched
from a laptop or from CI on someone's behalf — acts as the single shared agent
actor **`sandbox`** (`kind = 'agent'`, never admin) whenever it runs under a
task-scoped token, auto-provisioned on first mint as WL-306 already
implements. One row for all of it, because all of it carries exactly the
authority 001 §2.1 defines and there is nothing to revoke independently.

The identity is `sandbox` rather than the launching human because an agent's
writes must be distinguishable from a person's in the audit trail — a claim,
a state transition or a document edit made by an agent is a different fact from
one a person made, and 001 §2.1 is right to insist on that. The human is not
lost: §4 records them on the token.

That guarantee presumes the session is holding a task-scoped token in the
first place. §6 names the case where it is not.

### 3.2 Unattended automation {#sec-3.2}

An automation that runs with no human behind it gets its own actor, because
that is where the §1 test starts answering yes. `lode watch`'s pod informer
reports runtime events and should be able to do nothing else; a cron janitor
sweeping stale chores should be able to claim `kind = 'chore'` and not to
accept a document. Those are real, writable differences in the `grants` table,
and each one justifies exactly one row: `watcher`, `janitor`, and whatever
comes next on the same terms.

Provisioning one is a deliberate act with a written reason. A new agent actor
lands together with the permission difference that motivates it; an actor
provisioned with the same grants as `sandbox` is `sandbox` under another name.

### 3.3 Naming {#sec-3.3}

An agent actor id names the **dispatch mechanism and its policy** — `sandbox`,
`watcher`, `janitor` — never the harness, the model, the effort tier or the
trade. Ids are stable, lowercase, and carry no vendor name, so that replacing
the harness behind a mechanism is a configuration change and not an identity
migration.

## 4. The dispatching human {#sec-4}

> **Amends 001 §2.1.** A task-scoped token records the actor that minted it in
> a column rather than in its description prose.

Today `POST /api/v1/tasks/{id}/tokens` preserves its caller only inside the
token's free-text description (`"task-scoped token minted by " + actorID`),
which is unqueryable and unjoinable. With §3.1 collapsing all human-dispatched
agent work onto one actor, that string becomes the only record of who is
accountable for a given agent's actions — which is too much weight for prose.

Migration `0052_token_minted_by`:

```sql
-- The actor that minted this token, when that differs from the actor it acts
-- as. NULL for a token an actor minted for itself (an actor-scoped `lode
-- login` token, the bootstrap token) — there the two are the same row.
-- ON DELETE SET NULL, not RESTRICT: the minter is provenance, and deleting a
-- departed human's actor must not be blocked by tokens that have long expired.
ALTER TABLE tokens ADD COLUMN minted_by text REFERENCES actors (id) ON DELETE SET NULL;
```

`mintTaskToken` sets it from the request's subject and stops repeating it in
the description. Nothing else changes: the token still acts as its `actor_id`,
authz still decides on that, and `minted_by` is read only for provenance —
"which human dispatched the agent that made this change" becomes a join rather
than a string match.

## 5. Launch profiles are configuration {#sec-5}

The dimension the rejected candidates were reaching for is real: a harness, a
bootstrap prompt, an initial model and effort, and a skill set do form a
meaningful named bundle, and "the janitor setup" is a useful thing to say. That
bundle is a **launch profile**, and it is configuration — it belongs to the
plugin and the repo that dispatch the agent, not to the backbone's identity
table.

The distinction that matters is lifetime. A profile is chosen per run and can
change between two runs of the same automation with no migration and no audit
consequence; an actor is provisioned once and referenced forever by rows that
outlive it. Putting a profile in an identity gives the volatile thing the
lifetime of the durable one.

Where a specific task genuinely needs a specific setup, the task says so — in
its body, or in the `skills` its plan declares (026, plan task metadata) — and
the executor reads it. That keeps the requirement attached to the work that has
it, and leaves the executor free to escalate its own model mid-task, which is a
decision no credential can usefully constrain.

This spec deliberately defines no schema, no vocabulary and no endpoint for
profiles. If they later need to be shared org-wide, the skill registry (016) is
the existing mechanism for org-wide agent configuration and should be extended
rather than joined by a second one.

## 6. Non-goals {#sec-6}

- **No new actor kind.** `human`, `agent` and `service` are enough; this spec
  says how to populate `agent`, not what to add beside it.
- **No per-actor RBAC model.** The `grants` table with `user`/`admin` stays as
  it is until an unattended actor under §3.2 needs a narrower grant, and that
  narrowing is the change that introduces it — not a speculative role system
  introduced first.
- **No agent Crew membership.** 029 §6.1 stands unchanged.
- **No ontology change.** This spec introduces no frontmatter key and no `wl:`
  term; `ns/` is untouched.
- **No transparent token swap for interactive sessions.** §3.1's attribution
  guarantee holds once a session is running under a task-scoped token; it
  does not make that happen. A session working under a human's own
  actor-scoped `lode login` token — the common path for an interactive CLI
  session today, since nothing there ever calls `POST
  /api/v1/tasks/{id}/tokens` — still attributes every write to that human.
  Closing that gap is a session-lifecycle change, not an actor-roster one,
  and is deferred to WL-611.
