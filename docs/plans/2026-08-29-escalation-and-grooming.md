---
status: draft
covers:
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.1
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.6
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.7
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.8
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-15.5
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-24
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-decision-tasks.md
      - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
      - docs/plans/2026-08-29-doc-version-graphs.md
blockedBy:
  - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
---

# Documents part 4 — escalation, stale plans, grooming

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** the upward and downward halves of 025 §8 that
`doc-accept-gate-and-amendment-plan` did not build. Upward: `lode task
escalate` releases the executor's lease, mints a `design` task assigned to
the plan's author, wires the `blocks` edge that resumes execution when the
amendment lands, and deduplicates against an open escalation for the same
section (§8.1); the ladder's remaining events — `task.gap_found`,
`fix.started`, `fix.finished`, `doc.stale` — land on the log (§15.5).
Downward: a §8.2 patch marks unexecuted covering plans `stale` and a
re-planning task is minted (§8.6); a grooming sweeper emits `doc.stale` when
an accepted document crosses the staleness threshold with no execution, the
subscriber mints the grooming task, and stale documents are flagged wherever
they render (§8.7). `claim --next --kind` takes a list so the escalated
task is not claimed by the loop that could not resolve it (§8.8).

**Series:** part 4 of 4 over spec 025's unplanned sections, `blockedBy:`
`doc-accept-gate-and-amendment-plan` (WL-PLAN-109), whose interfaces this
plan calls by name: `store.PatchDoc` and
`model.DocPatchResult.UnexecutedCoveringPlans` (the §8.6 seam left for this
plan), `store.AddDocNote`, the `doc.patched` event, and the
`worklode_doc_operations_total{op,outcome}` counter. If an executor finds
one of those shapes landed under a different name, the sibling plan's
"Interfaces the escalation part builds on" section is the contract — follow
what actually landed and say so in the PR.

## Decisions this plan executes (made against the spec; do not reopen)

- **"Moves the task to `blocked`" (§8.1) is the derived-blocked model, not a
  new state.** This store has no `blocked` state: blocked is a query over
  open `blocks` edges (`BlockedTaskIDs`), and `readyCandidates` already
  excludes it. Escalate releases the lease (the task returns to `ready` via
  the existing release path) and adds the `blocks` edge from the minted
  task; the ready-set exclusion is what §8.1 means by blocked, exactly as
  `lode block` works today. No `tasks.state` migration.
- **The escalation task's kind is `design`.** §8.1 says "mints a `spec`
  task" in §10's pre-rename vocabulary; migration 0025 renamed `spec` to
  `design` and §24 criterion 1 fixes the seven kinds. Same for §8.7's
  grooming task.
- **Dedup is `(about_doc, about_anchor)` over open `design` tasks.**
  `tasks.about_doc` exists; a nullable `about_anchor` column joins it so
  "same document section" (§8.1) is queryable. Joining means: no second
  mint, a second `blocks` edge from the existing open task to the newly
  blocked one. A NULL anchor deduplicates at document level.
- **Escalation target:** `--to plan` targets the task's `plan_doc` (error
  when the task has none); `--to spec` targets the one spec the plan's
  `covers` edges name, and requires `--doc <ref>` when they name several.
- **Assignee is the target document's `created_by`** (§8.1 "the human who
  authored the plan"; 025 §12 makes `created_by` the author), falling back
  to the document's `assignee`, else unassigned.
- **The four new events are dotted backbone types** recorded through the
  existing `RecordEvent` path (`doc.deleted`, `doc.approval_requested`, and
  the sibling's `doc.patched` are the precedent) — bookkeeping, not RDF, so
  no `eventbus` vocab entry and no JSON-LD payload validation.
- **One mint path for both stale causes.** `doc.stale` carries
  `cause: "amended"` (§8.6, emitted in the patch transaction) or
  `cause: "clock"` (§8.7, emitted by the sweeper). The `doc-lifecycle`
  subscriber mints the follow-up `design` task for either — "Re-plan" for a
  plan, "Groom" for a spec — suppressed while an open `design` task already
  references the document, exactly the §15.4 guard shape. §8.6's
  re-planning task is therefore minted by the subscriber, not in the patch
  transaction; the transaction's job ends at the status flip and the event.
- **`stale` and `withdrawn` join `wlc:DesignDocStatus`** and the docs CHECK.
  Spec 025 already specifies both (§8.6, §8.7), so this is the ns/ mirror
  step, not a spec amendment. `stale` is only ever set on plans; `accept`
  gains the `stale → accepted` transition (re-acceptance clears it, §8.6),
  and re-accept already mints only missing declarations (§24 crit 38).
  Order: `draft → accepted → stale → superseded → withdrawn` in
  `wlc:DesignDocStatusOrder`; the pinned `ns_test.go` order updates with it.
- **A stale plan's coverage lapses by construction.** The aggregate coverage
  query counts accepted-or-superseded plans, so flipping a plan to `stale`
  makes its sections report unplanned again — that is §8.6's "regenerated,
  not patched" pressure, and no coverage code changes.
- **§8.6's "flagged at claim time" option, not re-derivation:** claiming a
  task whose `plan_doc` is `stale` succeeds with a warning in the claim
  response and the brief, so an agent knows its task text predates the
  amendment. Re-deriving unclaimed tasks is the re-planning task's job.
- **Sweeper scope:** accepted **plans** none of whose minted tasks was ever
  leased, and accepted **specs** with no accepted plan covering any section.
  ADRs record decisions already taken — there is nothing to execute, so
  they are never groomed. Clock base is `docs.updated_at` (any revision or
  patch touches it, which is §8.7's "any revision re-arms the clock");
  fire-once is the event log's own `(source, external_id)` dedup with
  `external_id = doc.stale:<slug>:<version>`, so a version bump re-arms and
  an unchanged doc cannot re-fire.
- **Threshold config:** `LODE_DOC_STALENESS_DAYS` (default 30) on
  `lode-server`, per-project override in a nullable
  `projects.doc_staleness_days` column. The threshold arithmetic is one
  pure function so the table test needs no Postgres.
- **§8.8 is CLI/API/store only, no migration.** The wire field
  `ClaimNextInput.Kind` stays one string, now comma-separated; the API
  splits and validates each element against `validKinds`; the store filter
  becomes `kind = ANY(...)`. Tier defaulting is the `lode:next` skill's
  text, not server logic.
- **Metrics:** escalate mutates a task, not a document, so it does not ride
  `worklode_doc_operations_total`; it gets
  `worklode_task_escalations_total{outcome}` (`minted|joined|error`). The
  groom sweep is a background loop: `worklode_doc_groom_runs_total{outcome}`
  plus `worklode_docs_stale_emitted_total`. Stale-marking and withdraw are
  document operations and extend the existing docOps `op` set
  (`stale`, `withdraw`).

## Global Constraints

- ADR 036 layering: wire shapes in `internal/model` (stdlib-only), store
  scans into them, `internal/cmd` decides, `internal/cli` renders; new
  human-readable views are `cli.*Table`/`cli.*Render` in `internal/cli`.
- New routes join `routeGuards` in `internal/api/router.go` or the server
  refuses to boot; task mutations here are `guardedBound(permTaskWrite)`
  (escalate, gap, fix), doc mutations `guardedAny(permDocWrite)`.
- Migrations: one new numbered pair, listed in
  `deploy/base/kustomization.yaml`, never editing a shipped file; run
  `./scripts/check-migrations.sh --no-fix` (sibling plans claim numbers
  concurrently; the pre-commit hook renumbers).
- `ns/` mirror discipline: the spec already names the terms; edit
  `ns/concept.ttl`, run `riot --validate ns/*.ttl` and `./scripts/nsgen.py`,
  and let the generated `internal/ns/gen.go` carry the enum into the store
  and API validators. Never hand-edit `gen.go`.
- Store and API tests need Postgres with pgvector (`TEST_POSTGRES_DSN`); a
  silently skipped store test proved nothing. Every task leaves
  `go test -trimpath -race -count=1 ./...` green.
- `e2e/` drives public surfaces only.
- File naming: escalation is a task-vertical feature (`store/escalate.go`,
  `api/escalate.go`, additions to `cli/tasks.go`, `cmd/tasks.go`); grooming
  is a doc-vertical feature (`store/docgroom.go`, additions to
  `internal/watcher/doclifecycle.go`). `store/docs.go` and `store/tasks.go`
  are near the 2000-line ceiling — new store code goes in those
  feature-named siblings.

## Tasks

### Task 1 — Migration and ns mirror: stale, withdrawn, about_anchor, staleness threshold

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
  - worklode-docs-authoring
```

One migration pair at the next free number (0058+ — run
`./scripts/check-migrations.sh --no-fix`; three sibling plans are claiming
numbers concurrently). Up:

```sql
-- 025 §8.6/§8.7: stale (plan amended out from under) and withdrawn (closed
-- without a successor) join the document lifecycle.
ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft','accepted','stale','superseded','withdrawn'));

-- 025 §8.1: escalation dedup is per document section. NULL = whole document.
ALTER TABLE tasks ADD COLUMN about_anchor text;

-- 025 §8.7: per-project staleness override, days. NULL = instance default.
ALTER TABLE projects ADD COLUMN doc_staleness_days int
    CHECK (doc_staleness_days > 0);
```

(Check `\d docs` for the real constraint name — 0027 created it inline, so
Postgres may have named it; use the name `\d` reports.) Down restores the
three-value CHECK (first `UPDATE docs SET status = 'superseded' WHERE status
IN ('stale','withdrawn')` so the down migration round-trips on a populated
database) and drops the two columns.

`ns/concept.ttl`: add `wlc:stale` and `wlc:withdrawn` to
`wlc:DesignDocStatus` with one-line `skos:definition`s taken from §8.6/§8.7,
and extend `wlc:DesignDocStatusOrder`'s `skos:memberList` to
`( wlc:draft wlc:accepted wlc:stale wlc:superseded wlc:withdrawn )`. Run
`riot --validate ns/*.ttl`, then `./scripts/nsgen.py`. Update the pinned
order assertion in `internal/ns/ns_test.go` (it hardcodes
`draft/accepted/superseded`). `store.validDocStatuses` and the API's
`validDocStatuses` both read `ns.DesignDocStatuses`, so they pick the new
values up from the generated set — verify no other test pins the old list
(`grep -rn "superseded" internal/*/[a-z]*_test.go`).

- [ ] `./scripts/check-migrations.sh --no-fix` — no collision.
- [ ] `riot --validate ns/*.ttl` — clean; `./scripts/nsgen.py` — `gen.go`
      regenerated.
- [ ] `make test` against Postgres — green, including `internal/ns`.
- [ ] Commit: `Migration + ns: stale/withdrawn doc statuses, tasks.about_anchor, per-project staleness (025 §8.1, §8.6, §8.7)`.

### Task 2 — Pure rules: staleness clock and the groom-on-stale mint

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Two pure additions to `internal/watcher` (no DB, no HTTP — 025 §19), so the
sweeper's threshold logic and the subscriber's mint rule are table-tested
without Postgres.

```go
// StaleInput is everything the §8.7 clock consults for one document.
type StaleInput struct {
    DocKind      string    // spec | adr | plan
    Status       string
    UpdatedAt    time.Time // any revision or patch re-arms the clock
    ProjectDays  int       // projects.doc_staleness_days; 0 = no override
    DefaultDays  int       // instance default (30)
    HasExecution bool      // plan: a task ever leased; spec: an accepted covering plan
}

// StaleAt returns the instant the document crosses the staleness threshold,
// or the zero time when it never does: not accepted, an ADR (decisions
// already taken are never groomed), or execution exists.
func StaleAt(in StaleInput) time.Time
```

Table test: default 30d honored; project override wins over default;
`HasExecution` disarms; ADR always zero; `draft`/`stale`/`withdrawn` always
zero; boundary case (exactly at threshold) documented one way and asserted.

`Evaluate` (`internal/watcher/doclifecycle.go`) gains a `"doc.stale"` case —
the first dotted type the rules act on, so the doc-comment noting dotted
types fall through gets updated in the same edit:

```go
const ruleGroomOnStale = "groom-on-stale"

func evaluateStale(in Input) []Action {
    if in.OpenDesignTask != "" {
        return []Action{{Rule: ruleGroomOnStale, Suppressed: true, NoteTask: in.OpenDesignTask}}
    }
    title := "Groom: " + in.DocTitle // spec: re-evaluate, adjust, or close
    if in.DocKind == "plan" {
        title = "Re-plan: " + in.DocTitle // §8.6: regenerated, not patched
    }
    return []Action{{Rule: ruleGroomOnStale, TaskKind: "design", Title: title, Body: groomBody(in)}}
}
```

`groomBody` quotes the §8.7 charge — "re-evaluate, adjust, or close" — names
`lode doc withdraw` as the close verb, and carries `prov:wasInformedBy
wlid:event/<id>` like its two siblings. Table-test both branches and the
suppression, mirroring `TestEvaluate*` in `doclifecycle_test.go`.

- [ ] `go test -trimpath ./internal/watcher -count=1` — `ok`.
- [ ] Commit: `watcher: pure staleness clock and groom-on-stale rule (025 §8.7)`.

### Task 3 — Store reader: stale-candidate facts

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

`internal/store/docgroom.go`: one bulk reader for the fact family "accepted
documents and whether anything ever executed them". It fetches facts; the
threshold verdict stays in `watcher.StaleAt` so the rule has one owner.

```go
// StaleCandidate is one accepted spec or plan with the §8.7 clock facts.
type StaleCandidate struct {
    DocID        int64
    Slug         string
    Version      int
    Kind         string
    Project      string
    UpdatedAt    time.Time
    ProjectDays  int  // projects.doc_staleness_days, 0 when NULL
    HasExecution bool
}

// StaleCandidateDocs returns every accepted spec and plan with its clock
// facts. Plans count as executed when any minted task ever held a lease;
// specs when any accepted plan covers one of their sections. ADRs are
// excluded here, matching watcher.StaleAt.
func (s *Store) StaleCandidateDocs(ctx context.Context) ([]StaleCandidate, error)
```

`HasExecution` in SQL: for plans, `EXISTS (SELECT 1 FROM leases l JOIN tasks
t ON t.id = l.task_id WHERE t.plan_doc = d.id)` (any lease row, active or
expired — execution happened); for specs, `EXISTS (SELECT 1 FROM doc_edges
de JOIN docs p ON p.id = de.from_doc WHERE de.type = 'covers' AND de.to_doc
= d.id AND p.status = 'accepted')`. Check `doc_edges`'s real column names
against `internal/store/docedges.go` before writing the query — the brief
above is the intent, the schema is the truth.

Store test (Postgres): a fixture per row of the truth table — accepted plan
with no lease (candidate, `HasExecution` false), same plan after a claim
(`HasExecution` true), accepted spec with and without an accepted covering
plan, an ADR (absent), a draft (absent), a project with
`doc_staleness_days` set (field populated).

- [ ] `go test -trimpath ./internal/store -run TestStaleCandidateDocs -count=1` — `ok`.
- [ ] Commit: `store: stale-candidate reader for the grooming sweeper (025 §8.7)`.

### Task 4 — lode task escalate: the §8.1 transaction, store to CLI

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

The whole mutation in one task, per the one-task-per-mutation rule: store
transaction, event, metric, API route, CLI verb.

**Store** (`internal/store/escalate.go`):

```go
type EscalateInput struct {
    TaskID  string
    To      string // "plan" | "spec"
    DocID   int64  // resolved target document
    Anchor  string // optional "sec-N"; "" = whole document
    Reason  string
    ActorID string
}

// EscalateResult: exactly one of Minted/Joined is set.
type EscalateResult struct {
    Minted *model.Task // the new design task, or nil
    Joined string      // id of the open escalation joined instead
}
```

`EscalateTask` runs §8.1's five numbered steps in one transaction:

1. verify the caller holds the task's active lease (same ownership check the
   release path uses), close the lease, transition the task back to `ready`
   — reuse the existing release helper, do not reimplement it;
2. dedup first: an open `design` task with `about_doc = DocID` and
   `about_anchor IS NOT DISTINCT FROM Anchor` short-circuits to a `blocks`
   edge from it to the escalating task, result `Joined`;
3. otherwise mint the `design` task — `about_doc`, `about_anchor`, body
   carrying the reason and the escalating task's id, assignee = the target
   doc's `created_by` (fallback `assignee`, else unassigned), priority
   `high`;
4. add the `blocks` edge minted-task → escalating task;
5. record `task.gap_found` on the events log (dotted type, source `"cli"`
   path the other task mutations use), payload
   `{"task": <id>, "doc": <slug>, "anchor": <or "">, "to": "plan"|"spec", "reason": ...}`.

First store test:

```go
func TestEscalateTask(t *testing.T) {
    // accepted plan P (author "alice") minted task T; claim T.
    // EscalateTask(T, to=plan, anchor="sec-3", reason):
    //   - T's lease closed, T back in ready
    //   - design task D: about_doc=P, about_anchor="sec-3", assignee=alice
    //   - blocks edge D -> T: BlockedTaskIDs()[T.ID] == true
    //   - task.gap_found event recorded with the payload above
    // claim sibling task T2, escalate same doc+anchor:
    //   - Joined == D.ID, no second design task, blocks edge D -> T2
    // escalate without holding a lease: error, nothing written
    // --to spec on a plan covering two specs and no DocID: ErrInvalidInput
}
```

**Metric** (`internal/store/metrics.go`):
`worklode_task_escalations_total{outcome}` counter, `outcome ∈
minted|joined|error`, nil-safe like its neighbours, tested.

**API**: `POST /api/v1/tasks/{id}/escalate`, `guardedBound(permTaskWrite)`
in `routeGuards`, request/response shapes in `internal/model` (ADR 036):
`EscalateTaskInput{To, Doc, Anchor, Reason string}` — `Doc` is a document
ref the server resolves — and `EscalateTaskResult{Minted *Task, Joined
string}`. The handler resolves the target per the Decisions rule (`--to
plan` → `plan_doc`; `--to spec` → unique covers target or 422 naming the
candidates).

**CLI**: `lode task escalate --to plan|spec --reason "..." [--doc <ref>]
[--section sec-N]`, worktree-bound like `lode block` (`resolveWorktreeTask`),
then `purgeTaskSecrets`/`clearTaskBinding` on success, confirmation line via
`internal/cli` (joined vs minted, naming the design task and its assignee).
`--json` prints the response raw.

- [ ] `go test -trimpath ./internal/store -run TestEscalate -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/api ./internal/cmd -run Escalate -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestRenderRule -count=1` — `ok`.
- [ ] Commit: `lode task escalate: release, mint design task, block, dedup (025 §8.1, §15.5)`.

### Task 5 — Ladder telemetry: task.gap_found standalone, fix.started, fix.finished

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

§15.5's funnel needs the ladder's non-escalating rungs on the log too: an
executor that records a gap without stopping, and the fixer's start/finish.
These write events only — no task state change, no lease change.

**API**: two routes in `routeGuards`, both `guardedBound(permTaskWrite)`:

- `POST /api/v1/tasks/{id}/gap` — body `{Doc, Anchor, Reason string}`;
  records `task.gap_found` with the same payload shape as Task 4's.
- `POST /api/v1/tasks/{id}/fix` — body `{Phase, Tier, Doc, Outcome string}`;
  `Phase ∈ started|finished`; `started` requires `Tier ∈ plan|spec` and
  `Doc` ("with the tier and the target document"); `finished` requires
  `Outcome ∈ resolved|substantive|escalated`. Records `fix.started` /
  `fix.finished`. 422 on anything else — the funnel is only worth graphing
  if the label set is closed.

External ids make retries idempotent at the log, matching the
`(source, external_id)` discipline: `task.gap_found:<task>:<doc>#<anchor>`,
`fix.started:<task>:<n>` where `n` is a client-supplied attempt ordinal
(default 1) echoed on `finished` so a start pairs with its finish.

**CLI**: `lode task gap <docref> --reason "..."` and `lode task fix
--phase started|finished [--tier plan|spec] [--doc <ref>] [--outcome ...]`,
both resolving the task from the worktree like `lode done`. These are
plumbing for the skill flow in Task 9, so keep the surface minimal — no
rendering beyond a one-line confirmation.

API test: each route lands its event with the right type and payload
(`lode event tail`'s store reader is the assertion path); invalid
phase/outcome is 422; a replayed `started` does not duplicate.

- [ ] `go test -trimpath ./internal/api -run 'TestTaskGap|TestTaskFix' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/cmd -run 'Gap|Fix' -count=1` — `ok`.
- [ ] Commit: `Ladder telemetry: task.gap_found, fix.started, fix.finished (025 §15.5)`.

### Task 6 — §8.6 stale marking off the patch seam

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

This is the wiring of the seam the sibling plan left:
`model.DocPatchResult.UnexecutedCoveringPlans` — accepted plans covering a
patched section with no claimed task, excluded from §8.2 referrers — comes
back from `store.PatchDoc`, and this task acts on it **in the same
transaction** inside the `POST /api/v1/docs/{id}/patch` handler.

**Store** (`internal/store/docgroom.go`):

```go
// MarkPlansStale flips accepted plans to stale (025 §8.6) and records one
// doc.stale event per plan, payload {"cause":"amended","spec":<slug>,
// "anchors":[...]}. external_id doc.stale:<plan-slug>:<version> — the same
// key the sweeper uses, so a plan already marked by either path is a no-op.
func MarkPlansStale(tx *sql.Tx, now time.Time, planIDs []int64, specSlug string, anchors []string, eventID int64) error
```

Counts on docOps as `op="stale"` per plan (extend the counter's help-string
op list). The re-planning task is **not** minted here — the `doc.stale`
event flows to the doc-lifecycle subscriber, whose Task 2 rule mints
"Re-plan: <title>" once, with the open-design-task guard. That keeps one
mint path for §8.6 and §8.7 and keeps this transaction small.

**Accept clears it** (§8.6 "cleared by re-acceptance"): widen the accept
path's status gate (`internal/store/docplanning.go` — it currently knows
`draft` and re-`accepted`) to accept from `stale`, running the same
plan-accept mint (which by §24 crit 38 mints only declarations without
rows). A spec/ADR can never be `stale`, so only the plan branch changes.

**Claim-time flag** (§8.6's "flagged at claim time"): `Claim`/`ClaimNext`
responses and the brief carry a warning when the task's `plan_doc` has
`status = 'stale'` — one string field on the existing response shapes
(`model`), rendered by `internal/cli` on `lode next`/`lode resume`:
`warning: plan <slug> is stale — task text may predate the amendment
(025 §8.6)`. No refusal: the re-planning task decides what survives.

First store test:

```go
func TestPatchMarksUnexecutedPlansStale(t *testing.T) {
    // accepted spec S#sec-2 covered by accepted plan P, tasks minted, none
    // claimed. PatchDoc on S changing sec-2:
    //   - result.UnexecutedCoveringPlans == [P]  (sibling's contract)
    //   - after MarkPlansStale: P.status == "stale"
    //   - doc.stale event, cause "amended", anchors ["sec-2"]
    //   - repeat: no second event (external_id collision), still stale
    // claim one of P's minted tasks: response carries the stale warning
    // re-accept P (post re-plan edit): status accepted, new declarations
    //   minted, existing tasks untouched
}
```

Subscriber leg (API test, `docwatch` harness): feed the `doc.stale` event
through `handleDocLifecycle` — one `design` "Re-plan" task about P, minted
once; a second event while it is open is suppressed.

- [ ] `go test -trimpath ./internal/store -run 'Stale|PatchMarks' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/api -run 'DocWatch|Stale' -count=1` — `ok`.
- [ ] Commit: `Patch marks unexecuted covering plans stale; re-accept clears (025 §8.6)`.

### Task 7 — The grooming sweeper

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 6]
```

§8.7's clock, on the lease sweeper's tick (004 supplies the clock — reuse
its loop, not a second goroutine): `internal/store/sweeper.go`'s
`sweepLeases` gains a `s.sweepStaleDocs(ctx)` call per tick, and
`internal/store/docgroom.go` implements it:

```go
// sweepStaleDocs emits doc.stale (cause "clock") for every accepted spec or
// plan past its staleness threshold with no execution (025 §8.7). The
// events log's (source, external_id) key — doc.stale:<slug>:<version> —
// makes each firing once-per-version: a revision bumps the version and
// re-arms, an unchanged doc collides and is skipped.
func (s *Store) sweepStaleDocs(ctx context.Context) (emitted int, err error)
```

It reads `StaleCandidateDocs` (Task 3), applies `watcher.StaleAt` (Task 2)
with `s.docStalenessDays` — a store field set from serve config — and
records one event per crossing. Documents already `stale` (the §8.6 path
got there first) are not candidates (`status = 'accepted'` filter in Task
3's query). A clock-fired **spec** stays `accepted` — §8.7 changes how it
is served, not its status; only §8.6 flips a status, and only on plans.

**Config**: `LODE_DOC_STALENESS_DAYS` (default `30`) read in
`internal/cmd/serve.go` alongside its neighbours and threaded to the store;
per-project override already rides each candidate row.

**Subscriber**: no new code — Task 2's `groom-on-stale` rule handles
`cause: "clock"` identically (mint "Groom:" for a spec), and Task 6 already
wired the event type through `handleDocLifecycle`.

**Metrics** (`internal/store/metrics.go`, registered with its neighbours,
nil-safe, both series pre-initialised like `sweeperRuns`):

- `worklode_doc_groom_runs_total{outcome}` — `ok|error` per sweep;
- `worklode_docs_stale_emitted_total` — counter of `doc.stale` emissions.

Store test (Postgres, `nowFn` injected): an accepted unexecuted plan older
than threshold → one event, second sweep → none; project override shorter
than default → fires earlier; a revision (version bump) after grooming →
fires again at the new version; an executed plan → never.

- [ ] `go test -trimpath ./internal/store -run 'SweepStale|Groom' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/store -run TestMetrics -count=1` — `ok`.
- [ ] Commit: `Grooming sweeper: doc.stale on the lease sweeper's clock (025 §8.7, §15.5)`.

### Task 8 — Serving the staleness: flags, withdraw, unresolved

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1, 6]
```

§8.7's rendering half — "a rendering rule rather than a workflow" — plus the
two verbs that make 100% resolution reachable.

- **`lode doc show` / `lode show <ref>` flag it**: the doc render in
  `internal/cli/docs.go` prints a status banner for `stale` and `withdrawn`
  (`STALE since <updated_at> — re-planning owed (025 §8.6)`); the section
  list of a **spec** flags each section whose covering plan is stale
  (`covered by <plan> (stale)`), which is the "stale-plan-covered section"
  flag. Where an edge render names a `requires` target that is stale, the
  suffix ` (stale)` rides the existing edge line — one formatter in
  `internal/cli`, reused, per the render seam.
- **Brief exclusion**: briefs do not inline document bodies today, so the
  §8.7 exclusion lands at the two places documents reach an agent's
  context: `BlockingPlans` entries carry the plan's status (so
  `lode next`'s brief shows `(stale)`), and the doc endpoints that assemble
  reading lists later inherit the rule via the `status = 'accepted'`
  filters they already use — note this in the doc-comment on the render,
  and add the status field now.
- **`lode doc withdraw <ref> --justification "..."`**: `accepted|stale →
  withdrawn`, refusing drafts (discard exists) and superseded (already
  resolved). Store mutation in `store/docgroom.go`, docOps `op="withdraw"`,
  route `POST /api/v1/docs/{id}/withdraw` (`guardedAny(permDocWrite)`),
  event `doc.withdrawn` payload `{justification}` — the close verb §8.7's
  grooming task needs.
- **`lode doc list --unresolved [--older-than 30d]`**: accepted docs with
  no execution (same predicate as Task 3 — reuse `StaleCandidateDocs`'s SQL
  via a shared helper, never a second spelling of "executed"), `--older-than`
  parsed as a duration in days (`30d`) against `updated_at`. Renders through
  the existing doc list table with an age column.

Tests: store test for withdraw transitions (allowed, refused, event,
counter); CLI render test for the stale banner and the `(stale)` suffix;
list test for `--unresolved --older-than` boundary.

- [ ] `go test -trimpath ./internal/store ./internal/api ./internal/cli ./internal/cmd -run 'Withdraw|Unresolved|Stale' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestRenderRule -count=1` — `ok`.
- [ ] Commit: `Stale/withdrawn served: doc show flags, lode doc withdraw, --unresolved (025 §8.7)`.

### Task 9 — §8.8 kind lists, and the skills that close the ladder

```yaml
kind: feature
priority: medium
skills:
  - worklode-lode-plugin
blockedBy: [4, 5]
```

**Server/CLI, no migration** (§8.8: "this needs no schema change"):

- `internal/store/ranking.go` `readyCandidates`: the kind filter becomes
  `AND ($2 = '' OR t.kind = ANY(string_to_array($2, ',')))`.
- `internal/api/lifecycle.go`: split `req.Kind` on commas, trim, run
  `normalizeTaskKind` and the `validKinds` gate per element, rejoin — the
  422 message names the bad element.
- `internal/cmd/lifecycle.go`: `--kind` help becomes "comma-separated list
  of kinds: feature,bug,chore,design,review,spike";
  `warnDeprecatedTaskKind` runs per element. Same for `lode task claim
  --next --kind` if it exposes the flag — check and keep the two aligned.

Store test: two ready tasks of different kinds, `--kind design,review`
claims the design one and never the feature; single-kind behaviour
unchanged; unknown element 422s.

**Skills** (the ladder is a protocol; agents follow it from skill text —
`docs/agent-surfaces.md` is the register and its checklist applies):

- `plugins/claude/lode/skills/next/SKILL.md`: `--kind` takes a list; add
  the §8.8 tier default table verbatim — mechanical loops
  `--kind feature,bug,chore`, high-tier loops `--kind design,spike,review`
  — keyed off the session's model tier per `MODEL_SELECTION.md`.
- `plugins/claude/lode/skills/working-under-worklode/SKILL.md`: the §8
  fixer loop as executor instructions — on a plan gap: record it
  (`lode task gap`), spawn the fixer at the tier the fix requires,
  `lode task fix --phase started/finished` around it; outcome `resolved` →
  continue, `substantive` → stop, `escalated` → `lode task escalate`. State
  §8.3's thumb on the scale in its own words: **"Uncertain counts as
  substantive."**
- Run the marketplace/Codex mirror sync the `worklode-lode-plugin` skill
  prescribes, and tick `docs/agent-surfaces.md`'s checklist for the new
  `lode task escalate|gap|fix` verbs and the `--kind` list form.

- [ ] `go test -trimpath ./internal/store -run 'ClaimNext|ReadyCandidates' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/api ./internal/cmd -run 'Kind' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestAgentSurfaces -count=1` — `ok`.
- [ ] Commit: `claim --next --kind takes a list; ladder + tier-default skills (025 §8, §8.8)`.

### Task 10 — e2e: the ladder end to end

```yaml
kind: chore
priority: medium
skills:
  - superpowers:verification-before-completion
blockedBy: [4, 5, 6, 7, 8, 9]
```

One journey in `e2e/`, public surfaces only, extending the doc-lifecycle
e2e the sibling plan leaves behind:

1. Accept a spec and a covering plan (author alice); claim a minted task.
2. `lode task gap`, `lode task fix --phase started`, `--phase finished
   --outcome escalated`, then `lode task escalate --to plan --section sec-2
   --reason "..."` — the design task exists assigned to alice, the
   escalated task shows blocked in `lode show`, and `lode event tail` shows
   `task.gap_found`, `fix.started`, `fix.finished` in order.
3. A second worker escalates the same section — joined, no second design
   task.
4. Patch the spec (sibling's `lode doc edit` path) on a section a second,
   unexecuted plan covers — that plan reads `stale` in `lode doc list`, the
   re-plan task appears, and claiming its minted task prints the stale
   warning.
5. Server started with `LODE_DOC_STALENESS_DAYS=0`-adjacent short threshold
   (or the test clock the e2e harness provides): the sweeper emits
   `doc.stale` for an untouched accepted plan once, the groom task appears,
   a second sweep adds nothing.
6. `lode doc withdraw` the groomed doc; `lode doc list --unresolved` no
   longer names it.
7. A `--kind design,review` claim loop picks up the escalation task; a
   `--kind feature,bug,chore` loop never does.

- [ ] `make test-e2e` with `TEST_POSTGRES_DSN` reachable — green.
- [ ] `make test` — whole tree green.
- [ ] Commit: `e2e: escalation ladder, stale plans, grooming, tier routing (025 §8)`.

## Verification

- `lode doc todo WL-SPEC-25 --json` reports no `unplanned` anchors once all
  four parts exist.
- The §15.5 funnel is queryable: `lode event tail` shows
  `task.gap_found` → `fix.started` → `fix.finished{outcome}` →
  (`doc.patched` | escalation) for a worked example.
- `lode doc list --unresolved --older-than 30d` answers §8.7's "age of the
  unresolved set" question from the store alone.
