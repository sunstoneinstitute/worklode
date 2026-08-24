---
status: draft
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7
    coverage: full
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.1
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-approvals-1-table-and-web-act.md
      - docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.2
    coverage: full
  - spec: docs/specs/032-project-cockpit.md#sec-7
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
      - docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
blockedBy:
  - 2026-08-14-approvals-1-table-and-web-act.md
---

# Approvals part 2 — flows, requirements, and the multi-lane queue

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** 029 §7.2 exists end to end. Named, versioned approval flows live in
instance configuration with pre-baked defaults Worklode ships; a project
carries the *effective snapshot* of the flow that governs it
(`projects.approval_flow`), so a later configuration edit cannot silently
change an open review; the rule engine that matches flows to project labels
and derives requirement rows from a flow is pure, table-tested code in
`internal/store/approval_rules.go`; a governed deliverable created in a
flow-stamped project materializes its review lanes as `awaiting` rows owned
by the system `worklode` actor; ad-hoc requirements can be added to any
governed target; and the `/reviews` queue — hard-wired to `pull_requests` by
part 1's `prEntityJoin` — learns to show a `document`, `deliverable`, or
`task` awaiting sign-off.

**The load-bearing property** (029 §7.2): an open review answers to the
snapshot stamped on the project, never to live instance configuration. The
flow files are inputs to one explicit act — applying a flow — and everything
downstream (materialization, `required_role`, the reviewer template) reads
the project's stored copy. Editing a flow file changes nothing until someone
re-applies it, and that re-application is an event.

**Series:** part 2 of the approvals series.
`docs/plans/2026-08-14-approvals-1-table-and-web-act.md` (part 1, shipped)
built the `approvals` table, the PR ingest, and the session-gated web decide
act; its closing section defers exactly what this plan picks up. Part 3,
`2026-08-25-approvals-3-revision-binding-and-gates`, owns
reopen-on-material-change, the impact review, the self-review exception, and
the CI/CMS gates. This plan is P3 of the 2026-08-25 nine-plan series over
spec 029: **P4** (`2026-08-25-research-work-3-intake-and-promotion`) is
blocked by this plan — its promotion transaction stamps `projects.labels`
and calls this plan's `MatchFlow` + `ApplyApprovalFlow` — and **P5**
(approvals part 3) is blocked by this plan's lane-keyed rows.

**Coverage gaps, declared:** for 029 §7.1, this plan delivers the widened
`(entity_kind, entity_id, subject_revision, lane)` key and materialization
beyond the PR source, but reopen-on-material-change, the impact review, and
the self-review exception stay with part 1 and part 3
(`fullCoverageWith`). 029 §7 (the bare section head) is only the heading
sentence and is `full` here with §7.2. For 032 §7, this plan covers the
review-requirement half — lanes existing, independent decisions, the
reviewer template; the deliverable readiness view belongs to
`2026-08-25-research-work-1-milestones` and the revision-bound evidence
bundle presentation (exact commit, environment lock, diff from the
previously reviewed revision) to part 3. 032 §10 and §11 bind every task
here and are implemented by none of them.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, cobra CLI, Prometheus client. Store and
`internal/api` tests need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §7.1, §7.2
- `docs/plans/2026-08-14-approvals-1-table-and-web-act.md` — the shipped
  base this plan extends, and the voice of its migration comments
- `internal/store/approvals.go` — everything part 1 built: `Approval`,
  `InsertAwaitingApproval`, `OpenApprovalForEntity`, `DecideApproval`,
  `prEntityJoin`, `ListAwaitingApprovals`, `ApprovalsAwaiting`
- `internal/store/approval_rules.go` — the three pure functions this plan
  grows into the rules engine
- `internal/api/deliverables.go` — `recordDeliverable`, the write path Task
  7 hooks
- `internal/api/instanceenv.go` — the posture for instance configuration:
  parse at boot, refuse the typo, never fall back silently
- `internal/api/server.go` — `Config`, `recordEvent`, the
  `EnsureServiceActor` call for the `watcher` actor (Task 2 repeats the
  pattern for `worklode`)
- `internal/model/rule_test.go` — what ADR 036 will refuse; Task 3 moves
  `Approval` into `internal/model` and this test is the referee

## Global Constraints

- **Exact spellings, quoted once.** The shipped default flow is named
  `story`, revision `1`, matching label `kind=sunstone-story`. Its six lanes
  and their required groups:
  `analysis/peer` → `analysis-reviewers`,
  `methodology/science-lead` → `science-leads`,
  `methodology/domain-expert` → `domain-experts`,
  `report/buddy` → `report-buddies`,
  `report/expert` → `domain-experts`,
  `report/journalist` → `journalists`.
  Deliverable targets, matched case-insensitively on exact name:
  `Reproducible analysis`, `Methodology`, `Scientific report`. Flow files
  admit requirement entity kinds `document`, `deliverable`, `task` only —
  `pr` rows come from part 1's GitHub ingest, never from a flow. Env var:
  `LODE_APPROVAL_FLOWS_DIR`. System actor id: `worklode`. Event types:
  `approval_flow.applied` (source `cli`), `approval.required` (source `cli`
  for the API, riding the `deliverable.created` event when materialized at
  creation). Routes: `POST /api/v1/projects/{id}/approval-flow`
  (permission `project.admin`, already granted), `POST /api/v1/approvals`
  (new permission `approval.require`, granted `{RoleUser, RoleAdmin}`).
  Metric names: `worklode_approval_flow_applied_total{outcome}` with
  `outcome` ∈ `{applied, unknown_flow, error}`;
  `worklode_approval_requirements_total{origin}` with `origin` ∈
  `{flow, adhoc}`. An empty `subject_revision` (`''`) means "required, no
  revision designated yet"; part 1's PR rows and the pre-0057 backfill keep
  lane `''`.
- **The three lanes are the spec's, verbatim** (029 §7.2): "reproducible
  analysis: GitHub PR review per task policy plus one analysis-level
  qualified data-science or engineering peer decision on an exact commit;
  the peer is selected through the project reviewer template and is not the
  author; methodology: Science Lead and domain-expert decisions on an exact
  revision; and scientific report: buddy, expert, and journalist decisions
  on an exact revision." The PR half of the analysis lane is part 1's ingest
  and this plan does not duplicate it; the analysis-level peer decision is
  the `analysis/peer` lane.
- **P4 owns `projects.labels`.** The rule engine reads a
  `map[string]string` of labels and is table-tested against hand-set maps;
  nothing in this plan creates, stores, or reads a labels column. `MatchFlow`
  gains its one database-wired caller when P4's promotion lands. The flow
  apply surface in this plan takes an explicit flow name for the same
  reason.
- **Rule-created rows are owned by the system `worklode` actor** (029
  §7.2): `approvals.created_by = 'worklode'` on every row a flow
  materializes, because the rule inserted them, and attributing policy to
  whichever human filed the idea would misstate who did what. The event log
  preserves causality. `NewServer` ensures the actor row exists exactly as
  it does for `watcher`.
- **Every mutation is one event.** The flow apply and the ad-hoc require
  each wrap their store writes in `RecordEvent`; materialization at
  deliverable creation rides the existing `deliverable.created`
  transaction's apply callback, so the rows' provenance is that event. No
  approval row is written outside an event transaction.
- **Migrations:** new numbered `.up.sql`/`.down.sql` pairs — 0056 and 0057,
  assigned to this plan — listed in `deploy/base/kustomization.yaml`, never
  an edit to a shipped migration (0038 stays exactly as it landed; its
  comment anticipated this plan).
- **Metrics** (spec 022): nil-safe metrics structs in the owning package's
  `metrics.go`, `worklode_` prefix, bounded label values only — never a
  project id, flow name, lane, actor, or group as a label. Tests for every
  new metric.
- **One model** (ADR 036): every shape crossing the HTTP boundary is
  declared once in `internal/model` — this plan moves `Approval` there and
  adds the flow types. `internal/model/rule_test.go` and `deps_test.go` are
  the referees.
- **Every route is a `routeGuards` row**; `NewServer` refuses to boot
  otherwise. **`internal/cmd` decides, `internal/cli` renders**: the new
  CLI verbs fetch in cobra `RunE` and render through one `cli.*Render`
  function; `internal/cmd/renderrule_test.go` is the tripwire.
- **Store tests need Postgres with pgvector** (`store.OpenTestStore`); they
  skip silently without it unless `CI` is set — a green run without
  Postgres proved nothing.
- **`e2e/` drives public surfaces only** — HTTP API, signed webhooks, web
  pages; never a direct store write (032 §11).
- **UI toolchain is fixed** by 032 §12: templ components compiled by
  `go generate ./...`, Tailwind via the pinned CLI; regenerate and commit
  generated artifacts in any task touching a `.templ` or the stylesheet.
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (made in the approved design; do not reopen)

- **Lane is a column, and the identity of a requirement.** One revision
  carries several review lanes by widening part 1's UNIQUE key to
  `(entity_kind, entity_id, subject_revision, lane)` — exactly the widening
  0038's comment reserved. A lane names the flow requirement that minted
  the row (`methodology/science-lead`), so re-materialization is idempotent
  by constraint. Part 1's PR rows keep lane `''`.
- **Flow requirements target deliverables by exact, case-insensitive
  name.** 032 §7's governed targets (analysis, methodology, report) are a
  story project's deliverables; the backbone has no deliverable taxonomy
  and this plan does not invent one. A project whose deliverables use other
  names gets no flow-minted rows for them — visible in the queue as their
  absence is not, which is why ad-hoc requirements exist. Document- and
  task-kind requirements are valid in flow files but the shipped default
  uses none; in this plan they arrive ad-hoc.
- **One required group per lane.** `required_role` is a single Keycloak
  group (029 §7.1). The spec's "data-science or engineering peer" union is
  the group `analysis-reviewers`, whose membership the org composes in
  Keycloak — flows are instance configuration, so instances that want a
  different vocabulary edit the flow file, not this code.
- **The reviewer template is the lane→actor map in the effective
  snapshot.** Applying a flow accepts `reviewers` (`lane` → actor id);
  materialization stamps `required_actor` from it, else falls back to the
  lane's `required_role`. "Not the author" is enforced at decide time by
  the generalized self-approval check, not at template time.
- **A deliverable lane materializes with `subject_revision = ''`** — the
  requirement exists from entity creation (029 §7.1) but nothing has been
  designated for review. `DecideApproval` refuses an empty revision
  (`ErrNoRevision`): approving nothing would unmake §7.1's "the revision
  the actor actually saw". Part 3 owns designating and re-binding
  revisions. An ad-hoc requirement may carry an explicit revision (a
  commit, a doc version) and is then decidable immediately; an ad-hoc row
  on a `document` defaults to the doc's current `version`.
- **Self-approval beyond PRs compares the decider to the entity's
  `created_by`.** Part 1's GitHub-login comparison stays for `entity_kind
  'pr'`; `document`, `deliverable`, and `task` rows resolve their author
  from the entity row. Unknown on either side proves nothing and does not
  refuse, exactly as part 1 decided.
- **The flow apply is explicit and admin-gated in this plan.** `POST
  /api/v1/projects/{id}/approval-flow` takes a flow name; P4's promotion
  becomes the second caller of the same store function, with `MatchFlow`
  choosing the name from labels. Re-applying is allowed — it is an explicit,
  event-logged act, so it is not a *silent* change — and materialization is
  idempotent on the widened key.
- **Elective plan review is an ad-hoc row** (029 §7.2: "If a user elects to
  review a plan…"): no automation mints it, and the plan's task-result PR
  reviews already flow through part 1's ingest per task policy. One review
  session presenting several targets is just several rows; each decide is
  already per-row.
- **No web form for ad-hoc requirements in this plan.** The API and CLI
  carry the mutation; the cockpit's approval-oriented review views are 032
  §7 surface owned by part 3. The queue page (Task 9) renders every kind
  read-only-plus-decide, which is the web's half here.

## Tasks

### Task 1 — Migrations 0056 and 0057: the flow snapshot and the lane key

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
  - worklode-migrations
blockedBy: []
```

Two pairs. `deploy/base/migrations/0056_project_approval_flow.up.sql`:

```sql
-- Spec 029 §7.2: the project stores the *effective snapshot* of its
-- approval flow, so a later instance-configuration edit cannot silently
-- change an open review. approval_flow is the full snapshot (flow plus the
-- project reviewer template); name and rev are denormalized for listing
-- without unmarshalling. All NULL until a flow is applied.
ALTER TABLE projects ADD COLUMN approval_flow      jsonb;
ALTER TABLE projects ADD COLUMN approval_flow_name text;
ALTER TABLE projects ADD COLUMN approval_flow_rev  text;
```

Down: drop the three columns.

`deploy/base/migrations/0057_approvals_lanes.up.sql` — the two widenings
0038's comment reserved for this plan, plus row ownership:

```sql
-- Spec 029 §7.2: one revision carries several independent review lanes
-- (Science Lead and domain-expert on the same methodology revision are two
-- rows). lane names the flow requirement that minted the row; part 1's
-- PR rows and ad-hoc rows that name no lane keep ''.
ALTER TABLE approvals ADD COLUMN lane text NOT NULL DEFAULT '';

-- Who put the requirement here. Rule-created rows are owned by the system
-- 'worklode' actor (029 §7.2); ad-hoc rows record the requesting actor.
-- Nullable: part 1's ingest rows predate the column.
ALTER TABLE approvals ADD COLUMN created_by text
    REFERENCES actors (id) ON DELETE RESTRICT;

ALTER TABLE approvals
    DROP CONSTRAINT approvals_entity_kind_entity_id_subject_revision_key;
ALTER TABLE approvals ADD CONSTRAINT approvals_entity_revision_lane_key
    UNIQUE (entity_kind, entity_id, subject_revision, lane);
```

Down: drop the new constraint, re-add the three-column UNIQUE, drop both
columns. (The down direction fails on data only if two lanes share a
revision — acceptable for a dev rollback, and the up is what ships.)

- [ ] Write all four files; add the four lines under `worklode-migrations`
      in `deploy/base/kustomization.yaml` after the 0051 entries.
- [ ] `./scripts/check-migrations.sh --no-fix` — expect exit 0. The other
      series parts claim 0052–0055 and 0058+ in parallel; this plan's
      numbers are assigned, so a collision means someone strayed.
- [ ] Roundtrip against a scratch database (golang-migrate:test-roundtrip):
      up → down → up applies cleanly on an empty database.
- [ ] `go test -trimpath ./internal/store -run TestMigrations -count=1` —
      expect `ok` (the harness applies the full chain on every
      `OpenTestStore`).
- [ ] Commit: `Flow snapshot columns and the approvals lane key (029 §7.2)`.

### Task 2 — Flow model, shipped defaults, and the loader

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
```

The flow types in `internal/model/approvalflow.go` (stdlib only; wire
names, per ADR 036 — the snapshot jsonb, the apply request, and the flow
list all serialize these):

```go
// ApprovalFlow is one named, versioned review flow (029 §7.2). Flows live
// in instance configuration; a project stores the effective snapshot.
type ApprovalFlow struct {
	Name         string                `json:"name"`
	Rev          string                `json:"rev"`
	Match        map[string]string     `json:"match,omitempty"` // label selector
	Requirements []ApprovalRequirement `json:"requirements"`
}

// ApprovalRequirement is one review lane a flow demands.
type ApprovalRequirement struct {
	Lane       string `json:"lane"`        // unique within the flow
	EntityKind string `json:"entity_kind"` // document | deliverable | task
	Target     string `json:"target,omitempty"` // exact entity name, case-insensitive; empty = every entity of the kind
	Role       string `json:"role"`        // Keycloak group that may decide
}

// ApprovalFlowSnapshot is what projects.approval_flow stores: the flow the
// project was stamped with plus its reviewer template (lane -> actor id).
type ApprovalFlowSnapshot struct {
	Flow      ApprovalFlow      `json:"flow"`
	Reviewers map[string]string `json:"reviewers,omitempty"`
}
```

The shipped default lives in `internal/api/approvalflows/story.json` —
name `story`, rev `1`, match `{"kind": "sunstone-story"}`, and the six
lanes with the exact spellings from Global Constraints. Loader in
`internal/api/approvalflows.go`:

```go
//go:embed approvalflows/*.json
var defaultFlowFS embed.FS

// LoadApprovalFlows returns the effective flow set: the embedded defaults,
// then every *.json in dir (LODE_APPROVAL_FLOWS_DIR; empty dir string
// means defaults only). A dir flow whose name matches a default replaces
// it. Any unreadable or invalid file is a boot error, not a fallback —
// the instanceenv posture: a typo in configuration that changes what the
// server demands must fail startup.
func LoadApprovalFlows(dir string) ([]model.ApprovalFlow, error)
```

Validation (called per flow by the loader, and pure so Task 4's tests reuse
it): `store.ValidateFlow(f model.ApprovalFlow) error` in
`approval_rules.go` — non-empty `name` and `rev`; lanes non-empty and
unique within the flow; `entity_kind` ∈ `{document, deliverable, task}`
(`pr` is refused: PR rows come from the ingest); non-empty `role`.

Wiring: `Config` gains `ApprovalFlowsDir string // LODE_APPROVAL_FLOWS_DIR`
threaded from the serve command like its siblings; `NewServer` calls
`LoadApprovalFlows`, keeps the slice on `server`, and calls
`st.EnsureServiceActor(ctx, "worklode", "approval flow rules")` — the
pattern the `watcher` actor already follows in `server.go`.

First test, in `internal/api/approvalflows_test.go` (no database):

```go
func TestLoadApprovalFlowsShipsTheStoryDefault(t *testing.T) {
	flows, err := LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	if story.Rev != "1" || story.Match["kind"] != "sunstone-story" {
		t.Errorf("story = rev %q match %v", story.Rev, story.Match)
	}
	if len(story.Requirements) != 6 {
		t.Errorf("story has %d lanes, want 6", len(story.Requirements))
	}
}
```

Also cover: a dir flow overriding `story` by name; a dir file with an
unknown entity kind, a duplicate lane, or a `pr` requirement fails with an
error naming the file; an unreadable dir entry fails.

- [ ] `go test -trimpath ./internal/api -run TestLoadApprovalFlows -count=1`
      — expect `ok`.
- [ ] `go test -trimpath ./internal/model/... -count=1` — `rule_test.go`
      and `deps_test.go` accept the new types.
- [ ] Commit: `Approval flows: model, shipped story default, loader (029 §7.2)`.

### Task 3 — Store: lanes, row ownership, and the model move

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Make the store lane-aware, and move `Approval` to `internal/model` while
touching every call site anyway — Tasks 10 and 11 put it on the wire, and
ADR 036 says it is declared once, where the wire shapes live.

In `internal/model/approval.go`: `Approval` with part 1's fields plus
`Lane string` and `CreatedBy *string`, json-tagged with wire names
(`entity_kind`, `subject_revision`, `lane`, `created_by`, …).
`internal/store/approvals.go` keeps its functions and scan plumbing but
scans into `model.Approval`; `internal/api` and `internal/ui` follow the
rename mechanically.

Signature changes, part 1 callers updated in the same task
(`internal/hooks/github.go` passes `lane ""` and `createdBy nil`):

```go
// InsertAwaitingApproval materializes one requirement as an 'awaiting'
// row. Idempotent on (entity_kind, entity_id, subject_revision, lane).
// Returns whether a row was inserted, so materialization can count and
// event payloads can say what actually changed.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision, lane string,
	requiredRole, requiredActor, createdBy *string) (bool, error)

// OpenApprovalForLane returns the open row ('awaiting' or
// 'changes_requested') for one lane of one entity; ErrNotFound otherwise.
// Replaces OpenApprovalForEntity: with lanes, "the" open row of an entity
// is no longer a single thing. The PR ingest reads lane "".
func OpenApprovalForLane(tx *sql.Tx, entityKind, entityID, lane string) (*model.Approval, error)

// OpenApprovalsForEntity lists every open row of an entity, lane order.
func OpenApprovalsForEntity(tx *sql.Tx, entityKind, entityID string) ([]model.Approval, error)
```

First test (Postgres):

```go
func TestApprovalLanesAreIndependentRows(t *testing.T) {
	s := store.OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	for _, lane := range []string{"methodology/science-lead", "methodology/domain-expert"} {
		ins, err := store.InsertAwaitingApproval(tx, now,
			"deliverable", "WL-DEL-1", "", lane, ptr("science-leads"), nil, ptr("worklode"))
		if err != nil || !ins {
			t.Fatalf("lane %s: inserted=%v err=%v", lane, ins, err)
		}
	}
	open, err := store.OpenApprovalsForEntity(tx, "deliverable", "WL-DEL-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("open rows = %d, want 2 (one per lane)", len(open))
	}
}
```

Also cover: re-inserting the same lane returns `false, nil` and stays one
row; two lanes on the same non-empty revision coexist (the widened key);
`OpenApprovalForLane` finds only its lane; `created_by` round-trips; part
1's `TestApproval*` suite still passes with lane `''`.

- [ ] `go test -trimpath ./internal/store -run TestApproval -count=1`
      against Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/hooks -count=1` — the ingest still
      passes with lane `''`.
- [ ] `go test -trimpath ./... -count=1` — the model move left everything
      green.
- [ ] Commit: `Lane-keyed approvals; Approval moves to internal/model (029 §7.2)`.

### Task 4 — Pure rules: flow matching and requirement derivation

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

The rules engine, as pure functions beside part 1's three in
`internal/store/approval_rules.go`, table-tested with no database — P4's
promotion and Task 6's materialization both consume them against typed
inputs:

```go
// MatchFlow picks the flow that governs a project with these labels: every
// pair in a flow's match must be present in labels. Among matches the most
// specific (largest match set) wins; ties break on name. A flow with an
// empty match never auto-matches — it is only ever applied by name. Nil
// when nothing matches.
func MatchFlow(flows []model.ApprovalFlow, labels map[string]string) *model.ApprovalFlow

// RequirementsForEntity returns the lanes a flow demands of one entity:
// requirements whose EntityKind matches and whose Target is empty or
// equals name case-insensitively. Deterministic lane order.
func RequirementsForEntity(f model.ApprovalFlow, entityKind, name string) []model.ApprovalRequirement
```

First test, verbatim shape (hand-set labels — `projects.labels` is P4's,
and this is exactly why the engine is pure):

```go
func TestMatchFlowOnLabels(t *testing.T) {
	story := model.ApprovalFlow{Name: "story", Rev: "1",
		Match: map[string]string{"kind": "sunstone-story"}}
	byName := model.ApprovalFlow{Name: "custom", Rev: "1"} // empty match
	cases := []struct {
		labels map[string]string
		want   string // "" = no match
	}{
		{map[string]string{"kind": "sunstone-story"}, "story"},
		{map[string]string{"kind": "sunstone-story", "horizon": "bounded"}, "story"},
		{map[string]string{"kind": "engineering"}, ""},
		{nil, ""},
	}
	for _, c := range cases {
		got := store.MatchFlow([]model.ApprovalFlow{story, byName}, c.labels)
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != c.want {
			t.Errorf("MatchFlow(%v) = %q, want %q", c.labels, name, c.want)
		}
	}
}
```

Also cover: specificity (a two-pair match beats a one-pair match), the name
tiebreak, and for `RequirementsForEntity`: the shipped `story` flow (loaded
via Task 2's `LoadApprovalFlows("")`) yields exactly
`methodology/science-lead` and `methodology/domain-expert` for
`("deliverable", "Methodology")`, three lanes for
`("deliverable", "Scientific report")`, one for
`("deliverable", "Reproducible analysis")`, none for
`("deliverable", "Interview notes")`, none for `("task", "anything")` —
029 §7.2: tasks have no review requirement by default. `ValidateFlow`'s
refusals get their own table.

- [ ] `go test -trimpath ./internal/store -run 'TestMatchFlow|TestRequirementsForEntity|TestValidateFlow' -count=1`
      — expect `ok` without Postgres (pure functions).
- [ ] Commit: `Flow matching and requirement derivation, pure (029 §7.2)`.

### Task 5 — Store: decide generalization beyond PRs

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`DecideApproval` learns the three new entity kinds. In
`internal/store/approvals.go` and `errors.go`:

1. **New sentinel** `ErrNoRevision`. After the open-state check: a row with
   `SubjectRevision == ""` is refused — there is nothing designated to
   review, so there is nothing an approval could bind (029 §7.1: the
   decision binds "the immutable … revision the actor actually saw").
   Part 3 designates revisions; until then these rows are the visible gap,
   not a decidable item.
2. **Author lookup per kind.** `authorActorForEntity(tx, kind, id) (string,
   error)`: `document` → `docs.created_by` (entity_id is the doc id),
   `deliverable` → `deliverables.created_by`, `task` → `tasks.created_by`;
   `""` on no row or NULL. For these kinds, self-approval is
   `author == in.ActorID` — actor ids compare directly, no GitHub login
   indirection. The `'pr'` branch keeps part 1's login comparison
   untouched.
3. The error mapping in `internal/api/webform.go`'s `decideApproval` gains
   `ErrNoRevision` → 422 with "nothing has been designated for review yet";
   the decision metric's `outcome` label set gains `no_revision` (bounded).

First test (Postgres):

```go
func TestDecideRefusesUndesignatedRevision(t *testing.T) {
	s := store.OpenTestStore(t)
	tx := mustBegin(t, s)
	seedActor(t, tx, "alice")
	mustInsertAwaiting(t, tx, "deliverable", "WL-DEL-1", "", "methodology/science-lead")

	_, err := store.DecideApproval(tx, store.DecideInput{
		ApprovalID: openID(t, tx, "deliverable", "WL-DEL-1", "methodology/science-lead"),
		Decision:   "approve", ActorID: "alice", Now: time.Now(),
	})
	if !errors.Is(err, store.ErrNoRevision) {
		t.Fatalf("err = %v, want ErrNoRevision", err)
	}
}
```

Also cover: a `document` row whose `created_by` is the decider →
`ErrSelfApproval`; a different decider with a revision-bound `deliverable`
row resolves; unknown author (`created_by` NULL) does not refuse; the PR
path's part 1 tests still pass unchanged.

- [ ] `go test -trimpath ./internal/store -run TestDecide -count=1` against
      Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/api -run TestDecideApproval -count=1`
      — the handler mapping and metric label, expect `ok`.
- [ ] Commit: `Decide beyond PRs: author-by-created_by, ErrNoRevision (029 §7.2)`.

### Task 6 — Store: the snapshot stamp and materialization

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4]
```

The write side of §7.2, tx-scoped so Task 10's apply event and Task 7's
`deliverable.created` event both carry it. In
`internal/store/approvalflow.go`:

```go
// SetProjectApprovalFlow stamps the effective snapshot on the project.
// Overwrites any previous snapshot: applying is an explicit, event-logged
// act, so this is not the silent change the snapshot exists to prevent.
func SetProjectApprovalFlow(tx *sql.Tx, projectID string, snap model.ApprovalFlowSnapshot) error

// ProjectApprovalFlow loads the snapshot; nil when the project has none.
func ProjectApprovalFlow(tx *sql.Tx, projectID string) (*model.ApprovalFlowSnapshot, error)

// MaterializeForEntity inserts the 'awaiting' rows the snapshot's flow
// demands of one entity, lane-keyed and idempotent. required_actor comes
// from the reviewer template (snap.Reviewers[lane]) when set, else the
// row carries the lane's required_role. created_by is the system
// 'worklode' actor: the rule inserted these rows (029 §7.2). New
// deliverable rows bind subject_revision '' — nothing designated yet.
// Returns how many rows were actually inserted.
func MaterializeForEntity(tx *sql.Tx, now time.Time,
	snap model.ApprovalFlowSnapshot, entityKind, entityID, name string) (int, error)

// MaterializeForProject runs MaterializeForEntity over the project's
// existing deliverables — the backfill an apply performs so a flow stamped
// after creation still materializes every requirement (029 §7.1: the
// requirement is a visible row, whenever it became a requirement).
func MaterializeForProject(tx *sql.Tx, now time.Time,
	projectID string, snap model.ApprovalFlowSnapshot) (int, error)
```

Extend `projectColumns`/`scanProject` with `approval_flow_name` and
`approval_flow_rev`, and `model.Project` with `ApprovalFlowName` and
`ApprovalFlowRev` (json `approval_flow_name` / `approval_flow_rev`,
`omitempty`) — the cockpit and CLI can then say which flow governs a
project without unmarshalling the snapshot.

First test (Postgres): stamp a project with the shipped `story` snapshot,
seed a deliverable named `Methodology`, then

```go
n, err := store.MaterializeForProject(tx, now, projectID, snap)
// want n == 2: methodology/science-lead and methodology/domain-expert,
// both state 'awaiting', subject_revision '', created_by 'worklode'
```

and a second call returns `0` (idempotent). Also cover: a reviewer template
`{"methodology/science-lead": "alice"}` stamps `required_actor = alice` on
that lane only; a deliverable named `Interview notes` materializes nothing;
`ProjectApprovalFlow` round-trips the snapshot and is nil before any stamp.
Seed the `worklode` actor with `EnsureServiceActor` in the test setup —
the FK is real.

- [ ] `go test -trimpath ./internal/store -run 'TestMaterialize|TestProjectApprovalFlow' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Snapshot stamp and requirement materialization (029 §7.2)`.

### Task 7 — Materialize on deliverable creation

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

029 §7.1's "materialized as an `awaiting` row when the entity is created",
for the flow source. In `internal/api/deliverables.go`,
`recordDeliverable`'s apply callback — after `store.CreateDeliverable`
succeeds, inside the same `deliverable.created` transaction:

```go
snap, err := store.ProjectApprovalFlow(tx, in.ProjectID)
if err != nil {
	return err
}
if snap != nil {
	n, err := store.MaterializeForEntity(tx, now, *snap,
		"deliverable", d.ID, d.Name)
	if err != nil {
		return err
	}
	s.metrics.ApprovalRequirements("flow", n)
}
```

Both surfaces that call `recordDeliverable` (the JSON API and the cockpit
form) get this for free — that is why the hook lives in the shared record
function and not in a handler. A project with no snapshot is untouched:
`snap == nil`, no rows, no metric.

**Metric** (`internal/api/metrics.go`): counter
`worklode_approval_requirements_total{origin}`, `origin` ∈ `{flow, adhoc}`
(Task 11 adds the second value's caller). Nil-safe `ApprovalRequirements`
method adding `n`; test beside the existing metric tests.

First test, in `internal/api/deliverables_test.go` (existing handler
harness; stamp the snapshot through Task 6's store function in setup —
the apply *surface* arrives in Task 10, and this is a unit test of the
creation path):

```go
func TestDeliverableCreationMaterializesFlowLanes(t *testing.T) {
	st, h := newTestServer(t)
	projectID := seedStoryFlowProject(t, st) // stamps the story snapshot
	postDeliverable(t, h, projectID, "Methodology")

	rows := approvalRows(t, st, "deliverable")
	if len(rows) != 2 {
		t.Fatalf("materialized %d lanes, want 2", len(rows))
	}
	for _, r := range rows {
		if r.State != "awaiting" || r.SubjectRevision != "" || deref(r.CreatedBy) != "worklode" {
			t.Errorf("row %s: state=%q rev=%q created_by=%v",
				r.Lane, r.State, r.SubjectRevision, r.CreatedBy)
		}
	}
}
```

Also cover: a deliverable in a snapshot-less project creates no rows; the
metric increments by 2; creating the same-named deliverable twice (distinct
ids) materializes per entity id, not per name.

- [ ] `go test -trimpath ./internal/api -run 'TestDeliverable|TestApprovalRequirementsMetric' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Deliverable creation materializes its review lanes (029 §7.1, §7.2)`.

### Task 8 — Store readers: the queue learns every entity kind

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

Part 1's `ListAwaitingApprovals` hard-joins `pull_requests`
(`prEntityJoin`), so the queue is PR-only by construction. Generalize both
readers in `internal/store/approvals.go` — one fact family, still one
reader each, and plan D's Home counts pick up the new kinds unchanged.

`AwaitingApproval` becomes kind-neutral: replace the PR-specific fields
with what every row renders —

```go
type AwaitingApproval struct {
	model.Approval
	Title             string  // PR title, deliverable name, doc title, task title
	URL               string  // jump-out URL; "" for kinds whose surface is in-app
	Author            string  // display: PR author login, else created_by actor id
	TaskID            string  // "" for non-pr kinds
	ProjectID         string
	ProjectName       string
	RequiredActorName *string
}
```

The query becomes a `UNION ALL` over the four kinds, each branch supplying
its own join to a project — `pr` via `prEntityJoin` → tasks → projects
exactly as today; `deliverable` via `deliverables d ON a.entity_id = d.id`
→ projects; `document` via `docs` (`a.entity_id = d.id::text` — approvals
keys are text, doc ids are bigint); `task` via `tasks`. Ordering stays
oldest-first across the union. `ApprovalsAwaiting` gets the same four
branches inside its count (grouped by the branch's project id); its
signature and the `""`-actor degradation are unchanged.

First test (Postgres): seed one awaiting row of each kind — the PR row
through part 1's fixtures, a deliverable, a doc, and a task row through
`InsertAwaitingApproval` — then assert `ListAwaitingApprovals` returns all
four, each with the right `Title` and `ProjectID`, oldest first. A second
test: `ApprovalsAwaiting(actor, groups)` counts a `required_role` match on
a deliverable row in the right project (the part 1 assertions for PR rows
stay).

- [ ] `go test -trimpath ./internal/store -run 'TestListAwaitingApprovals|TestApprovalsAwaiting' -count=1`
      against Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/api -count=1` — the reviews page still
      renders PR rows through the reshaped reader.
- [ ] Commit: `Queue readers cover document, deliverable, and task rows (029 §7.2)`.

### Task 9 — The `/reviews` page renders every kind

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [8]
```

The web half of the generalized queue, in `internal/ui/approvals.templ`
and `internal/api/render.go`'s `approvalsView`:

- Row identity per kind: the `Title` links to `URL` for `pr` (GitHub stays
  the review surface), to `/docs/{id}` for `document`, to
  `/projects/{ProjectID}/deliverables` for `deliverable`, to
  `/tasks/{id}` for `task`. The kind renders as a `mono muted` prefix so a
  mixed queue scans (`pr`, `deliverable`, `document`, `task`).
- The lane renders when non-empty: `methodology/science-lead` beside
  "awaiting …" — two lanes of one revision are two visibly distinct rows,
  029 §7.2's independence made legible.
- A row with `SubjectRevision == ""` renders
  `<p class="muted">No revision designated for review yet.</p>` in place
  of the decide form — the store refuses the decide (Task 5), and the page
  does not offer an act that can only 422. Rows with a revision keep part
  1's decide form untouched.
- Part 1's invariants survive: one `aria-current="page"`, the `>Reviews<`
  nav marker, the honest empty state.

First test, in `internal/api/web_test.go`:

```go
func TestReviewsPageRendersNonPRLanes(t *testing.T) {
	st, h := newTestServer(t)
	seedAwaitingDeliverableLane(t, st, "Methodology", "methodology/science-lead")

	body := getOK(t, h, "/reviews")
	for _, want := range []string{"Methodology", "methodology/science-lead",
		"No revision designated for review yet."} {
		if !strings.Contains(body, want) {
			t.Errorf("reviews page missing %q", want)
		}
	}
	if strings.Contains(body, `value="approve"`) {
		t.Error("undesignated row must not offer a decide form")
	}
}
```

Also cover: a revision-bound deliverable row does render the decide form;
a document row links to its `/docs/{id}` page.

- [ ] `go generate ./...` after editing the `.templ`; commit the
      regenerated `*_templ.go` (and `app.css` if the stylesheet changed).
- [ ] `go test -trimpath ./internal/api -run 'TestReviews|TestWeb' -count=1`
      — expect `ok`.
- [ ] Commit: `Reviews queue renders every entity kind and lane (029 §7.2)`.

### Task 10 — The flow apply act: API, CLI, event, metric

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [6]
```

One mutation, every surface it has. `POST
/api/v1/projects/{id}/approval-flow`, request
`model.ApplyApprovalFlowInput` (`Name string`, json `name`; `Reviewers
map[string]string`, json `reviewers`, optional), response
`model.ApplyApprovalFlowResponse` — the updated `model.Project` plus
`Materialized int` (json `materialized`).

- **API** (`internal/api/approvalflows.go`): handler resolves `Name`
  against the loaded flow set (404 `unknown_flow` when absent — the flow
  vocabulary is instance configuration, not project data), builds the
  snapshot `{Flow: flow, Reviewers: req.Reviewers}`, and wraps the write:

```go
_, _, err := s.st.RecordEvent(ctx, "cli", extID, "approval_flow.applied", payload,
	func(tx *sql.Tx, _ int64) error {
		if err := store.SetProjectApprovalFlow(tx, projectID, snap); err != nil {
			return err
		}
		n, err := store.MaterializeForProject(tx, s.st.Now(), projectID, snap)
		materialized = n
		return err
	})
```

  with `payload` marshalling `{project, flow, rev, materialized_by:
  "worklode"}`. Route guard: `"POST /api/v1/projects/{id}/approval-flow":
  guarded(permProjectAdmin)` — stamping governance on a project sits with
  the permission that creates projects; P4's promotion runs server-side and
  does not pass through this route.
- **CLI**: `lode project flow <project> --name <flow>
  [--reviewer lane=actor]...` in `internal/cmd/project.go`;
  `Client.ApplyApprovalFlow` in `internal/cli/client.go`; the confirmation
  is one `cli.ApprovalFlowRender(w, resp)` line in `internal/cli/render.go`
  (`applied story rev 1 to WL: 2 requirements materialized`) — no
  tabwriter, no timestamp formatting in `internal/cmd`.
- **Metric**: `worklode_approval_flow_applied_total{outcome}`, `outcome` ∈
  `{applied, unknown_flow, error}`, nil-safe, tested.

First test, in `internal/api/approvalflows_test.go` (Postgres):

```go
func TestApplyFlowStampsSnapshotAndBackfills(t *testing.T) {
	st, h := newTestServer(t)
	projectID := seedProjectWithDeliverable(t, st, "Scientific report")

	resp := postJSON(t, h, "/api/v1/projects/"+projectID+"/approval-flow",
		`{"name":"story","reviewers":{"report/journalist":"alice"}}`, adminToken)
	var out model.ApplyApprovalFlowResponse
	decode(t, resp, &out)
	if out.Materialized != 3 || out.Project.ApprovalFlowName != "story" {
		t.Errorf("materialized=%d flow=%q", out.Materialized, out.Project.ApprovalFlowName)
	}
}
```

Also cover: unknown flow name → 404 and the `unknown_flow` metric outcome;
re-apply → 200 with `Materialized: 0` (idempotent) and a second
`approval_flow.applied` event; the `report/journalist` row carries
`required_actor = alice`; the CLI verb round-trips through a test server
(`internal/cmd` harness) and prints the one-line confirmation.

- [ ] `go test -trimpath ./internal/api -run 'TestApplyFlow|TestApprovalFlowMetric' -count=1`
      against Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestProjectFlow -count=1` —
      expect `ok`; `renderrule_test.go` stays green.
- [ ] `go test -trimpath ./... -count=1` — the router boot checks prove the
      route/guard pair is complete.
- [ ] Commit: `Apply an approval flow: snapshot, backfill, event (029 §7.2)`.

### Task 11 — Ad-hoc requirements: API, CLI, event, metric

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 5]
```

029 §7.2: "Ad-hoc requirements can be added to any governed target." The
mutation, across its surfaces:

- **Model**: `model.RequireApprovalInput{EntityKind, EntityID, Revision,
  Lane, Role, Actor string}` (json wire names; `Role` and `Actor` optional
  and mutually exclusive; `Lane` optional, default `''`; `Revision`
  optional — defaults to the document's current `version` for
  `entity_kind "document"`, `''` otherwise). Response: the created
  `model.Approval`.
- **API**: `POST /api/v1/approvals`, new permission `permApprovalRequire
  Permission = "approval.require"` with a `grants` entry `{RoleUser,
  RoleAdmin}` and its `routeGuards` row — an agent or CI job with a bearer
  token may *file* a requirement; only a session may decide one (part 1's
  gate, untouched). Handler validation: `entity_kind` ∈ `{document,
  deliverable, task, pr}`; the entity exists (404 otherwise); 422 on both
  `Role` and `Actor` set. Write wrapped in `RecordEvent(ctx, "cli", extID,
  "approval.required", payload, ...)` calling `InsertAwaitingApproval` with
  `created_by` = the requesting actor — a human filed this policy, so the
  row says so; only rule-created rows belong to `worklode`. An
  already-present `(kind, id, revision, lane)` returns 200 with the
  existing row (the insert reported `false`), so re-filing is safe.
  Increment `worklode_approval_requirements_total{origin="adhoc"}` on
  actual insert.
- **CLI**: `lode approval require <kind> <id> [--role <group> | --actor
  <actor>] [--lane <lane>] [--revision <rev>]` — a new
  `internal/cmd/approval.go` command group, `Client.RequireApproval` in
  `internal/cli/client.go`, rendered by one `cli.ApprovalRender(w, a)` in
  `render.go` (kind, id, lane, state, and who it awaits; times through
  `cli.LocalTime`).

First test, in `internal/api/approvals_api_test.go` (Postgres):

```go
func TestAdHocRequirementOnATask(t *testing.T) {
	st, h := newTestServer(t)
	taskID := seedTask(t, st)

	resp := postJSON(t, h, "/api/v1/approvals",
		fmt.Sprintf(`{"entity_kind":"task","entity_id":%q,"role":"science-leads","revision":"abc123"}`, taskID),
		userToken)
	var a model.Approval
	decode(t, resp, &a)
	if a.State != "awaiting" || a.SubjectRevision != "abc123" || a.Lane != "" {
		t.Errorf("row = %+v", a)
	}
}
```

Also cover: a `document` require defaults `Revision` to the doc's current
version and is immediately decidable (compose with Task 5's path); a
missing entity → 404; `role` and `actor` together → 422; re-filing returns
the existing row and does not double-count the metric; the CLI verb
round-trips and renders.

- [ ] `go test -trimpath ./internal/api -run TestAdHoc -count=1` against
      Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestApprovalRequire -count=1`
      — expect `ok`.
- [ ] `go test -trimpath ./... -count=1` — green, boot checks included.
- [ ] Commit: `Ad-hoc approval requirements over API and CLI (029 §7.2)`.

### Task 12 — e2e: a story flow's lanes through public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [7, 9, 10, 11]
```

`e2e/approval_flows_test.go` (build tag `e2e`), on the `smoke_test.go`
harness and public surfaces only:

1. Bootstrap token → create a project via `/api/v1/projects`.
2. `POST /api/v1/projects/{id}/approval-flow` with `{"name":"story"}` →
   200; the response names flow `story` rev `1`.
3. `POST /api/v1/projects/{id}/deliverables` named `Methodology` →
   `GET /reviews` lists two rows, `methodology/science-lead` and
   `methodology/domain-expert`, both showing "No revision designated for
   review yet." and no decide form.
4. `POST /api/v1/approvals` filing an ad-hoc `task` requirement with an
   explicit revision → `GET /reviews` shows the row with its decide form;
   POST the decide with no session → 403 and the row stays `awaiting`
   (part 1's gate holds for the new kinds on the real wire — the e2e stack
   runs `LODE_WEB_OPEN=true` with no OIDC issuer, and the
   session-authenticated success path is already proven in `internal/api`
   against the fake issuer).
5. Re-apply the flow → `Materialized: 0`; re-file the ad-hoc row → the
   same row back. Idempotence over the wire.

```go
//go:build e2e
```

- [ ] `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestApprovalFlow`
      against Postgres — expect `ok`.
- [ ] Full suite: `make test-e2e` — green.
- [ ] Commit: `e2e: story flow materializes, queues, and gates over public surfaces`.

## Verification

- `make test` green with Postgres reachable (a silent skip proved
  nothing); `make test-e2e` green.
- `curl -s localhost:9090/metrics | grep -E 'worklode_approval_(flow_applied|requirements)_total'`
  shows both new families after exercising the flows.
- `docker compose up -d`, apply `story` to a project, create a
  `Methodology` deliverable, and open `/reviews`: two lanes, no decide
  form, "No revision designated for review yet."
- `lode doc anchors docs/plans/2026-08-25-approvals-2-flows-and-requirements.md`
  reports no errors.

## What this plan does not do — part 3 of this series, stated so the gap is a decision

- Revision designation and re-binding: a deliverable designating a commit
  or evidence revision, reopen-on-material-change, and the explicit impact
  review (029 §7.1). Until then, flow-minted deliverable rows stay
  undecidable at `subject_revision ''` — visibly awaiting, deliberately.
- The policy-permitted self-review exception (029 §7.1) — self-approval
  stays unconditionally refused, now for every entity kind.
- The CI gate and the CMS gate (029 §7.3 bullets 2–3), including the
  `/api/v1` approval read the CI gate queries.
- The analysis evidence bundle's contents and presentation — exact commit,
  environment lock, entry point, dataset snapshots and lineage, the diff
  from the previously reviewed revision (032 §7).
