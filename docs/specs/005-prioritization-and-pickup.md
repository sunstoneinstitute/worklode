---
status: accepted
issued: 2026-07-21
wasDerivedFrom: 003-platform-graph-design.md (D8, D9, D10, D12, D15)
requires:
  - 004-execution-backbone.md
amendedBy:
  ".":
    - 018-task-hierarchy.md
replaces:
  ".":
    - 003-platform-graph-design.md
---
# Spec 005 — Prioritization & pickup

## 0. Purpose & scope {#sec-0}

This spec defines **what Worklode picks next and why** — the task properties that carry
priority signal (`concern`, `priority`), the per-project steering knob (`project.focus`), the
**ranking function** that orders ready work, the atomic **`lode task claim --next`** command
surface, the **`--strict-focus`** modifier, and the **`needs-decomposition`** sizing gate that
keeps oversized tasks out of the pickup loop.

**Multi-player framing (000).** Pickup is where the coordination layer earns its keep. With one
human and one agent in one repo, choosing the next task is a private decision and any consistent
ordering works. Worklode assumes a crowd — many people and many agents against one backbone — so
selection is itself shared state, and both the ranking and the claim have to stay correct under
contention.

The whole point (D8): today `claim` needs a task id, so an agent must **list → pick → claim**,
which races other agents → work is hand-kicked to dodge collisions → throughput is capped.
`claim --next` closes that window: the server **ranks and leases in one transaction**, so no two
agents can select the same task. That is what makes **safe unattended 24/7 parallel pickup**
possible on a well-spec'd project.

**In scope:** the selection predicate, the sort key, the CLI surface and its `--json` output, the
focus/strict-focus semantics, and the decomposition gate.
**Out of scope (referenced, not duplicated):** the lease/claim *transaction internals* and Postgres
mechanics (**004**); the graph vocabulary and IRI scheme (**006**); the drift queries and the
read-only overview frontier (**007**); plugin slash commands and hooks (**008**). Note: `claim --next`
computes its ranking **authoritatively on the backbone** (004 data) — 007's frontier is the
eventually-consistent **overview mirror**, never the atomic source.

## 1. `concern` & `priority` model (D10) {#sec-1}

Two orthogonal task properties carry prioritization signal. Both are **fixed, closed enums** that
grow only by explicit schema change (same discipline as today's `priority`).

**`concern`** — *what kind of value the task delivers.* The **name is `concern`** (chosen over
`dimension`/`aspect`). The v1 enum:

| concern | meaning |
|---|---|
| `completeness` | close a gap between declared intent and observed reality (missing feature/spec) |
| `performance` | make existing behaviour faster / cheaper |
| `usability` | improve the experience of an existing capability |
| `security` | close a vulnerability or harden a surface |

The trailing `…` in the design record is **not** an open value — it is a placeholder for future
enum members added by schema change. A task has **exactly one** `concern` (nullable in v1;
null sorts last in `concern_rank`).

**`priority`** — *how urgent,* independent of concern. Existing enum, unchanged:
`critical | high | medium | low`. `critical` is load-bearing in the ranking (see below).

`concern` and `priority` are independent: a `security` task may be `low`, a `usability` task may
be `critical`. Neither derives from the other.

## 2. `project.focus` semantics (D9) {#sec-2}

`project.focus` is a per-project, **ordered list of concerns** — the project's current steering,
e.g. `focus = [security, completeness]`. It is a **soft filter**, and softness is the whole
contract:

- It **directs the choice only when no higher-signal factor decides.** Below `critical`, among
  otherwise-comparable ready tasks, focus orders which concern is picked first.
- **Agents never idle while ready work exists.** Focus is a *sort preference*, never a *filter that
  removes rows*. When no in-focus task is ready, the agent **drifts to off-focus ready work rather
  than stall.** A concern absent from `focus` is not excluded — it simply ranks after every
  in-focus concern.
- **Critical bypasses focus by default** (see ranking). A `critical` task is picked regardless of
  focus; `--strict-focus` is the opt-out.

`concern_rank(task)` = the task's index in `project.focus` (0 = first/highest); any concern **not
listed** in `focus`, and a null concern, share the worst rank (sort last, stable among themselves).

## 3. The ranking function (D9, D12) {#sec-3}

`claim --next` selects from the **ready set** — tasks that are, per spec 004/007:
**ready** (all `blocks` dependencies satisfied) **AND unblocked** (no open blocker) **AND
unclaimed** (no live lease) **AND claimable** (not labelled `needs-decomposition`).

Over that set it applies the **default sort key**, descending priority of signal:

```
(is_critical, concern_rank, priority, blocking_fan_out)
```

1. **`is_critical`** — boolean, `priority == critical`. True first. *Critical bypasses focus.*
2. **`concern_rank`** — index in `project.focus` (lower first); unlisted/null last.
3. **`priority`** — `critical > high > medium > low` (redundant with #1 only for the critical tier;
   orders the rest).
4. **`blocking_fan_out`** — **how many tasks/deliverables the task transitively unblocks.** Higher
   first. This is the **estimate-free criticality proxy** (D12): with no effort estimates in v1,
   unit-weight transitive fan-out over the `blocks` edge stands in for "how much is waiting on
   this." Computed on the backbone's `blocks` graph (004/005) — backbone-only, so the atomic claim needs
   no KG read. (007's richer cross-store critical path is a separate, overview-only metric.)

Ties after all four keys resolve by a **stable deterministic tiebreak** (oldest `created_at`, then
task id) so ordering is reproducible and starvation-free.

The server takes the **top row** and leases it in the same transaction (below).

### 3.1 Worked example {#sec-3.1}

Project `focus = [security, completeness]`. Ready set:

| task | priority | concern | fan_out |
|---|---|---|---|
| T1 | high | completeness | 5 |
| T2 | high | security | 1 |
| T3 | critical | usability | 0 |
| T4 | medium | security | 8 |
| T5 | high | performance | 12 |

Sort key `(is_critical, concern_rank, priority, fan_out)`, with `concern_rank`:
`security=0, completeness=1, {usability, performance}=∞`:

1. **T3** — `is_critical=true` wins outright (critical bypasses focus), *despite* `usability` being
   off-focus and fan_out 0.
2. **T2** — security (rank 0), high.
3. **T4** — security (rank 0), medium.
4. **T1** — completeness (rank 1), high.
5. **T5** — performance (off-focus), high, fan_out 12 — **drifted to, not excluded** (agents never
   idle: with T1–T4 taken, T5 is still picked over idling).

`claim --next` returns **T3** and leases it.

Under **`--strict-focus`** (drop `is_critical`; key `(concern_rank, priority, fan_out)`): order
becomes **T2, T4, T1, T3, T5** — T3's critical no longer preempts; it sorts by its off-focus
concern, though still ahead of nothing by priority within its rank.

## 4. `claim --next` CLI & behaviour (D8) {#sec-4}

```
lode task claim --next [--project <id>] [--strict-focus] [--dry-run] [--json]
```

**Behaviour — one atomic step:** the server, in a **single serialized transaction** (mechanics in
004), evaluates the selection predicate + sort key over the ready set, takes the top-ranked
**ready + unblocked + unclaimed + claimable** task, and **leases it to the calling worktree**
before returning. There is **no list → pick → claim window**, therefore **no collision**: two
concurrent `claim --next` calls are serialized and get **different** tasks (or one gets a task and
the other gets "none ready"). This is the property that enables safe 24/7 parallel pickup.

**Flags:**
- `--project <id>` — restrict the ready set to one project (uses that project's `focus`). Omitted =
  all projects the caller may work; `concern_rank` then uses each task's own project focus.
- `--strict-focus` — see below.
- `--dry-run` — return the task that *would* be claimed **without** leasing it. This is the only
  non-atomic, non-binding read; it must **not** be used as a pick-then-claim step (that reintroduces
  the race). Intended for previews / `/lode-status`.
- `--json` — deterministic machine output (the plugin always passes this; D14 CLI-not-MCP).

**Outcomes:**
- **Claimed:** exit 0; `--json` emits `{ "claimed": true, "task": { id, slug, concern, priority,
  fan_out, project, lease: { worktree, expires_at } } }`. The deterministic `slug`/`id` feed the
  worktree name `wt/<id>-<slug>` (008).
- **None ready:** exit 0, `{ "claimed": false, "reason": "no-ready-task" }` — an **empty ready set
  is normal**, not an error (the 24/7 loop polls). Distinct exit-0 reasons let the loop back off vs.
  stop.
- **Error** (no project, auth, backbone down): non-zero exit, `{ "error": … }`.

The lease binds to the **git worktree, not the session** (D14); its lifecycle, renewal
(commit-cadence heartbeat), and expiry/sweep are **004**.

## 5. `--strict-focus` (D9) {#sec-5}

`--strict-focus` **suppresses the critical-first jump** so `project.focus` governs **even critical
tasks**. It drops `is_critical` from the key:

```
(concern_rank, priority, blocking_fan_out)
```

Critical work is **still ordered by priority within its concern** — it just **no longer preempts
focus**. Use it when a project must burn down one concern (e.g. a `security` sprint) without
critical work in other concerns yanking agents away.

Default (flag absent) = critical-first. The flag is an explicit, per-invocation opt-out; there is
no persistent project-level "always strict" setting in v1 (see open questions).

## 6. Task sizing & `needs-decomposition` (D15) {#sec-6}

Not every task is claimable. **`needs-decomposition`** is a task **label** meaning the task's scope
exceeds the context **"smart zone"** — the effective-reasoning region well below the model's hard
limit.

- A task labelled `needs-decomposition` is **excluded from the ready set** — `claim --next` will
  **never** select it. It is not "ready work an agent can drift to"; it is **not claimable at all**
  until split.
- Such a task **routes to decomposition first**: produce a Spec/Plan that splits it into child
  tasks (via `child_of`, spec 004/006). **Decomposing a big task is itself a Worklode task** — a
  normal, claimable one with its own `concern`/`priority`.
- **The "too big" call is agentic, made at review (crit)** — not a static pre-filter. A reviewer
  (human or agent) sets the label when the task's **projected context** (brief + governing
  spec/plan excerpt + affected components + definition-of-done, per `lode task brief`) would blow
  the budget.
- Backed by a concrete, **server-side-configurable token budget, default ~100k.** The number is a
  guide for the agentic call and a ceiling the reviewer checks against — **not** an automatic
  detector (Q15.1: no separate heuristic needed for v1).
- Clearing the label (after the split lands) makes the parent's children — not the parent — the
  claimable units.

## 7. Dependencies {#sec-7}

- **004 — Execution backbone:** task state machine, `concern`/`priority`/label storage, the `blocks`
  and `child_of` edges, the atomic lease transaction, worktree-bound lease lifecycle & heartbeat.
- **007 — Drift & overview:** the **read-only overview** frontier that *mirrors* this spec's ranking
  (005 is authoritative and computes on the backbone), plus the richer cross-store critical path used
  only for human overview — never by the atomic `claim --next`.
- **006 — Knowledge graph:** projection of Task (with `concern`) into the graph; `child_of` /
  `hasPart` decomposition modelling.
- **008 — Plugin:** `/lode-next`, `/lode-status`, and the worktree that receives the lease consume
  this command surface.

## 8. Open questions {#sec-8}

1. **`--strict-focus` name — RESOLVED: `--strict-focus`.** It reads as "make focus strict (a hard
   ordering) rather than soft," which is exactly the semantic (focus governs even critical) and pairs
   naturally with the default soft-focus story. (`--focus-first` was weaker: focus is *already*
   applied first below critical, so that name wouldn't distinguish the flag's actual effect.)
2. **Persistent strict-focus per project?** v1 is per-invocation only. Should `project.focus` carry
   a `strict: bool` so a security-sprint project defaults to strict without every call passing the
   flag? Deferred — adds config surface; revisit if sprints are common.
3. **`concern` null policy.** v1 allows null concern (sorts last). Should `concern` be **required**
   at task creation once the enum stabilises? Leaning required, but deferred for now.
4. **Cross-project fan_out weighting.** With `--project` omitted, `blocking_fan_out` counts across
   projects uniformly. Is an in-focus/in-project task's fan_out worth more than a cross-project
   one? v1 treats them equally; flag if starvation appears.

## 9. Acceptance criteria {#sec-9}

1. **Atomicity / no collision.** N concurrent `claim --next` calls against a ready set of M tasks
   yield **min(N, M) distinct** claims and **zero double-claims** — verified under contention.
2. **Ranking correctness.** Given the worked-example fixture, default `claim --next` returns **T3**;
   with `--strict-focus` it returns **T2**. Full order matches the spec for both keys.
3. **Focus is soft.** With an empty in-focus ready set but off-focus ready tasks present,
   `claim --next` **returns an off-focus task** (never idles) — and with in-focus tasks present it
   prefers them.
4. **Critical bypass + opt-out.** A `critical` off-focus task is picked first by default; the same
   task is **not** picked first under `--strict-focus`.
5. **Decomposition gate.** A `needs-decomposition` task is **never** returned by `claim --next`
   (default or strict); removing the label + landing child tasks makes the **children** claimable.
6. **Stable output.** `--json` shape is exactly as specified; "none ready" is **exit 0** with a
   machine-readable reason, distinct from error exits.
7. **Determinism.** Identical ready set + focus ⇒ identical selection across runs (stable
   tiebreak); no starvation of the lowest-ranked ready task over repeated claims.
