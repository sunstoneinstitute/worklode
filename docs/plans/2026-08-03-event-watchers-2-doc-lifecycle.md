---
status: draft
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-15.4
  - docs/specs/025-documents-in-the-backbone.md#sec-18
  - docs/specs/025-documents-in-the-backbone.md#sec-15.6
  - docs/specs/025-documents-in-the-backbone.md#sec-15.7
requires:
  - 2026-08-03-event-watchers-1-ordered-log.md
  - 2026-08-03-documents-in-the-backbone-3-plan-acceptance.md
---
# Event watchers 2/2: the doc-lifecycle subscriber

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 2 — see
`2026-08-03-event-watchers-1-ordered-log.md` for the series map. Task
numbers restart at 1. **Both `requires` edges must be merged first**: part
1 supplies `internal/eventbus` and the ordered log; documents-in-the-backbone
part 3 (transitively parts 1–2) supplies the `docs` rows, the editorial
lifecycle with its API, the `design` task kind, and the `lode doc` command
group this part's `submit` verb joins. Until those exist there is no
`wl:DocumentAccepted` to emit (spec 025 §19).

**Goal:** The first subscriber: `doc-lifecycle` mints a `review` task when
a document is submitted and a `design` (planning) task when a spec is
accepted, with both §5 suppression guards, both idempotency layers, the
`worklode_watcher_actions_total` metric, and §7's rule that a planning
session claims its design task so its tokens bill to it.

**Architecture:** The document lifecycle handlers switch from dotted
`doc.*` events with random external ids to part 1's typed emission
(`wl:DocumentSubmitted` / `wl:DocumentAccepted`, deterministic
`<type>:<subject>:<version>` ids), still inside the same `RecordEvent`-shaped
transaction as the change. A new `tasks.about_doc` column is the queryable
"this task references that document" edge both §5 guards need.
`internal/watcher` holds the two rules as a pure function
(`Evaluate(Input) []Action` — no store handle, no HTTP); the executor that
feeds it lives in `internal/api` and is started by `api.NewServer` off
`cfg.BackgroundCtx`, the same pattern as the boot-time skill sync — which
is also what lets e2e drive it through `httptest` with no `lode serve`
process. Watcher actions write through `RecordEvent` with external id
`doc-lifecycle:<rule>:<event-id>`, so the `(source, external_id)` unique
constraint **is** the `(event_id, subscriber)` idempotency key of §5 — no
new table.

**Tech Stack:** Go 1.25+, Postgres (golang-migrate), cobra,
prometheus/client_golang.

**Read first:**
- `docs/specs/025-documents-in-the-backbone.md` §5, §7, §9, §11 — rules, guards,
  billing, tests
- `docs/specs/025-documents-in-the-backbone.md` §3 — why submission is an
  event and not a status
- `internal/store/docs.go` (exists after documents-in-the-backbone part 2)
  — the `Doc` row, accept path
- `internal/api/docs.go` — the lifecycle handlers whose event emission this
  plan retypes
- `internal/api/tasks.go:120-160` — `createTask`'s
  `RecordEvent` + `store.CreateTask` + `LogChange` shape, which the
  executor mirrors
- `internal/eventbus/loop.go`, `emit.go` (part 1) — `Handler`, `Outcome`,
  `Emit`
- `internal/api/server.go` — where `NewServer` starts background work off
  `cfg.BackgroundCtx` (skill sync precedent)

**Conventions:**
- `go test ./internal/... -count=1`; store/API tests need Postgres with
  pgvector (`TEST_POSTGRES_DSN` overrides the DSN).
- Migration number `0014` is provisional — run
  `./scripts/check-migrations.sh --no-fix`, use what it assigns, and list
  the pair in `deploy/base/kustomization.yaml`.
- Commit after every task, imperative mood, no trailers.
- Task 7 works in the **claude-plugins repo**
  (`~/git/sunstone/claude-plugins`), branch + PR, not in worklode.

---

## File structure

| File | Responsibility |
|---|---|
| `deploy/base/migrations/0014_task_doc_ref.{up,down}.sql` (new) | `tasks.about_doc` |
| `deploy/base/kustomization.yaml` | list the pair |
| `internal/store/tasks.go` (+ tests) | `AboutDoc` on `Task`/`TaskInput`, guard query `OpenTaskForDoc` |
| `internal/store/docs.go` (+ tests) | `DocIRI`, `DocBySubjectIRI` |
| `internal/api/tasks.go` | `about_doc` in task JSON |
| `internal/api/docs.go` (+ tests) | typed accept emission; `POST /api/v1/docs/{id}/submit` |
| `internal/api/docwatch.go` (+ `docwatch_test.go`) (new) | the executor: parse event, fetch guard facts, run actions; loop wiring in `NewServer` |
| `internal/watcher/doclifecycle.go` (+ test) (new) | `Evaluate(Input) []Action`, the two §5 rules, pure |
| `internal/watcher/metrics.go` (+ test) (new) | `worklode_watcher_actions_total{rule,outcome}` |
| `internal/cmd/doc.go`, `internal/cli/client.go` | `lode doc submit` |
| `e2e/doc_lifecycle_test.go` (new) | §9's e2e: submit/accept via HTTP only |
| claude-plugins `plugins/lode/skills/…` | §7: the planning skill claims its task |

---

## Tasks

### Task 1 — Migration: `tasks.about_doc`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Spec 025 §15.4 mints tasks "referencing the document" and both guards are
"queries over open tasks referencing the document", but no such reference
exists: `tasks.plan_doc` means "minted by this plan's acceptance"
(025 §9.2), which is a different edge. This column is the resolution — the
document a `review`/`design` task is *about*. (§7's "no schema change"
covers only §7; the spec's migration list simply missed this edge.)

**Files:**
- Create: `deploy/base/migrations/0014_task_doc_ref.up.sql`, `.down.sql`
- Modify: `deploy/base/kustomization.yaml`

- [ ] **Step 1: Write the migration**

`0014_task_doc_ref.up.sql`:

```sql
-- The document this task is about (spec 025 §15.4): set on review tasks
-- minted at submission and design tasks minted at acceptance. Distinct
-- from plan_doc (the plan whose acceptance minted the task, 025 §9.2).
-- The §5 suppression guards are partial-index-backed queries over open
-- tasks carrying this reference — queries, not stored state (025 §1).
ALTER TABLE tasks ADD COLUMN about_doc bigint REFERENCES docs(id);
CREATE INDEX tasks_about_doc ON tasks (about_doc) WHERE about_doc IS NOT NULL;
```

`0014_task_doc_ref.down.sql`:

```sql
DROP INDEX tasks_about_doc;
ALTER TABLE tasks DROP COLUMN about_doc;
```

- [ ] **Step 2: List both files in `deploy/base/kustomization.yaml`, then verify and commit**

```bash
./scripts/check-migrations.sh --no-fix
go test ./internal/store -run TestCreateTask -count=1
git add deploy/base && git commit -m "Add tasks.about_doc for document-referencing tasks"
```

---

### Task 2 — Store and API: the doc reference and the guard query

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/store/tasks.go`, `internal/store/tasks_test.go`,
  `internal/store/docs.go`, `internal/store/docs_test.go`,
  `internal/api/tasks.go`, `internal/api/tasks_test.go`

- [ ] **Step 1: Failing store tests**

- `TestCreateTaskAboutDoc` — create a doc row, then a task with
  `TaskInput{..., Kind: "review", AboutDoc: docID}`; read it back, assert
  `AboutDoc`.
- `TestOpenTaskForDoc` — with one open `review` task about the doc:
  `OpenTaskForDoc(ctx, docID, "review")` returns its id; after moving the
  task to `abandoned`, returns none; a `design` task about the same doc
  does not satisfy the `review` query.
- `TestDocIRIRoundTrip` — `DocIRI` on a spec row numbered 25 returns
  `wlid:doc/spec-25`; on an ADR, `wlid:doc/adr-<n>`; on a plan (no
  number), `wlid:doc/plan/<slug>`; `DocBySubjectIRI` inverts all three.

- [ ] **Step 2: Implement**

`tasks.go`: add `AboutDoc sql.NullInt64` to `Task` (JSON `about_doc`,
omitted when null), `AboutDoc int64` to `TaskInput` (0 = none), append
`about_doc` to `taskColumns` and the `CreateTask` INSERT (the `skills`
column precedent shows where). Guard query:

```go
// OpenTaskForDoc returns the id of an open task of the given kind that
// references doc, or "" — the §5 suppression guard, computed rather than
// stored (025 §1). Open means state NOT IN closedStates.
func (s *Store) OpenTaskForDoc(ctx context.Context, docID int64, kind string) (string, error)
```

```sql
SELECT id FROM tasks
 WHERE about_doc = $1 AND kind = $2 AND state NOT IN ('merged','deployed_dev','deployed_prod','released','abandoned')
 ORDER BY created_at LIMIT 1
```

(reuse the `closedStates` const rather than inlining the tuple).

`docs.go`:

```go
// DocIRI is the document's subject IRI in event payloads (025 §15.2's
// wlid:doc/spec-025 form): wlid:doc/<kind>-<number> for numbered kinds,
// wlid:doc/plan/<slug> for plans.
func DocIRI(d Doc) string
// DocBySubjectIRI resolves an event's wl:subject back to the row.
func (s *Store) DocBySubjectIRI(ctx context.Context, iri string) (Doc, error)
```

`internal/api/tasks.go`: surface `about_doc` in task JSON (get + list),
and accept an optional `about_doc` filter on `GET /api/v1/tasks`
(`TaskFilter.AboutDoc int64`) — the e2e test polls with it.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/store ./internal/api -count=1
git commit -am "Add about_doc task references and the open-task guard query"
```

---

### Task 3 — Typed document events and `lode doc submit`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/api/docs.go`, `internal/api/docs_test.go`,
  `internal/api/server.go` (one route), `internal/cli/client.go`,
  `internal/cmd/doc.go`

- [ ] **Step 1: Failing API tests**

- `TestAcceptDocEmitsTypedEvent` — accept a spec via
  `POST /api/v1/docs/{id}/accept`; query the events API (part 1's
  `GET /api/v1/events?type=wl:DocumentAccepted`) and assert one event with
  `external_id = "wl:DocumentAccepted:" + DocIRI + ":" + version`, payload
  carrying `wl:subject`, `wl:fromStatus: "wlc:draft"`,
  `wl:toStatus: "wlc:accepted"`. Accepting again (or retrying the request)
  adds no second event.
- `TestSubmitDoc` — `POST /api/v1/docs/{id}/submit` on a draft → 200,
  emits `wl:DocumentSubmitted`, and **no document column changes**
  (fetch the doc before/after, compare status + updated_at). On a
  nonexistent id → 404. A second submit of the same version → 200,
  `inserted=false` at the log (same deterministic id — idempotent before
  the guard even runs).

- [ ] **Step 2: Implement**

In `internal/api/docs.go`:

- **Accept**: replace the dotted `doc.accepted` + `randomExternalID`
  `RecordEvent` call (documents-in-the-backbone part 2, Task 5 wrote it)
  with part 1's typed seam:

  ```go
  ev := eventbus.DocumentAccepted{
      Doc: store.DocIRI(doc), Actor: actor.ID, At: s.st.Now(),
      Version: doc.Version, From: "wlc:draft", To: "wlc:accepted",
  }
  _, _, err = eventbus.Emit(r.Context(), s.st, "cli", ev, applyAccept)
  ```

  where `applyAccept` is the existing accept-transaction body, unchanged.
  Leave the other `doc.*` events (created/updated/revised) dotted — 027
  types only the two events its subscriber consumes; retyping the rest is
  its own decision, out of scope.
- **Submit**: new handler + route
  `mux.Handle("POST /api/v1/docs/{id}/submit", s.auth(s.submitDoc))` —
  any authenticated actor; it emits `eventbus.DocumentSubmitted{...}` with
  a nil apply (the event *is* the whole change; 025 §7's "submission is an
  event, not a status") and returns the doc JSON.

CLI: `SubmitDoc(ctx, id)` client method; `lode doc submit <id>` subcommand
on the existing `doc` command group (documents-in-the-backbone part 3
created `internal/cmd/doc.go`; if it is somehow absent, create the group
with just `submit`).

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/api ./internal/cli ./internal/cmd -count=1
git commit -am "Emit typed document events and add lode doc submit"
```

---

### Task 4 — The rules, pure: `internal/watcher`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/watcher/doclifecycle.go`,
  `internal/watcher/doclifecycle_test.go`, `internal/watcher/metrics.go`,
  `internal/watcher/metrics_test.go`

025 §19: the package takes an event and returns actions, **no store handle
and no HTTP** — the executor (Task 5) fetches the facts and performs the
actions. Everything here is table-testable without Postgres.

- [ ] **Step 1: Failing tests, the §5 truth table**

```go
func TestEvaluate(t *testing.T) {
	cases := []struct {
		name string
		in   watcher.Input
		want []watcher.Action
	}{
		{"submit mints review", ...},
		{"submit with open review task suppressed", ...},
		{"accept of spec mints design", ...},
		{"accept of spec with open design task suppressed with note", ...},
		{"accept of plan mints nothing", ...},   // 025 §9.2 owns plan acceptance; 025 §22
		{"accept of adr mints nothing", ...},    // §5: "where the document is a spec"
		{"vendor event ignored", ...},           // dotted type → nil actions
		{"unknown wl: type ignored", ...},
	}
	...
}
```

- [ ] **Step 2: Implement `doclifecycle.go`**

```go
// Input is everything the two rules of spec 025 §15.4 may consult. The
// executor fills it; Evaluate never touches the store, so the rules are a
// pure function (025 §19).
type Input struct {
	EventID   int64
	EventType string // events.type: a wl: curie or a vendor dotted type
	DocID     int64
	DocIRI    string
	DocKind   string // spec | adr | plan
	DocTitle  string
	Project   string
	// Open task of the relevant kind already referencing the doc; "" = none.
	OpenReviewTask string
	OpenDesignTask string
}

// Action is one consequence for the executor to perform.
type Action struct {
	Rule       string // "review-on-submit" | "plan-on-accept" — the metric label
	Suppressed bool   // guard hit: perform no mint
	NoteTask   string // when suppressed on accept: note the absorbed event here (§5)
	// Mint parameters (Suppressed == false):
	TaskKind  string // "review" | "design"
	Title     string
	Body      string
}

// Evaluate applies the two hardcoded rules of 025 §15.4. Rules must never
// emit an event this subscriber consumes (no cascades — a rule, reviewed
// here, not a mechanism; §5).
func Evaluate(in Input) []Action
```

Behaviour, exactly:

- `wl:DocumentSubmitted` → rule `review-on-submit`. Open review task →
  one `Action{Suppressed: true}` (no note — the log's dedup usually
  absorbs a same-version resubmit before this guard runs). Else mint:
  `TaskKind: "review"`, `Title: "Review: " + DocTitle`, body naming the
  doc IRI, its version, `prov:wasInformedBy wlid:event/<EventID>`, and
  that closing this task is the review outcome while acceptance stays
  `lode doc accept`.
- `wl:DocumentAccepted` where `DocKind == "spec"` → rule
  `plan-on-accept`. Open design task → `Action{Suppressed: true,
  NoteTask: OpenDesignTask}`. Else mint: `TaskKind: "design"`,
  `Title: "Plan: decompose " + DocTitle + " into plans"`, body carrying
  §5's charge verbatim — *decide how to decompose this spec into plans,
  and write them* — the provenance line, and §7's instruction to claim
  this task before writing so the planning cost bills to it.
- Anything else (plan/adr acceptance, vendor types, unknown curies) →
  nil.

- [ ] **Step 3: `metrics.go`** — copy `internal/hooks/metrics.go`'s
  nil-safe shape:

```go
// worklode_watcher_actions_total{rule, outcome}, outcome ∈ applied|suppressed|error (spec 025 §15.7).
```

with a `testutil` assertion in `metrics_test.go`.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/watcher -count=1
git commit -am "Add the pure doc-lifecycle rules and watcher metric"
```

---

### Task 5 — The executor and the wired subscriber

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 4]
```

**Files:**
- Create: `internal/api/docwatch.go`, `internal/api/docwatch_test.go`
- Modify: `internal/api/server.go`, `internal/cmd/serve.go` (nothing but a
  comment — see step 3)

- [ ] **Step 1: Failing tests** (`docwatch_test.go`, store-backed; call the
  handler directly rather than running the loop, so no timing)

- `TestDocWatchMintsReviewOnSubmit` — emit `wl:DocumentSubmitted` for a
  draft spec, run the handler on the event row: one `review` task exists,
  state `ready`, project = the doc's, `about_doc` set; handler returned
  `OutcomeApplied`.
- `TestDocWatchRedeliveryMintsOnce` — run the handler on the **same**
  event row twice: still one task (§5 idempotency layer 1 — the second
  run's `RecordEvent` hits the `(source, external_id)` conflict).
- `TestDocWatchSuppressionCycle` — §9's full cycle: accept → design task
  minted; emit a second acceptance (version 2) while it is open → handler
  returns `OutcomeSuppressed`, no new task, and the open task's timeline
  (`StateLogForEntity`) gained the absorbed-acceptance note; abandon the
  task; emit a third acceptance → a fresh design task (layer 2 — the
  guard, correct because sections accepted since the last plan need
  planning).
- `TestDocWatchIgnoresVendorEvents` — a `push` event flows through with
  `OutcomeApplied` and no task minted (§9: not treated as RDF).

- [ ] **Step 2: Implement `docwatch.go`**

```go
// docLifecycleHandler is the doc-lifecycle subscriber (spec 025 §15.4): it
// parses the event, fetches the guard facts, lets the pure rules decide
// (internal/watcher), and performs the actions. Every mint goes through
// RecordEvent with external id "doc-lifecycle:<rule>:<event-id>" — the
// (source, external_id) unique constraint is the (event_id, subscriber)
// idempotency key, and the action event's payload carries
// prov:wasInformedBy back to the triggering event, so the provenance
// chain webhook→event→task holds with no new table.
func (s *Server) docLifecycleHandler() eventbus.Handler {
	return func(ctx context.Context, ev store.Event) (eventbus.Outcome, error) {
		if ev.Type != eventbus.TypeDocumentSubmitted && ev.Type != eventbus.TypeDocumentAccepted {
			return eventbus.OutcomeApplied, nil // vendor/webhook population: pass through untouched
		}
		// payload["wl:subject"] → store.DocBySubjectIRI → doc row
		// store.OpenTaskForDoc(doc.ID, "review"/"design") → watcher.Input
		// for each watcher.Evaluate action:
		//   suppressed with NoteTask → RecordEvent(source "watcher",
		//     extID "doc-lifecycle:note:<event-id>", type "task.updated",
		//     apply: store.LogChange on the task {"absorbed_event": ev.ID, "type": ev.Type})
		//   mint → RecordEvent(source "watcher",
		//     extID "doc-lifecycle:<rule>:<event-id>", type "task.created",
		//     payload {"rule": ..., "prov:wasInformedBy": "wlid:event/<ev.ID>"},
		//     apply: store.CreateTask(tx, now, TaskInput{Project, Title, Body,
		//       Kind, AboutDoc: doc.ID, CreatedBy: "watcher", Priority: "medium"})
		//       + store.LogChange(...))
		// metrics: s.watcherMetrics.action(rule, outcome) per action
		// outcome: any suppressed action → OutcomeSuppressed, else OutcomeApplied;
		// any error → return it (the loop acks the prefix and redelivers).
	}
}
```

A `wl:subject` that resolves to no doc row is an error (redelivery will
retry; if the doc was deleted the operator uses `lode event seek` — that
is what the verb is for). `CreatedBy: "watcher"` requires a `watcher`
actor to exist; create it idempotently at wiring time
(`st.CreateActor` with `ON CONFLICT DO NOTHING` semantics — check
`store.CreateActor`'s behaviour and mirror how the bootstrap admin is
ensured in `server.go`).

- [ ] **Step 3: Wire it in `NewServer`**

In `internal/api/server.go`, after the skill-sync startup block:

```go
if cfg.BackgroundCtx != nil {
	if err := st.EnsureEventSubscriber(context.Background(), "doc-lifecycle"); err != nil { ... }
	busMetrics := eventbus.NewMetrics(reg, st) // first registration: part 1 wired no subscriber
	s.watcherMetrics = watcher.NewMetrics(reg)
	go eventbus.Run(cfg.BackgroundCtx, eventbus.Options{
		Store: st, Name: "doc-lifecycle", Handler: s.docLifecycleHandler(),
		Poll: cfg.EventPoll, Metrics: busMetrics, Log: s.log,
	})
}
```

Add `EventPoll time.Duration` to `api.Config` (zero → the loop's 1 s
default) so e2e can poll fast. `serve.go` already passes `BackgroundCtx`,
so production wiring is free — add a one-line comment there pointing at
`NewServer` for the subscriber. Gate on `BackgroundCtx != nil` keeps every
existing test (which passes none) loop-free.

- [ ] **Step 4: Verify and commit**

```bash
go test ./internal/api -count=1 -race
git commit -am "Wire the doc-lifecycle subscriber with both idempotency layers"
```

---

### Task 6 — e2e: the lifecycle through public surfaces only

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

**Files:**
- Create: `e2e/doc_lifecycle_test.go`

- [ ] **Step 1: Write the test** (§9's last bullet; HTTP only, no store
  writes — the `e2e/` contract)

Boot as `smoke_test.go` does, but pass
`api.Config{BackgroundCtx: ctx, EventPoll: 50 * time.Millisecond, ...}`
with a cancelled-on-cleanup context. Then, entirely through `cli.Client` /
raw HTTP:

1. Create a project and a spec doc (`POST /api/v1/docs`, two sections).
2. `POST /docs/{id}/submit`. Poll `GET /api/v1/tasks?about_doc=<id>`
   (≤ 10 s, 100 ms interval) until one `review` task, state `ready`.
3. Submit again: task count stays 1 (guard + log dedup).
4. Close the review task (abandon via the tasks API), accept the doc as
   its assignee. Poll until one `design` task appears, `ready`, in the
   doc's project.
5. Assert the design task's timeline (`GET /api/v1/tasks/{id}/timeline`)
   reaches the watcher event — the `prov:wasInformedBy` chain of §5.
6. `GET /api/v1/event-subscribers` shows `doc-lifecycle` with lag 0 and a
   nonzero holder.

- [ ] **Step 2: Verify and commit**

```bash
go test -race -count=1 -tags e2e ./e2e/ -run TestDocLifecycleWatcher
git commit -am "Prove the doc-lifecycle watcher end to end over HTTP"
```

---

### Task 7 — §7: the planning skill claims its task (claude-plugins repo)

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files (in `~/git/sunstone/claude-plugins`, branch + PR):**
- Modify or create: the lode plugin's planning/decomposition skill

**Reality check first:** no `plugins/lode/` exists in claude-plugins today,
and 025 §18's guided-flow skills (authoring, review, decomposition) are
future work of 025's orbit. Do not build them here. The §7 deliverable is
one rule wherever the planning ceremony lives at execution time:

- If a lode planning/decomposition skill exists by then: add, as its
  **first step**, `lode task claim <design-task-id>` into
  `wt/<task-id>-<slug>` before anything is read or written, with a
  sentence of why (tokens bill through
  `agent_sessions → leases → tasks`, 012 §4; pre-claim exploration stays
  unattributed by design).
- If none exists: create the minimal skill —
  `plugins/lode/skills/plan-spec/SKILL.md` (mirror an existing plugin's
  manifest conventions; register in the marketplace file as its
  neighbours do) whose body is: claim the design task first, then follow
  `superpowers:writing-plans` against the accepted spec, then
  `lode task done` when the plan document is accepted.

Either way, verify AC9 manually once: run a planning session under the
claim, then `lode task cost <design-task>` reports its tokens.

- [ ] **Step 1: Implement in claude-plugins, PR titled
  "Planning skill claims its design task (worklode spec 025 §15.6)"**
- [ ] **Step 2: In worklode, nothing — this task's artifact is the PR.**

---

### Task 8 — Docs: CLAUDE.md and the indexes

```yaml
kind: chore
priority: low
blockedBy: [5]
```

**Files:**
- Modify: `CLAUDE.md`, `docs/follow-ups.md`

- [ ] **Step 1: CLAUDE.md** — in "Specs, plans, tasks", one sentence:
  submitting a document (`lode doc submit`) and accepting a spec mint the
  review/planning tasks via the doc-lifecycle watcher (spec 025 §15.4);
  minting the prompt is not performing the act (025 §7).
- [ ] **Step 2: `docs/follow-ups.md`** — record the two known
  non-blocking gaps this series leaves: (a) `internal/eventbus/vocab.go`
  is hand-mirrored until 025 §17's codegen owns it (drift test in place);
  (b) a poison event head-of-line-blocks its subscriber by design — no
  DLQ until a real case appears (025 §22).
- [ ] **Step 3: Verify and commit**

```bash
./scripts/secfmt.py -l && ./scripts/secindex.py
git add -A && git commit -m "Document the doc-lifecycle watcher and its follow-ups"
```

---

## Done when (maps to 025 §24)

- AC4: submit mints one `ready` review task and changes no document
  column; a second submit while it is open mints nothing (Task 3 + 5 + 6).
- AC5: the accept → mint → suppress-and-note → close → fresh-mint cycle,
  with `prov:wasInformedBy` reachable from the task (Task 5, Task 6 step 5).
- AC8's watcher half: `worklode_watcher_actions_total` registered and
  tested (Task 4).
- AC9: a claimed planning session's tokens answer to
  `lode task cost <design-task>` (Task 7, manual verification recorded in
  the PR).
- §9's remaining bullets: redelivery-mints-once, vendor passthrough, e2e
  via HTTP only (Tasks 5–6).
