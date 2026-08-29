---
status: draft
covers:
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-10
    coverage: partial
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-10.1
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-24
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-doc-accept-gate-and-amendment.md
      - docs/plans/2026-08-29-escalation-and-grooming.md
      - docs/plans/2026-08-29-doc-version-graphs.md
---
# Decision-kind tasks

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** `decision` ships as the seventh `tasks.kind` (025 §10): a posed
question whose deliverable is a recorded answer in a `task_decisions` side
table (025 §10.1), never a document or a diff. A decision is never claimable
into a worktree — it is excluded from `readyCandidates` and refused by the
claim transaction (004 §6.3, as amended by 025 §10) — and is worked through
the existing lease-free assign/start path instead. Recording the answer,
stamping `decided_at`, and closing the task are one transaction.

Part 1 of a four-part series planning spec 025's remaining gap; the other
parts (`doc-accept-gate-and-amendment-plan`, `escalation-and-grooming-plan`,
`doc-version-graphs-plan`) are independent — no `blockedBy` edge exists in
either direction, and nothing here waits on them.

**Read first:**
- `docs/specs/inlined/025-documents-in-the-backbone.md` §10, §10.1 — the kind
  semantics and the `task_decisions` DDL this plan quotes
- `docs/specs/inlined/004-execution-backbone.md` §6.3 — the amended
  never-claimable rule
- `deploy/base/migrations/0025_rename_spec_kind_to_design.up.sql` — the
  drop/re-add pattern for `tasks_kind_check`
- `internal/store/assign.go` — `AssignTask`/`StartTask`: the lease-free
  ownership rules the answer path mirrors
- `internal/store/tasks.go:100` — `legalTransitions`: `ready → merged` and
  `in_progress → merged` are already legal ("direct-to-main jumps"), so
  closing a decision needs no state-machine change
- `internal/store/instructions.go` — `EnqueueInstruction`: the
  `RecordEvent`-wrapped side-table write this feature's mutations copy
- `internal/api/tasks.go:64` — `createTask`: the one create path pose extends
- `internal/api/tasks_test.go:931` — `TestTaskKindsAgreeAcrossSources`
  creates one task per `ns.TaskKinds` entry via `POST /api/v1/tasks` and
  expects 201, which is why pose extends that route instead of adding a
  kind-refusing sibling

## Global constraints

- **The seven kinds, exactly** (025 §10; AC 1):

  ```sql
  CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'))
  ```

  `ns/concept.ttl`, the regenerated `internal/ns/gen.go`, and the CHECK move
  in one commit; `TestTaskKindsAgreeAcrossSources` (api),
  `TestKindCheckConstraintMatchesGeneratedKinds` (store), and
  `TestTaskKindsMatchTurtle` (ns) hold them together and must never see a
  commit where they disagree.
- **The exclusion is a kind predicate, not an edge predicate** (004 §6.3):
  `AND t.kind <> 'decision'` in `readyCandidates`, plus the same refusal in
  the claim transaction beside the existing container guard — a decision has
  no child edge to detect it by.
- **The answer JSON vocabulary** is exactly §10.1's: `answer` holds
  `{picked: [...], notes, freetext}`, and a `yes_no` answer is
  `{"value": "yes" | "no" | "unsure"}`. Recording is terminal: an answered
  decision is never re-answered; deciding again is another decision task.
- **Feature-named vertical** (CLAUDE.md): `model/decision.go`,
  `store/decisions.go`, `api/decisions.go`, `cli/decisions.go`,
  `cmd/decision.go`. Shapes live in `internal/model` only (ADR 036, wire
  names, `model/rule_test.go` enforces); rendering lives in `internal/cli`
  (`renderrule_test.go` enforces).
- Repo standing rules apply: the migration is a new numbered pair listed in
  `deploy/base/kustomization.yaml`, never an edit to a shipped one; store
  operations with meaningful outcomes extend `worklode_*` metrics with tests
  and bounded labels; store tests need Postgres with pgvector (a silently
  skipped run proved nothing); `e2e/` drives public surfaces only.
- **Every task leaves `make test` green and ends in its own commit.** New
  CLI commands regenerate the command reference in the same task
  (`go test -trimpath ./internal/cmd -run TestCommandReference -update-command-ref`).

## Tasks

### Task 1 — Migration 0058: the seventh kind and `task_decisions`

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
blockedBy: [ ]
```

New pair `deploy/base/migrations/0058_decision_kind.up.sql` /
`.down.sql`, listed in `deploy/base/kustomization.yaml` (number subject to
`./scripts/check-migrations.sh`, which renumbers on collision). Up, following
0025's drop/re-add pattern:

```sql
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));

-- 025 §10.1, verbatim: everything a decision needs beyond its own
-- title/body lives here, so no kind of task carries columns another
-- kind leaves null.
CREATE TABLE task_decisions (
    task_id          text PRIMARY KEY REFERENCES tasks(id),
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select', 'multi_select', 'single_select_notes',
                         'pick_or_freetext', 'yes_no', 'freetext')),
    options          jsonb,      -- [{label, description}], null for yes_no/freetext
    min_picks        int,        -- multi_select only
    max_picks        int,        -- multi_select only
    answer           jsonb,      -- {picked: [...], notes, freetext}; null until recorded
    decided_at       timestamptz
);
```

Down deletes `decision`-kind rows (a down is destructive by definition; the
FK means `task_decisions` goes first), drops the table, and restores the
six-kind CHECK, so the pair round-trips on any database (AC 1).

Same commit, atomically (025 §10's closing paragraph): add to
`ns/concept.ttl` under `wlc:TaskKind`, matching the existing entries' shape —

```turtle
wlc:decision a skos:Concept ; skos:inScheme wlc:TaskKind ; skos:prefLabel "decision" ;
    skos:definition "Answer a posed question and record the answer on the task; the deliverable is the recorded answer in task_decisions, never a document or a diff." .
```

— then `./scripts/nsgen.py` regenerates `internal/ns/gen.go`
(`TaskKinds` becomes the sorted
`["bug","chore","decision","design","feature","review","spike"]`), and
`riot --validate ns/*.ttl` if riot is installed. No `ns/shapes.ttl` edit:
its Task shape constrains by `skos:inScheme wlc:TaskKind`, not a literal
list.

No hand-written test is needed for agreement — the three guard tests named
in Global Constraints already pin all directions; this task's proof is that
they pass with the new kind present.

- [ ] Write the migration pair; add both files to `deploy/base/kustomization.yaml`
- [ ] `./scripts/check-migrations.sh --no-fix` — clean
- [ ] Edit `ns/concept.ttl`; run `./scripts/nsgen.py`
- [ ] `make test` — the three kind-agreement tests pass against real Postgres
- [ ] Commit

### Task 2 — Decision shapes and pure answer validation

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

New `internal/model/decision.go` (stdlib only, wire names, json tags):

```go
// Decision is the side row a decision-kind task carries (025 §10.1). The
// question is the task's own title and body; nothing here restates it.
type Decision struct {
	Task         string           `json:"task"`
	ResponseType string           `json:"response_type"`
	Options      []DecisionOption `json:"options,omitempty"`
	MinPicks     *int             `json:"min_picks,omitempty"`
	MaxPicks     *int             `json:"max_picks,omitempty"`
	Answer       *DecisionAnswer  `json:"answer,omitempty"`
	DecidedAt    *time.Time       `json:"decided_at,omitempty"`
}

type DecisionOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// DecisionAnswer is §10.1's answer JSON; Value is the yes_no third field
// ("yes" | "no" | "unsure").
type DecisionAnswer struct {
	Picked   []string `json:"picked,omitempty"`
	Notes    string   `json:"notes,omitempty"`
	Freetext string   `json:"freetext,omitempty"`
	Value    string   `json:"value,omitempty"`
}
```

New `internal/store/decisions.go` starts with two pure functions (no DB, no
transaction — later parts of this vertical call them):

- `ValidateDecisionSpec(d model.Decision) error` — pose-time shape:
  `response_type` in the six values; `options` non-empty for
  `single_select`, `multi_select`, `single_select_notes`,
  `pick_or_freetext` and absent for `yes_no`, `freetext`; option labels
  non-empty and unique; `min_picks`/`max_picks` only on `multi_select`,
  each ≥ 1, `min ≤ max ≤ len(options)` where set; `answer`/`decided_at`
  absent (the answer is never posed).
- `validateAnswer(d model.Decision, a model.DecisionAnswer) error` — per
  type: `single_select` picks exactly one offered label;
  `multi_select` picks distinct offered labels within
  `[min_picks, max_picks]` (defaults 1 and `len(options)`);
  `single_select_notes` picks one and requires non-empty `notes`;
  `pick_or_freetext` requires exactly one of a single pick or non-empty
  `freetext`; `yes_no` requires `value` in `yes|no|unsure` and nothing
  else; `freetext` requires non-empty `freetext` and nothing else. Fields a
  type does not use must be empty — an answer that smuggles extras is
  refused, so what is stored is exactly what the type defines.

First tests: table tests in `internal/store/decisions_test.go` over both
functions — one accepting and at least one refusing case per response type,
no Postgres required (they must run and pass with no DSN). Both return
`ErrInvalidInput`-wrapped errors naming the offending field. The answer
table starts:

```go
func TestValidateAnswer(t *testing.T) {
	opts := []model.DecisionOption{{Label: "a"}, {Label: "b"}, {Label: "c"}}
	two := 2
	cases := []struct {
		name    string
		spec    model.Decision
		answer  model.DecisionAnswer
		wantErr bool
	}{
		{"single_select picks one offered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"b"}}, false},
		{"single_select refuses an unoffered label",
			model.Decision{ResponseType: "single_select", Options: opts},
			model.DecisionAnswer{Picked: []string{"z"}}, true},
		{"multi_select within max_picks",
			model.Decision{ResponseType: "multi_select", Options: opts, MaxPicks: &two},
			model.DecisionAnswer{Picked: []string{"a", "c"}}, false},
		{"yes_no takes the third value",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "unsure"}, false},
		{"yes_no refuses smuggled freetext",
			model.Decision{ResponseType: "yes_no"},
			model.DecisionAnswer{Value: "yes", Freetext: "but"}, true},
		// ... one accepting + one refusing case per remaining type
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateAnswer(tc.spec, tc.answer)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateAnswer: %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error %v is not ErrInvalidInput", err)
			}
		})
	}
}
```

`internal/model/rule_test.go` and `deps_test.go` stay green.

- [ ] Write the table tests; watch them fail
- [ ] Implement `model/decision.go` and the two validators
- [ ] `go test -trimpath ./internal/model ./internal/store -run 'TestModelRule|TestDeps|Decision'`
- [ ] Commit

### Task 3 — Never claimable, never a container

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

The exclusion rule, all three doors (004 §6.3 as amended, AC 1):

- `internal/store/ranking.go` `readyCandidates`: beside the existing
  `child_of` container predicate, add

  ```sql
  AND t.kind <> 'decision'
  ```

  and extend the function comment — the worktree is the unit of Worklode
  work, a decision has nothing to check out, and the predicate is on the
  kind because no child edge exists to detect it by (004 §6.3).
- `internal/store/leases.go` claim transaction: beside the `hasChildren`
  container guard (`leases.go:178`), refuse `kind = 'decision'` with
  `ErrBadTransition` and a message pointing at `lode task assign` — a
  decision is never leased.
- `internal/store/hierarchy.go`: `checkHierarchy` and `Decompose` refuse a
  `decision`-kind **parent** (`ErrInvalidInput`). A decision closes by its
  recorded answer in one transaction; a parent closes by roll-up, and the
  two cannot both hold. A decision as a *child* stays legal — it is a leaf
  like any other.

Store tests (Postgres): a ready, unblocked, unleased decision task in a
project where `readyCandidates` offers a sibling feature task — the decision
is absent from the result; `Claim` on it returns `ErrBadTransition`;
`Decompose` on it and `AddEdge` making it a parent both refuse. Also the
lease-free path holds as-is: `AssignTask` + `StartTask` on a decision
succeed unchanged (no new code, just the pin).

- [ ] Write the exclusion tests; watch them fail
- [ ] Add the three guards
- [ ] `go test -trimpath ./internal/store -run 'Ready|Claim|Hierarchy|Decompose|Decision'`
- [ ] Commit

### Task 4 — Pose: create a decision through every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

Posing is task creation — one mutation across every surface in this task, so
the event provenance lands wired (the event stays `task.created`; the
decision block rides in its payload because the handler already records the
request as payload).

- **Model:** `model.CreateTaskInput` gains
  `Decision *Decision \`json:"decision,omitempty"\``.
- **Store:** `store.CreateDecision(tx, taskID string, d model.Decision) error`
  in `decisions.go` — runs `ValidateDecisionSpec`, inserts the
  `task_decisions` row. Called only inside the create transaction.
- **API** (`internal/api/tasks.go` createTask, plus new
  `internal/api/decisions.go` for the shared bits): when `req.Kind ==
  "decision"`, a nil `req.Decision` defaults to `{response_type:
  "freetext"}` — §10.1's question-plus-context needs nothing more, and this
  keeps `TestTaskKindsAgreeAcrossSources` green without special-casing the
  test. When `req.Kind != "decision"`, a non-nil `req.Decision` is a 422.
  Spec violations from `ValidateDecisionSpec` map to 422 naming the field.
- **Kind is fixed at pose:** the PATCH handler (`internal/api/lifecycle.go`,
  the existing `validKinds` gate) refuses changing kind **to or from**
  `decision` (422) — a retyped decision would strand or lack its side row.
- **Metric:** `decisions` CounterVec in `internal/store/metrics.go` —
  `worklode_decisions_total`, labels `op` (`pose`|`answer`), `outcome`
  (`ok`|`refused`); nil-safe like every sibling; incremented `op=pose` from
  the store write. Registration test beside the existing metrics tests.
- **CLI/cmd:** new `internal/cli/decisions.go` — `Client.CreateTask` already
  exists; add option-building helpers and `cli.DecisionRender(w, task,
  decision)` (tabwriter lives here, never in cmd). New
  `internal/cmd/decision.go`: `lode decision pose --title <question>
  [--project] [--body] [--type freetext] [--option "Label[:description]"]...
  [--min-picks N] [--max-picks N] [--priority]`, defaulting project from the
  existing scope helpers, posting through the one create path, rendering via
  the shared task confirmation.
- Regenerate the command reference (Global Constraints).

First tests: API handler tests — pose each response type and assert 201 plus
a `task_decisions` row via the GET added in Task 5's shape (until then, via
store read in the same package test); `kind: feature` with a decision block
is 422; PATCH kind onto/off a decision is 422; bare `kind: decision` gets
the freetext default. CLI test in `internal/cmd` per `task_test.go`'s
pattern for flag plumbing.

- [ ] Write handler tests; watch them fail
- [ ] Model field, `CreateDecision`, handler wiring, PATCH guard, metric
- [ ] `lode decision pose` + render; regenerate command reference
- [ ] `make test`
- [ ] Commit

### Task 5 — Read: `GET .../decision` and `lode decision show`

```yaml
kind: feature
priority: medium
skills: [ ]
blockedBy: [4]
```

The decider has to see the question, the offered options, and — afterwards —
the recorded answer.

- **Store:** `Store.GetDecision(ctx, taskID) (*model.Decision, error)` —
  scans the row, unmarshals `options`/`answer`; `ErrNotFound` for a task
  with no decision row.
- **API:** `GET /api/v1/tasks/{id}/decision` in `internal/api/decisions.go`,
  returning `model.Decision`; `routeGuards` entry with the same read
  permission as `GET /api/v1/tasks/{id}` (the router refuses to boot on an
  unnamed route, so the entry is not optional).
- **CLI/cmd:** `Client.GetDecision`; `lode decision show <id>` fetches task
  and decision, renders question (title), body, type, options,
  min/max picks, and the answer with `decided_at` via `cli.LocalTime` when
  recorded. `--json` prints the raw decision. Command reference regenerated.

Tests: handler test for 200/404 and answered/unanswered rendering of the
JSON; a render test over `DecisionRender` with and without an answer.

- [ ] Store reader + handler + guard entry
- [ ] `lode decision show` + render; regenerate command reference
- [ ] `make test`
- [ ] Commit

### Task 6 — Answer: record and close in one transaction, every surface

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

The record-answer mutation across store, event, metric, API, CLI (AC 1's
closing clause). **Store** — `Store.RecordDecision(ctx, taskID, actorID
string, a model.DecisionAnswer) (*model.Decision, error)` in
`decisions.go`, wrapped in `RecordEvent(ctx, "cli", extID, "task.decided",
payload{task, actor}, ...)` exactly as `EnqueueInstruction` is; inside the
transaction:

1. `lockTaskOwnership` (row lock), plus the kind read: not `decision` →
   `ErrInvalidInput`.
2. State must be `ready`, `in_progress`, or `in_review` (each has a legal
   `→ merged` edge); a delivered/abandoned state refuses.
3. Ownership mirrors `StartTask`: unassigned → the caller becomes assignee
   (with `LogChange`); assigned to someone else → refuse naming them (029
   §6.1 — the assignee is the accountable decider).
4. `IsBlocked` → refuse: a blocked decision is ordered behind work that has
   not closed.
5. `validateAnswer` (Task 2) against the stored spec → `ErrInvalidInput`
   naming the field.
6. `UPDATE task_decisions SET answer = $1, decided_at = $2 WHERE task_id =
   $3 AND answer IS NULL`; zero rows affected on an already-answered row →
   refuse ("recording is terminal; deciding again is another decision
   task").
7. `transitionKnown(..., state, "merged", ...)` — the record and the closure
   are the same act; there is no state in which a decision is answered but
   still open.

Metric `op=answer`, `outcome` `ok`/`refused`. **API:** `POST
/api/v1/tasks/{id}/decide` (the transition-verb shape of `/done`, `/start`),
`routeGuards` `guarded(permTaskWrite)`, body `model.DecisionAnswer`, returns
the updated `model.Decision`; store errors map 404/409/422 as the existing
transition handlers do. **CLI/cmd:** `Client.AnswerDecision`; `lode decision
answer <id> [--pick <label>]... [--notes <s>] [--text <s>] [--value
yes|no|unsure]`, flags mapping one-to-one onto the answer JSON; renders the
closed decision via `DecisionRender`. Command reference regenerated.

First tests, store (Postgres): one happy path per response type asserting
answer echoed, `decided_at` set, task state `merged`, and the `state_log`
row sharing the answer's event id (the one-transaction proof); refusals for
re-answer, wrong kind, assigned-to-other, blocked, abandoned, and an invalid
answer — each leaving both the row and the task untouched. The
one-transaction assertion, first:

```go
d, err := s.RecordDecision(t.Context(), task.ID, "actor-1",
	model.DecisionAnswer{Picked: []string{"a"}})
if err != nil {
	t.Fatalf("RecordDecision: %v", err)
}
if d.Answer == nil || d.DecidedAt == nil {
	t.Fatalf("answer/decided_at not recorded: %+v", d)
}
got, _ := s.GetTask(t.Context(), task.ID)
if got.State != "merged" {
	t.Fatalf("state = %q, want merged: the record and the closure are one act", got.State)
}
// The closure rode the task.decided event: state_log's transition row and
// the answer share an event id, or two transactions happened.
var n int
if err := s.db.QueryRow(`SELECT count(*) FROM state_log sl
	JOIN events e ON e.id = sl.event_id
	WHERE sl.task_id = $1 AND sl.to_state = 'merged'
	  AND e.type = 'task.decided'`, task.ID).Scan(&n); err != nil || n != 1 {
	t.Fatalf("merged transition rows on task.decided = %d (%v), want 1", n, err)
}
```

(Adjust column names to `state_log`'s actual schema when writing the test;
the assertion — one shared event id — is the contract.) Handler tests for
the status mapping; a cmd test for flag plumbing.

- [ ] Write store tests; watch them fail
- [ ] `RecordDecision` + metric; handler + guard; CLI verb + reference
- [ ] `make test`
- [ ] Commit

### Task 7 — e2e journey through public surfaces

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [6]
```

One journey in `e2e/`, `-tags e2e`, HTTP only (never a store write),
following the suite's existing style: create a project; pose a
`single_select` decision with two options via `POST /api/v1/tasks`; assert
`GET .../decision` shows the unanswered spec; assert the decision never
appears in the next/ready surface the suite already exercises and that a
claim attempt is refused; assign an actor; answer with an offered pick via
`POST .../decide`; assert the response carries the answer and `decided_at`,
the task reads `merged`, and a second answer is refused. Align docs in the
same task: `docs/agent-surfaces.md` gains the `lode decision` verbs per its
own checklist, and the regenerated command reference is committed if any
task above missed it.

- [ ] Write the journey; run `make test-e2e` against the compose stack
- [ ] `docs/agent-surfaces.md` updated per its checklist
- [ ] `make test` and `make test-e2e` green
- [ ] Commit
