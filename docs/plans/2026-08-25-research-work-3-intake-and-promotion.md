---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-1
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-2
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-8.1
    coverage: full
  - spec: docs/specs/032-project-cockpit.md#sec-3
    coverage: partial
  - spec: docs/specs/032-project-cockpit.md#sec-5
    coverage: full
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
blockedBy:
  - 2026-08-25-research-work-1-milestones.md
  - 2026-08-25-research-work-2-identifiers-and-references.md
  - 2026-08-25-approvals-2-flows-and-requirements.md
---

# Research work part 3 — intake, promotion, and the lifecycle modes

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ideas enter a standing intake project at Discovery and stay one
system of record from the first pitch to promotion or a kill (029 §8.1). The
two Selection decisions and the two Editorial Evaluation decisions are
governed, separately recorded facts; passing Editorial Evaluation runs one
promotion transaction — project, labels, `seeded_by` edge, default milestones
and deliverables, approval-flow snapshot, initial Crew, intake task closed —
with no second "Create project" confirmation, and redirects to the created
project's Approved launch cockpit (032 §5). `modeFactsForProject`
(`internal/api/cockpit.go:65`) stops returning `modeFacts{}` unconditionally:
all three mode facts derive from governed rows, which makes 032 §3's
Editorial decision → Approved launch → Operations transitions real. The
derived Sunstone stage becomes a query over governed decisions, milestones,
deliverables and work (029 §1) — never an editable status column.

**The one scoping call that shapes everything here** (CTO, 2026-08-25): the
dossier is a **query, not a stored entity**. An idea remains a task.
Selection's dossier — litmus-test results, claims, sources, unknowns,
hypothesis changes, recommendation, the exact AI run — is assembled by
reading facts already recorded against that intake task and its events, in
the same spirit as 025 §1's "groupings are queries, not rows". There is no
versioned `dossiers` table in this plan and none is ever added for it. A
fact that genuinely has nowhere to live yet is named in the closing Deferred
section instead of getting an invented schema.

**Series:** part 3 of the research-work series (spec 029). Part 1 owns
milestones (§2's tables); part 2 owns identifiers and the `entity_edges`
table (§4, §5); approvals part 2 owns `projects.approval_flow` and widening
`approvals.entity_kind` beyond `'pr'` (§7.2). This plan consumes all three,
which is what the `blockedBy:` frontmatter declares. Deliverable state (§8.3
sources, §3.2) is part 4; the Morning Brief (§8.2) is part 5; crew lifecycle
and chat spaces (§6, §8.4) are parts 6–7.

**Coverage gaps, declared.** 029 §2 is `partial`: this plan mints the
default `kind=sunstone-story` shape at promotion and snapshots the flow;
part 1 owns the milestone entity itself. 032 §3 is `partial`: the three
mode transitions and the derived stage land here, but the decision rail's
evidence-backed stage recommendations are limited to a closure hint, and
the close act itself (project close, Crew close) belongs to the crew
lifecycle part. 032 §10 and §11 bind this plan (narrow-width primary
decisions; e2e through public surfaces only) while being implemented by
none of it — new pages reuse the shell and stylesheet rules the earlier
cockpit plans already audited.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, Prometheus client. Store and `internal/api` tests
need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §1, §2, §8.1
- `docs/specs/inlined/032-project-cockpit.md` §3, §5
- `docs/specs/inlined/025-documents-in-the-backbone.md` §10, §10.1 — the
  `decision` task kind and `task_decisions`, which Task 2 builds
- `internal/api/cockpit.go` — `modeFacts`, `selectMode`,
  `modeFactsForProject`, `assembleProjectCockpit`
- `internal/api/server.go:503-506` — the `/ideas` and `/intake`
  `globalPlaceholder` registrations this plan replaces
- `internal/api/webform.go` — `sameOriginForm`, `parseWebForm`, `webActor`,
  `recordFormTask` (the RecordEvent + apply write pattern every web
  mutation follows)
- `internal/store/events.go` — `RecordEvent`; Task 9 adds the tx-scoped
  insert the promotion transaction needs
- `internal/store/tasks.go` — `CreateTask`, `Transition`, the state CHECK
- `internal/store/participants.go` — `AddParticipant(tx, …, eventID)`
- `docs/plans/2026-08-14-approvals-1-table-and-web-act.md` — the decide
  act this plan's Editorial Evaluation rows resolve through

## Global Constraints

- **Exact spellings, quoted once.**
  - Task kinds after Task 2 (the 025 §10 seven):
    `('feature','bug','chore','design','review','spike','decision')`.
  - Gate 1 decision: title `Authorize bounded pre-research?`,
    `response_type = 'yes_no'`. Gate 2 decision: title
    `Accept, narrow, park, or stop?`, `response_type =
    'single_select_notes'`, options (in order) `accept`, `narrow`, `park`,
    `stop`. Stage decision: title `Enter <stage>?`, `response_type =
    'single_select_notes'`, options `enter`, `defer`. Stage slugs:
    `research`, `report`, `story`, `distribution`.
  - Editorial Evaluation approval rows: `entity_kind = 'task'`,
    `entity_id = <intake task id>`, `subject_revision = <dossier
    revision>`, `required_role` `editor` and `science-lead` (one row
    each), created by the system with no human author.
  - Dossier revision: `ev<N>` where `N` is the highest `events.id` whose
    payload or apply touched the intake task (Task 5's `DossierRevision`).
  - Promotion stamps: labels default `{"kind": "sunstone-story"}` (the
    proposal's labels merged over it), `horizon = 'bounded'`. Milestones,
    in position order: 1 `internal review`, 2 `publication`. Deliverables:
    `dataset/data product`, `reproducible analysis`, `methodology`,
    `scientific report` under `internal review`; `story` under
    `publication`. This shape is a starting point the team refines — the
    server never enforces it after minting (029 §2).
  - `entity_edges` rows this plan writes (P2's table): `(project, <id>,
    task, <intake task id>, 'seeded_by')` and `(project, <id>, task,
    <decision task id>, 'stage_decision')`.
  - Event types minted here: `decision.posed`, `decision.recorded`,
    `intake.promoted`. Source `web` for every act in this plan. Capture
    and adoption reuse the existing task-create and task-assign event
    types — captured into intake *is* task creation in the intake project.
  - Closed candidate states: promoted → `merged`; killed → `abandoned`.
    A killed idea costs exactly one closed task and keeps its trace —
    nothing is deleted.
  - Metric names: `worklode_intake_flow_total{action}`, `action` ∈
    `{captured, adopted, selection_started, gate1_recorded,
    gate2_recorded, promoted, killed}`;
    `worklode_stage_transitions_total{outcome}`, `outcome` ∈
    `{recorded, refused_lead, refused_reason, invalid, error}`.
- **The intake project is designated by configuration.**
  `LODE_INTAKE_PROJECT` names the standing intake project's id. Unset, the
  `/ideas` and `/intake` pages render an honest setup message and the
  capture POST answers 409 — no silent auto-creation of a project. The
  intake project itself is created once, by an operator, through the
  ordinary project-create surface, with `horizon = 'standing'`.
- **`answer` jsonb carries flow keys.** 025 §10.1's answer shape is
  `{picked, notes, freetext, value}`; this plan's flows add keys beside
  them: the Gate 2 accept answer carries `proposal` (Task 8) and a stage
  decision answer carries `stage` (Task 11).
  `ValidateDecisionAnswer` validates the core keys for the response type
  and ignores extras.
- **Stage is a query.** No column ever stores the Sunstone stage, the
  cockpit mode, or "promoted". `selectMode` stays a pure function; the
  facts feeding it are the `seeded_by` edge and recorded stage decisions.
  `?variant=` or any other query parameter must never influence
  `selectMode`'s inputs.
- **Every mutation is one event.** Every act here wraps its store writes
  in `RecordEvent(ctx, "web", …)` or rides an existing apply transaction.
  The promotion transaction inserts its extra rows (`crew.member_added`
  per Crew member, `intake.promoted`) through Task 9's tx-scoped event
  insert — same table, same provenance, one transaction.
- **Migrations:** new numbered `.up.sql`/`.down.sql` pairs (0059, 0060),
  listed in `deploy/base/kustomization.yaml`, never an edit to a shipped
  one. Parts are authored in parallel; `./scripts/check-migrations.sh`
  renumbers on collision.
- **Metrics** (spec 022): nil-safe metrics structs in the owning
  package's `metrics.go`, `worklode_` prefix, bounded label values only —
  never a project id, task id, or actor as a label. Tests for every new
  metric.
- **One model** (ADR 036): every new shape crossing the HTTP boundary is
  declared in `internal/model` with wire-named fields;
  `internal/model/rule_test.go` enforces it.
- **Routes** are named in `internal/api/router.go`'s `routeGuards`;
  `NewServer` refuses to boot otherwise. `internal/cmd` decides,
  `internal/cli` renders — this plan adds no CLI verb, so the seam is
  untouched.
- **Store tests need Postgres with pgvector**; a silent skip proved
  nothing. **`e2e/` drives public surfaces only** — never a direct store
  write. **Every task leaves `go test ./...` green** and ends with a
  commit. `go generate ./...` after any `.templ` or stylesheet edit;
  commit the generated artifacts.

## Decisions this plan executes (made against the spec; do not reopen)

- **The dossier is a query** (CTO, 2026-08-25). Assembled from the intake
  task's own fields, its recorded decisions, its approval rows, and its
  events. No `dossiers` table, no dossier revision rows — the revision is
  the event high-water mark, so any new recorded fact makes prior
  approvals visibly stale.
- **The `decision` kind and `task_decisions` land here** (migration 0060),
  exactly as 025 §10/§10.1 specify. No other part of this series builds
  them, and §1's stage query and §8.1's gates cannot exist without them.
  The accepted 025 plan predates the seventh kind, so this plan carries it.
- **Editorial requirements are spec-mandated, not flow-driven.** 029 §8.1
  names the Editor and Science Lead decisions directly, so the Gate 2
  `accept` transaction inserts the two `awaiting` rows itself. 029 §7.2's
  flow engine (approvals part 2) governs the *promoted project's* reviews;
  its snapshot is copied at promotion, not consulted at intake.
- **Promotion parameters ride the Gate 2 answer.** "No second Create
  project confirmation" means the final approval decide must already know
  the project key, name, lead, and initial Crew. The Gate 2 `accept` form
  collects them; they are recorded in the answer's `proposal` key —
  a fact on the intake task, queryable and event-backed, dossier-style.
- **Adoption is assignment.** "A named human must adopt it before
  Selection begins" (029 §8.1, 032 §5): an unassigned intake task is an
  idea; assigning it is adopting it. No new column, no new state.
- **Stage transitions are one-act recorded decisions.** The lead's
  confirm act creates the decision task with its answer recorded and the
  task closed in one transaction, plus the `stage_decision` edge.
  "Deciding again is another decision task" (025 §10) — returning to an
  earlier stage appends another one, so history is append-only for free.
- **Promotion rides the second Editorial decide.** When a decide
  transaction leaves both Editorial rows approved on the current dossier
  revision, the same transaction promotes. A project-key collision at
  that instant fails the decide with a clear message naming the fix (a
  fresh Gate 2 decision with an available key); the accept-time check in
  Task 8 makes that race rare.

## Tasks

### Task 1 — Migration 0059: project labels and horizon

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
blockedBy: []
```

Create `deploy/base/migrations/0059_project_metadata.up.sql` / `.down.sql`
(number assigned to this part; the collision check renumbers):

```sql
-- Spec 029 §1: free-form labels stamped at promotion for classification
-- rules to act on (kind=sunstone-story is the first with meaning), and the
-- bounded/standing horizon attribute. No seeded_by column — that reference
-- is an entity_edges row (029 §5, part 2's table).
ALTER TABLE projects ADD COLUMN labels  jsonb NOT NULL DEFAULT '{}';
ALTER TABLE projects ADD COLUMN horizon text NOT NULL DEFAULT 'standing'
    CHECK (horizon IN ('bounded','standing'));
```

Down drops both columns. Then the read plumbing, end to end:

- `internal/model/project.go`: `Labels map[string]string` (wire `labels`)
  and `Horizon string` (wire `horizon`) on `Project`.
- `internal/store/projects.go`: extend `projectColumns`, `scanProject`,
  and `projectExtras`; `CreateProject` keeps its signature (defaults
  apply). Add the one setter promotion needs, tx-scoped like the house
  pattern: `SetProjectMetadata(tx *sql.Tx, projectID string,
  labels map[string]string, horizon string) error`, refusing an unknown
  horizon with `ErrInvalidInput` before the UPDATE.
- `internal/api/admin.go`: `toProjectJSON` carries both fields;
  `CreateProjectInput` gains optional `Labels`/`Horizon` so the standing
  intake project can be created with `horizon: "standing"` over the API.

First test (`internal/store/projects_test.go`):

```go
func TestProjectMetadataRoundTrip(t *testing.T) {
	s := store.OpenTestStore(t)
	mustCreateProject(t, s, "intake", "Intake", "IN")
	tx := mustBegin(t, s)
	err := store.SetProjectMetadata(tx, "intake",
		map[string]string{"kind": "sunstone-story"}, "bounded")
	if err != nil {
		t.Fatal(err)
	}
	commit(t, tx)
	p := mustGetProject(t, s, "intake")
	if p.Labels["kind"] != "sunstone-story" || p.Horizon != "bounded" {
		t.Errorf("got labels %v horizon %q", p.Labels, p.Horizon)
	}
}
```

Also cover: defaults on a fresh project (`{}`, `standing`); an invalid
horizon is `ErrInvalidInput`; the API create round-trips both fields.

- [ ] Both migration files written and listed in
      `deploy/base/kustomization.yaml`; `./scripts/check-migrations.sh
      --no-fix` exits 0.
- [ ] Roundtrip up → down → up against a scratch database.
- [ ] `go test ./internal/store ./internal/api -count=1` against Postgres
      — expect `ok`, not a skip.
- [ ] Commit: `Project labels and horizon (029 §1)`.

### Task 2 — Migration 0060: the decision task kind and task_decisions

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
blockedBy: []
```

Implement 025 §10's seventh kind, which the gates (§8.1) and the stage
query (§1) are made of. The CHECK, `validKinds`, and `wlc:TaskKind` change
together (the standing rule 029 §2 restates), so this is one commit:

`deploy/base/migrations/0060_decision_tasks.up.sql`:

```sql
-- 025 §10: 'decision' joins the kind scheme. A decision is assigned, never
-- claimed into a worktree; its deliverable is the recorded answer below.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','design','review','spike','decision'));

-- 025 §10.1 verbatim: the side table keyed one-to-one to tasks, so the
-- core table stays generic. answer is null until recorded; writing it,
-- stamping decided_at, and closing the task are one transaction.
CREATE TABLE task_decisions (
    task_id          text PRIMARY KEY REFERENCES tasks(id),
    response_type    text NOT NULL CHECK (response_type IN (
                         'single_select', 'multi_select', 'single_select_notes',
                         'pick_or_freetext', 'yes_no', 'freetext')),
    options          jsonb,
    min_picks        int,
    max_picks        int,
    answer           jsonb,
    decided_at       timestamptz
);
```

Down restores the six-kind CHECK (refusing while `decision` rows exist is
correct — a down over live decisions is data loss) and drops the table.

Code, in the same commit:

- `ns/concept.ttl`: add the `decision` `wlc:TaskKind` concept (camelCase
  term rules per the docs-authoring skill); run `./scripts/nsgen.py` so
  `internal/ns/gen.go`'s `TaskKinds` becomes the seven-kind list.
  `internal/api/tasks.go`'s `validKinds` derives from it — no edit there.
- Claim-path exclusion (025 §10: worked through the lease-free path,
  never `lode task claim`): `readyCandidates` in
  `internal/store/ranking.go` excludes `kind = 'decision'`, and the claim
  transaction refuses one with a message naming `lode task assign` as the
  path. The lease-free `start`/`submit`/`done` verbs continue to work on
  it unchanged.
- Verify `TestTaskKindsAgreeAcrossSources` (the existing three-source
  agreement test) passes; extend its fixture list if it enumerates kinds.

First test (`internal/store/ranking_test.go` or the claim tests' home):

```go
func TestDecisionTasksAreNeverClaimable(t *testing.T) {
	s := store.OpenTestStore(t)
	projectWithTask := seedProjectTask(t, s, "decision") // kind=decision, ready
	if _, err := claimNext(t, s); !errors.Is(err, store.ErrNoReadyTasks) {
		t.Fatalf("claim over a lone decision task: got %v, want no ready tasks", err)
	}
	_ = projectWithTask
}
```

- [ ] Migration pair listed in `kustomization.yaml`;
      `./scripts/check-migrations.sh --no-fix` exits 0; roundtrip clean.
- [ ] `./scripts/nsgen.py` run; `internal/ns/gen.go` committed.
- [ ] `go test ./... -count=1` green (kind-agreement test included).
- [ ] Commit: `Decision task kind and task_decisions (025 §10, for 029 §1/§8.1)`.

### Task 3 — Pure rules: answer validation and the derived stage

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
```

Two pure fact-to-conclusion functions, table-tested with no database, so
every later task (and later parts) can feed them typed inputs.

`internal/store/decision_rules.go`:

```go
// DecisionOption is one offered choice, mirroring options jsonb.
type DecisionOption struct{ Label, Description string }

// ValidateDecisionAnswer checks answer against the posed shape: picked
// labels exist among options and respect min/max picks for the types that
// pick; yes_no requires value yes|no|unsure; freetext requires non-empty
// freetext; notes are free. Keys beyond the core shape are ignored — flows
// ride extra keys (proposal, stage) beside them. Returns ErrInvalidInput
// (wrapped, with the reason) on any violation.
func ValidateDecisionAnswer(responseType string, options []DecisionOption,
	minPicks, maxPicks *int, answer map[string]any) error
```

`internal/store/stage_rules.go`:

```go
// StageDecision is one recorded stage transition, oldest first.
type StageDecision struct {
	Stage     string // research | report | story | distribution
	Entered   bool   // picked == enter
	Reason    string // notes
	DecidedAt time.Time
}

// StageWork is the open-work summary the derivation reads.
type StageWork struct {
	OpenTasks            int
	Deliverables         int
	TerminalDeliverables int
}

// StageView is the derived Sunstone stage (029 §1): a query result, never
// a stored column. Stage is the latest entered stage ("" before any —
// the mode machinery renders that as Approved launch or plain
// Operations). Carryover reports open work that predates the latest
// transition; it stays attached to its original milestone and is neither
// closed nor reparented. RecommendClosure is set for a bounded project
// whose deliverables are all terminal with no open tasks — a
// recommendation the lead confirms, never an automatic transition.
type StageView struct {
	Stage             string
	History           []StageDecision
	Carryover         bool
	RecommendClosure  bool
}

func DeriveStage(horizon string, decisions []StageDecision, work StageWork) StageView
```

First test, verbatim shape:

```go
func TestDeriveStageIsTheLatestEnteredStage(t *testing.T) {
	got := store.DeriveStage("bounded", []store.StageDecision{
		{Stage: "research", Entered: true, DecidedAt: t0},
		{Stage: "report", Entered: true, Reason: "two tasks carry over", DecidedAt: t1},
	}, store.StageWork{OpenTasks: 2, Deliverables: 5})
	if got.Stage != "report" || !got.Carryover || got.RecommendClosure {
		t.Errorf("got %+v", got)
	}
}
```

Cover for `DeriveStage`: no decisions (stage ""); a return to an earlier
stage (research after report — the latest entered wins, and `History`
keeps both, order preserved); closure recommended only when bounded, all
deliverables terminal, and no open tasks. For `ValidateDecisionAnswer`:
each response type's happy path, an unknown pick, min/max violations, a
yes_no `unsure` (valid — "nobody minds" is an answer, 025 §10.1), extra
keys ignored.

- [ ] `go test ./internal/store -run 'TestValidateDecisionAnswer|TestDeriveStage' -count=1`
      — expect `ok` with no database.
- [ ] Commit: `Pure decision-answer and derived-stage rules (029 §1)`.

### Task 4 — Decision store layer: pose, record, read

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

`internal/store/decisions.go` + `decisions_test.go`. Tx-scoped writes in
the house pattern (`CreateTask`, `AddParticipant`), so the intake flow
(Task 8), promotion (Task 9), and the stage act (Task 11) call them
inside their own `RecordEvent` transactions:

```go
type DecisionInput struct {
	ResponseType string
	Options      []DecisionOption
	MinPicks     *int
	MaxPicks     *int
}

// CreateDecisionTask poses a decision: a kind=decision task (CreateTask,
// so it draws an ordinary <KEY>-<n> id) plus its task_decisions row, in
// the caller's transaction. Assignee names the accountable decider (029
// §6.1: one assignee); empty means posed but unowned.
func CreateDecisionTask(tx *sql.Tx, now time.Time, projectID, title, body,
	assignee string, in DecisionInput, eventID int64) (*model.Task, error)

// RecordDecisionAnswer validates the answer against the posed shape
// (ValidateDecisionAnswer), writes answer + decided_at, and closes the
// task (Transition to 'merged') — one transaction, per 025 §10.1: there
// is no state in which a decision is answered but still open. Recording
// is terminal: a second call is ErrDecisionRecorded.
func RecordDecisionAnswer(tx *sql.Tx, now time.Time, taskID string,
	answer map[string]any, eventID int64) error

// DecisionForTask loads the task_decisions row (posed shape + answer);
// ErrNotFound when the task has none.
func (s *Store) DecisionForTask(ctx context.Context, taskID string) (*model.TaskDecision, error)
```

`model.TaskDecision` goes in `internal/model` (ADR 036): `Task`,
`ResponseType`, `Options`, `MinPicks`, `MaxPicks`, `Answer
map[string]any`, `DecidedAt *time.Time` — it will cross the HTTP boundary
on the candidate page's JSON twin later; declaring it now costs nothing
and prevents a package-local twin.

First test:

```go
func TestRecordDecisionAnswerClosesTheTaskAtomically(t *testing.T) {
	s := store.OpenTestStore(t)
	taskID := poseYesNo(t, s, "Authorize bounded pre-research?")
	tx := mustBegin(t, s)
	err := store.RecordDecisionAnswer(tx, s.Now(), taskID,
		map[string]any{"value": "yes"}, seedEvent(t, tx))
	if err != nil {
		t.Fatal(err)
	}
	commit(t, tx)
	d := mustDecision(t, s, taskID)
	task := mustGetTask(t, s, taskID)
	if d.DecidedAt == nil || task.State != "merged" {
		t.Errorf("decided_at %v, task state %q", d.DecidedAt, task.State)
	}
}
```

Also cover: an invalid answer records nothing and leaves the task open;
recording twice is `ErrDecisionRecorded`; `CreateDecisionTask` on a
non-decision `response_type` string is `ErrInvalidInput`;
`DecisionForTask` misses cleanly.

- [ ] `go test ./internal/store -run TestDecision -count=1` against
      Postgres — expect `ok`.
- [ ] Commit: `Decision store layer: pose, record, read (025 §10.1)`.

### Task 5 — Store readers: intake candidates, dossier, lifecycle facts

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 4]
```

One UI-neutral bulk reader per fact family, in
`internal/store/intake.go` + `intake_test.go`. These are the queries the
dossier decision made this plan out of:

```go
// IntakeCandidate is one intake task with its pipeline position derived
// from governed facts: unadopted (no assignee), in Selection (adopted,
// Gate 2 unrecorded), in Editorial Evaluation (Gate 2 accepted, approval
// rows open or stale), promoted (seeded_by edge points here), or killed
// (abandoned). Phase is derived in SQL/Go from those facts — no column.
type IntakeCandidate struct {
	Task           model.Task
	Assignee       *string
	Gate1, Gate2   *model.TaskDecision
	Approvals      []Approval // the two Editorial rows, when materialized
	PromotedTo     string     // project id via the seeded_by edge, "" otherwise
}

// ListIntakeCandidates returns every task in the intake project with the
// facts above, newest first. Killed (abandoned) candidates are included —
// the trace is the point — and the UI decides what to fold away.
func (s *Store) ListIntakeCandidates(ctx context.Context, intakeProject string) ([]IntakeCandidate, error)

// GetIntakeCandidate is the single-candidate variant, by task id;
// ErrNotFound when the task is absent or outside the intake project.
func (s *Store) GetIntakeCandidate(ctx context.Context, intakeProject, taskID string) (*IntakeCandidate, error)

// DossierRevision is the exact revision an Editorial decision binds:
// "ev<N>" for the highest events.id recorded against the task (its
// creation, decisions, assignment, and any later fact). Monotonic by
// construction — every mutation is an event — so any new recorded fact
// makes prior approvals visibly stale.
func DossierRevision(tx *sql.Tx, taskID string) (string, error)

// ProjectLifecycleFacts feeds modeFactsForProject and the stage card:
// the seeded_by edge (promoted from intake), the recorded stage
// decisions (entity_edges rel 'stage_decision' joined to task_decisions,
// oldest first), and the StageWork summary DeriveStage reads.
type LifecycleFacts struct {
	SeededBy  string // intake task id, "" for engineering projects
	Decisions []StageDecision
	Work      StageWork
}

func (s *Store) ProjectLifecycleFacts(ctx context.Context, projectID string) (*LifecycleFacts, error)
```

Gate 1 and Gate 2 rows are found through the `entity_edges` rows the
intake flow writes (Task 8): `(task, <candidate>, task, <gate task>,
'gate1')` / `'gate2'` — the same typed-edge table, so "which task is this
candidate's Gate 2" is a query, not a title match. `DossierRevision`
needs a deterministic events-to-task correlation; use the same
`state_log`/event linkage `CreateTask` and `Transition` already write
(`LogChange` rows carry the event id), plus the decision/approval events
whose payloads name the task — settle the exact join in this task and
test it, rather than leaving each caller to improvise one.

First test seeds one candidate through the real write paths (task create,
assign, gate edges from a helper) and asserts `ListIntakeCandidates`
derives each phase; a second asserts `DossierRevision` grows when a new
fact is recorded and is stable when nothing changed.

- [ ] `go test ./internal/store -run 'TestIntake|TestDossierRevision|TestProjectLifecycleFacts' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Intake, dossier, and lifecycle readers (029 §1, §8.1)`.

### Task 6 — Tracer: /ideas and /intake replace their placeholders

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

The convergence point: the first pages joining migrations, decisions,
and readers over real data, replacing the two `globalPlaceholder`
registrations at `internal/api/server.go:503-506`. This task also lands
the two entry mutations — capture and adopt — because the pages exist to
receive a pitch, and a capture form with no route would be a rendered
control pointing at nothing.

- **Config.** `Config.IntakeProject` from `LODE_INTAKE_PROJECT` (the
  project id). A helper `s.intakeProject(ctx)` resolves it via
  `GetProject`; unset or unresolvable yields a typed "not configured"
  error the pages render honestly.
- **`/ideas`** (`internal/ui/intake.templ`, handler in
  `internal/api/intake.go`): the low-friction capture surface. A form
  with exactly two fields — title and description (029 §8.1: capture
  requires only these) — posting to `POST /ideas`, above the list of
  unadopted candidates, each with an adopt form. The capture handler
  writes through the existing `recordFormTask` path into the intake
  project (kind `feature` is fine — the idea is ordinary work until
  posed decisions surround it), then redirects to `/intake/{id}`.
- **Adopt** (`POST /intake/{id}/adopt`): assignment is adoption. The
  form takes the adopter (default: the acting session actor) and writes
  through the same store path as `POST /api/v1/tasks/{id}/assign` —
  one write function, two surfaces, exactly as `recordCrewAdd` models.
- **`/intake`**: the portfolio. `ListIntakeCandidates` grouped by
  derived phase — Ideas (unadopted), Selection, Editorial Evaluation,
  Promoted, Killed — each row linking to `/intake/{id}` (Task 7's page;
  register that GET here as a minimal stub rendering the candidate
  title, so no dead link ships). Honest empty and not-configured states.
- **Routes and guards**: replace the two placeholder registrations; add
  `POST /ideas` and `POST /intake/{id}/adopt` (`guarded(permTaskWrite)`)
  and `GET /intake/{id}` (`guarded(permWebRead)`) to `routeGuards`.
- **Metric**: `worklode_intake_flow_total{action}` counter in
  `internal/api/metrics.go`, incremented `captured` / `adopted` here.

First test (`internal/api/web_test.go` harness):

```go
func TestCaptureRequiresOnlyTitleAndDescription(t *testing.T) {
	st, h := newTestServerWithIntake(t) // seeds the intake project + config
	rr := postForm(t, h, "/ideas", url.Values{
		"title":       {"Coastal wind subsidies"},
		"description": {"Who profits from the new subsidy scheme?"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("capture = %d, want 303", rr.Code)
	}
	body := getOK(t, h, "/ideas")
	if !strings.Contains(body, "Coastal wind subsidies") {
		t.Error("captured idea not listed")
	}
}
```

Also cover: unconfigured instance → setup message on both GETs, 409 on
the POST; `/intake` groups by phase; adoption moves an idea out of the
Ideas group; the nav-marker and `aria-current` invariants survive the
placeholder replacement (update the placeholder-message assertions).

- [ ] `go generate ./...`; regenerated artifacts committed.
- [ ] `go test ./internal/api -count=1` against Postgres — expect `ok`.
- [ ] Commit: `Ideas and Intake pages with capture and adoption (029 §8.1, 032 §5)`.

### Task 7 — The candidate page: Editorial decision mode over the dossier query

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5, 6]
```

`GET /intake/{id}` becomes the detailed candidate view: the cockpit
shell in **Editorial decision** mode even though the governed object
remains the intake task (032 §5). Read-only in this task — the gate
forms arrive with their routes in Task 8.

- Mode comes from the one authority: render with
  `selectMode(modeFacts{IntakeCandidate: open})`, where `open` is "the
  candidate task is not terminal". A promoted candidate's page keeps its
  trace and links to the created project via `PromotedTo`; a killed
  candidate's page shows the closed state.
- The dossier **is this page's center canvas**, assembled by query
  (`GetIntakeCandidate` + `DossierRevision` + the task's events): the
  current question (title, description), the recorded Gate 1 and Gate 2
  decisions with their answers and deciders, the two Editorial approval
  rows with state and the revision each bound, the current dossier
  revision (and a visible "stale — recorded after this review" marker
  when an approval's `subject_revision` is older), and the event trail.
  Layered per 032 §5: summary first, facts behind a disclosure.
- The decision rail names who acts next, derived: unadopted → "adopt";
  adopted, Gate 1 unrecorded → the adopter; Gate 2 accepted → the two
  approval rows ("awaiting editor", "awaiting science-lead") linking to
  the `/reviews` decide surface; **a rejected row → the rejecting role
  shown as accountable for revise / reconsider / park / close** (032
  §5) — a projection of the approval row, not a minted task.
- View types in `internal/ui/views.go` beside the existing ones; the
  mapping in `internal/api/render.go`. No new store code — this page is
  the proof the readers were UI-neutral.

First test: seed a candidate mid-Selection (adopted, Gate 1 recorded
yes), assert the page shows the recorded authorization, the dossier
revision, and no Gate 2 answer yet; a second test seeds a rejected
Editorial row and asserts the rejecting role is named accountable.

- [ ] `go generate ./...`; artifacts committed.
- [ ] `go test ./internal/api -run TestCandidatePage -count=1` — expect `ok`.
- [ ] Commit: `Candidate page: Editorial decision mode over the dossier query (032 §5)`.

### Task 8 — The gate decisions: Selection, Gate 1, Gate 2, and the kill path

```yaml
kind: feature
priority: critical
skills:
  - superpowers:test-driven-development
blockedBy: [4, 7]
```

The intake flow's mutations, each a web form on the candidate page, each
one `RecordEvent("web", …)` transaction composing Task 4's store
functions. Handlers in `internal/api/intake.go`, following
`createTaskFromForm`'s shape (`sameOriginForm`, `parseWebForm`, error
mapping); forms in `internal/ui/intake.templ`.

1. **Start Selection** (`POST /intake/{id}/selection`): poses Gate 1 —
   `CreateDecisionTask` (title `Authorize bounded pre-research?`,
   `yes_no`, assignee = the adopter) plus the `(task, <candidate>, task,
   <gate>, 'gate1')` edge, under event `decision.posed`. Refused (409)
   when the candidate is unadopted or Gate 1 already exists. Starting
   Selection may prepare Gate 1, but nothing more: **no pre-research
   begins without the recorded authorization** — the AI run itself has
   no server surface yet (see Deferred), so the server's whole
   enforcement is the recorded order of these facts, and the page states
   that plainly next to the Gate 1 form.
2. **Record Gate 1** (`POST /intake/{id}/gate1`):
   `RecordDecisionAnswer` with `{"value": yes|no|unsure}` under
   `decision.recorded`. A `yes` poses Gate 2 in the same transaction
   (title `Accept, narrow, park, or stop?`, `single_select_notes`,
   options in the constrained order, `'gate2'` edge, its own
   `decision.posed` rides the same tx via Task 9's `InsertEventTx` —
   or lands in this tx's apply with two event rows once that helper
   exists; if this task runs first, record the pose as part of the
   gate1 event payload and split later). Only the candidate's assignee
   records it when an acting actor is known; an open instance degrades
   the guard exactly as the crew forms do.
3. **Record Gate 2** (`POST /intake/{id}/gate2`): the answer's `picked`
   is one of the four. `accept` requires the **launch proposal** fields
   on the same form — project key (validated against `projectKeyRe` and
   availability now, so the promotion-time collision is a rare race, not
   the norm), project name (prefilled from the title), lead actor, and
   optional further Crew members with roles — recorded inside the answer
   as `proposal`, and the same transaction materializes the two
   Editorial `awaiting` rows: `entity_kind 'task'`, the candidate's id,
   `subject_revision = DossierRevision(tx, id)` computed *after* this
   answer's own event, `required_role` `editor` and `science-lead`.
   (Approvals part 2 has already widened `entity_kind` and the decide
   queue beyond `'pr'` — this plan's `blockedBy` guarantees it; verify
   the decide path accepts kind `task` before wiring, and stop if not.)
   `stop` transitions the candidate to `abandoned` in the same
   transaction — the kill path: one closed task, full trace. `narrow`
   and `park` record the answer and change nothing else; the portfolio
   shows them, and a later fresh Gate 2 decision reopens the question
   (deciding again is another decision task).
4. **Metric**: `worklode_intake_flow_total` gains `selection_started`,
   `gate1_recorded`, `gate2_recorded`, `killed`.

First test — the ordering property §8.1 exists for:

```go
func TestGate2CannotBePosedBeforeGate1Authorizes(t *testing.T) {
	st, h := newTestServerWithIntake(t)
	id := seedAdoptedCandidate(t, st)
	startSelection(t, h, id) // poses Gate 1
	rr := postForm(t, h, "/intake/"+id+"/gate2",
		url.Values{"picked": {"accept"}, "key": {"COW"}, "name": {"Cost of Wind"}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("gate2 before gate1 = %d, want 409", rr.Code)
	}
}
```

Also cover: Gate 1 `yes` poses Gate 2; Gate 1 `no` does not; Gate 2
`accept` materializes exactly two awaiting rows bound to the current
dossier revision; `accept` without a valid key is 422 and records
nothing; `stop` abandons the candidate and increments `killed`; a
non-assignee actor recording a gate is 403; every mutation leaves one
event row of the right type.

- [ ] `go generate ./...`; artifacts committed.
- [ ] `go test ./internal/api -run 'TestGate|TestSelection' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Selection and gate decisions with the kill path (029 §8.1)`.

### Task 9 — The promotion transaction

```yaml
kind: feature
priority: critical
skills:
  - superpowers:test-driven-development
blockedBy: [1, 8]
```

One transaction, no second "Create project" confirmation (029 §8.1,
032 §5). It rides the web decide act: when `POST /approvals/{id}/decide`
approves an intake Editorial row and both rows on the current dossier
revision are now approved, the same `RecordEvent` transaction promotes,
and the handler redirects to `/projects/{newID}` instead of `/reviews`.

**Store** (`internal/store/intake.go`):

```go
// InsertEventTx inserts one event row inside the caller's transaction —
// the primitive a multi-fact transaction needs to keep every fact
// event-backed (029 §8.4 requires crew.member_added from this very call
// site). Same (source, external_id) conflict semantics as RecordEvent.
// Lives in events.go beside RecordEvent.
func InsertEventTx(tx *sql.Tx, now time.Time,
	source, externalID, typ string, payload []byte) (id int64, inserted bool, err error)

type PromotionInput struct {
	CandidateID string
	Proposal    LaunchProposal // key, name, lead, crew — from the Gate 2 answer
	Labels      map[string]string
	ActorID     string // the deciding actor, for created_by fields
}

// PromoteCandidate runs 029 §8.1's transaction: refuses unless both
// Editorial rows for the current DossierRevision are approved
// (ErrNotApproved) and no seeded_by edge exists yet (ErrAlreadyPromoted);
// then creates the project, SetProjectMetadata (labels merged over
// {"kind":"sunstone-story"}, horizon bounded), writes the seeded_by
// entity_edges row, mints the two milestones and five deliverables of
// the default shape (part 1's milestone create + CreateDeliverable with
// its milestone), snapshots the approval flow resolved for the labels
// into projects.approval_flow/_name/_rev (approvals part 2's resolver —
// verify its exported name at execution), adds the lead (is_lead, role
// from the proposal or 'member') and each proposed Crew member via
// AddParticipant with a crew.member_added event each (InsertEventTx),
// and closes the intake task (Transition to 'merged'). Returns the
// created project id.
func PromoteCandidate(tx *sql.Tx, now time.Time, in PromotionInput) (string, error)
```

**API** (`internal/api/webform.go` / wherever approvals part 2 left the
decide handler): after the decide's store write succeeds inside the
apply, detect the intake case (entity kind `task`, entity id inside the
configured intake project), reload both rows, and when both are
`approved` on the candidate's current dossier revision call
`PromoteCandidate` in the same tx, followed by one
`InsertEventTx("web", "intake-promote:"+candidateID, "intake.promoted",
payload)`. On success the handler redirects `303` to
`/projects/{newID}` — the Approved launch cockpit (Task 10 makes the
mode real). A key collision surfacing here maps to 409 with a message
naming the fix (a fresh Gate 2 decision with an available key); the
approval row stays undecided because the transaction rolled back.

**Metric**: `worklode_intake_flow_total{action="promoted"}`.

First test (`internal/store/intake_test.go`), the atomicity property:

```go
func TestPromotionIsOneTransaction(t *testing.T) {
	s := store.OpenTestStore(t)
	cand := seedCandidateThroughGate2Accept(t, s, "COW", "Cost of Wind")
	approveEditorialRow(t, s, cand, "editor")
	approveEditorialRow(t, s, cand, "science-lead") // triggers promotion

	p := mustGetProject(t, s, projectFor(t, s, cand))
	if p.Labels["kind"] != "sunstone-story" || p.Horizon != "bounded" {
		t.Errorf("labels %v horizon %q", p.Labels, p.Horizon)
	}
	assertSeededByEdge(t, s, p.ID, cand)
	assertMilestones(t, s, p.ID, "internal review", "publication")
	assertDeliverableCount(t, s, p.ID, 5)
	if mustGetTask(t, s, cand).State != "merged" {
		t.Error("intake task not closed")
	}
}
```

Also cover: one approval alone promotes nothing; a stale approval (an
older `subject_revision` than the current dossier revision) promotes
nothing; re-delivering the second decide is idempotent
(`ErrAlreadyPromoted` surfaces as the existing 409-on-resolved decide);
each Crew member got a `crew.member_added` event and the lead row has
`is_lead`; the snapshot columns are populated; a key collision rolls the
whole decide back.

- [ ] `go test ./internal/store ./internal/api -run 'TestPromot' -count=1`
      against Postgres — expect `ok`.
- [ ] `go test ./... -count=1` — green.
- [ ] Commit: `The promotion transaction (029 §8.1, 032 §5)`.

### Task 10 — modeFactsForProject stops lying; the stage card

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5, 9]
```

`internal/api/cockpit.go:41-65` already holds the pure machinery; this
task feeds it real facts and surfaces the derived stage — the series'
payoff.

- Replace `modeFactsForProject(store.Project) modeFacts` with a
  derivation from `ProjectLifecycleFacts`: `PromotedFromIntake =
  SeededBy != ""`; `EnteredResearch =` a recorded `research` stage
  decision with `Entered`; `IntakeCandidate` stays false for a real
  project — a candidate is a task, and its page (Task 7) is where that
  fact is true. `assembleProjectCockpit` loads the facts once and passes
  them to both `selectMode` and the stage card.
- Retire the unconditional `operationsModeBasis`: the mode's
  `EvidenceSummary` now states its actual basis — for Approved launch,
  the `intake.promoted` event and the missing Enter Research decision;
  for Operations-via-decision, the recorded decision task and when; for
  a never-promoted project, the current "no intake lifecycle facts"
  wording survives as the honest default.
- Add `Stage *model.StageView` to `model.CockpitProjection` (stage slug,
  history with reasons, carryover flag, closure recommendation), mapped
  from `DeriveStage`, `nil` for projects with no lifecycle facts. Render
  it on the project page: the stage orientation line, carryover shown
  under its original milestone grouping (stage movement neither closes
  nor reparents work — the milestone views from part 1 already hold it),
  and the closure recommendation as rail text, explicitly not a button
  (the close act is out of scope here).
- No query parameter reaches any of this; assert it — a request with
  `?variant=editorial` renders the same mode.

First test:

```go
func TestPromotedProjectIsApprovedLaunchUntilEnterResearch(t *testing.T) {
	st, h := newTestServerWithIntake(t)
	projectID := promoteSeededCandidate(t, st) // Task 9's path
	proj := getCockpitJSON(t, h, projectID)
	if proj.Mode.Name != "approved_launch" {
		t.Fatalf("mode = %q, want approved_launch", proj.Mode.Name)
	}
	recordStageDecision(t, st, projectID, "research") // store seed; the act is Task 11
	if got := getCockpitJSON(t, h, projectID).Mode.Name; got != "operations" {
		t.Errorf("mode after Enter Research = %q, want operations", got)
	}
}
```

Also cover: a pre-029 project stays `operations` with the honest default
basis; the stage card renders history and carryover; the JSON cockpit
carries `Stage`; the `?variant=` assertion.

- [ ] `go generate ./...` if the templ changed; artifacts committed.
- [ ] `go test ./internal/api -run 'TestMode|TestStage|TestCockpit' -count=1`
      — expect `ok`.
- [ ] Commit: `Mode facts from governed rows; the derived stage card (029 §1, 032 §3)`.

### Task 11 — The stage transition act

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [10]
```

`POST /projects/{id}/stage` — the lead's explicit confirmation, of which
Enter Research is the first (029 §1, 032 §3). One act, one transaction:
`CreateDecisionTask` (title `Enter <stage>?`, `single_select_notes`,
assignee = the acting lead) and `RecordDecisionAnswer`
(`{"picked":["enter"], "notes": <reason>, "stage": <slug>}`) in the same
`RecordEvent("web", …, "decision.recorded", …)` apply, plus the
`(project, <id>, task, <id>, 'stage_decision')` edge. The posed-and-
answered-in-one-act shape is legitimate here because the decider and the
poser are the same accountable lead; the record is the governed fact.

Guards, in the handler before the write:

- Only the project lead acts: the acting actor must hold the project's
  `is_lead` participant row when an actor is known (open-instance
  degradation as elsewhere). Refusal → 403,
  `worklode_stage_transitions_total{outcome="refused_lead"}`.
- Stage slug must be one of the four; anything else → 422.
- **Advancing with unfinished work requires a reason** (029 §1): when
  `DeriveStage`'s work summary shows open tasks or non-terminal
  deliverables and the target stage is ahead of the current one, an
  empty `notes` → 422 with a message naming the open work count,
  `outcome="refused_reason"`. Returning to an earlier stage always
  takes a reason — it appends another reasoned transition and preserves
  history, never rewriting anything.

The form lives in the decision rail: Approved launch renders "Enter
Research" as its primary act; Operations renders the next-stage confirm
(and the go-back option behind disclosure). Native buttons in a plain
POST form, keyboard-operable (032 §10's standing rule).

First test:

```go
func TestAdvancingWithOpenWorkRequiresAReason(t *testing.T) {
	st, h := newTestServerWithIntake(t)
	projectID := promoteSeededCandidate(t, st)
	enterResearch(t, h, projectID) // no open-work gate on the first entry
	seedOpenTask(t, st, projectID)

	rr := postFormAsLead(t, h, "/projects/"+projectID+"/stage",
		url.Values{"stage": {"report"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("advance without reason = %d, want 422", rr.Code)
	}
	rr = postFormAsLead(t, h, "/projects/"+projectID+"/stage",
		url.Values{"stage": {"report"}, "notes": {"pipeline task carries over"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("advance with reason = %d, want 303", rr.Code)
	}
}
```

Also cover: a non-lead is refused; the recorded decision closes its task
and the edge exists; returning to `research` after `report` appends a
second decision and `DeriveStage` reports the return with both history
entries; metric outcomes.

- [ ] `go generate ./...`; artifacts committed.
- [ ] `go test ./internal/api -run TestStage -count=1` against Postgres
      — expect `ok`.
- [ ] Commit: `Stage transition act: Enter Research and reasoned moves (029 §1)`.

### Task 12 — e2e: pitch to Approved launch through public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [9, 11]
```

`e2e/intake_test.go` (build tag `e2e`), on the `smoke_test.go` harness,
public surfaces only. The Editorial decides are session-gated, so this
test stands up the fake OIDC issuer the `internal/api` tests already use
(extract the helper into a shared internal test package, or replicate
its minimal issuer in `e2e/` — whichever is smaller; logging in through
`/auth/login` is itself a public surface) with two users: one in the
`editor` group, one in `science-lead`.

The journey, each step asserted through pages or `/api/v1` reads:

1. Bootstrap token → create the standing intake project
   (`horizon: "standing"`) → the server under test runs with
   `LODE_INTAKE_PROJECT` set to it.
2. `POST /ideas` with title and description only → the idea lists on
   `/ideas`.
3. Adopt → start Selection → record Gate 1 `yes` → record Gate 2
   `accept` with key `COW`, a name, and the editor user as lead.
4. `GET /reviews` lists the two Editorial rows; the editor session
   decides one, the science-lead session decides the other; the second
   decide's redirect lands on `/projects/{id}` and that page renders
   Approved launch mode.
5. `/api/v1/projects/{id}` and the project page show the labels, the
   two milestones, the five deliverables, and the Crew with the lead;
   the intake task reads `merged`; `/intake/{candidate}` links to the
   project.
6. The lead enters Research; the page now renders Operations with stage
   `research`.
7. A second captured idea is killed at Gate 2 (`stop`): one `abandoned`
   task, still listed on `/intake` with its trace.

```go
//go:build e2e
```

- [ ] `go test -race -count=1 -tags e2e ./e2e/ -run TestIntakeToApprovedLaunch`
      against Postgres — expect `ok`.
- [ ] Full suite: `go test -race -count=1 -tags e2e ./e2e/` — green.
- [ ] Commit: `e2e: pitch to Approved launch over public surfaces`.

## Verification

- `go test ./... -count=1` green with Postgres reachable (a silent skip
  proved nothing); `go test -race -count=1 -tags e2e ./e2e/` green.
- `curl -s localhost:9090/metrics | grep -E 'worklode_(intake_flow|stage_transitions)_total'`
  shows both families after exercising the flows.
- `./scripts/check-migrations.sh --no-fix` exits 0; both new pairs are in
  `deploy/base/kustomization.yaml`.
- `lode doc anchors docs/plans/2026-08-25-research-work-3-intake-and-promotion.md`
  reports no errors.
- Manual trace on the compose stack: capture → adopt → gates → two
  decides → the redirect lands on an Approved launch cockpit whose mode
  basis names the promotion event.

## Deferred — named so each gap is a decision, not an oversight

- **The AI Threat↔Intervention analysis** that groups and deduplicates
  findings into an unowned pitch (029 §8.1 gives it one sentence): needs
  a spec of its own — where findings come from, what deduplication
  means, and how an unowned pitch is represented before adoption. This
  plan's capture path is where an adopted one would enter.
- **The immutable AI run record** — effective policy, prompts, tool
  calls, source excerpts, scoring inputs (029 §8.1, one sentence): needs
  a spec of its own before any schema exists. Until then the dossier
  query names the recommendation only if someone records it as an
  ordinary fact on the task; the audit path §8.1 sketches has nowhere to
  live, and this plan says so rather than inventing it.
- **Structured dossier facts** — litmus-test results, claims, sources,
  unknowns, hypothesis changes as typed rows: same status. Today they
  ride the task body, its attachments, and its events; the dossier query
  renders what exists.
- **Project close and the active Crew's close** (029 §1's last
  sentence): the crew-lifecycle part owns the Crew half; the close act
  and closure ceremony are planned with it. This plan stops at the
  closure *recommendation*.
- **Stage-advance recommendations richer than the closure hint**
  (032 §3's decision rail): grows with deliverable-state facts from the
  deliverable-state part; the pure `DeriveStage` seam is where they
  plug in.
- **A generic pose/answer surface for decisions** outside the intake and
  stage flows (025 §10's full breadth): this plan builds the kind, the
  table, and the store layer; a general "pose a decision" UI is its own
  small piece of work when something needs it.
