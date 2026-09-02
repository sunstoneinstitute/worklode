---
status: accepted
covers:
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.1
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-approvals-1-table-and-web-act.md
      - docs/plans/2026-08-25-approvals-2-flows-and-requirements.md
  - spec: docs/specs/029-research-work-in-the-backbone.md#sec-7.3
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-14-approvals-1-table-and-web-act.md
  - spec: docs/specs/032-project-cockpit.md#sec-7
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-25-research-work-1-milestones.md
      - docs/plans/2026-08-25-approvals-2-flows-and-requirements.md
  - spec: docs/specs/032-project-cockpit.md#sec-10
    coverage: none
  - spec: docs/specs/032-project-cockpit.md#sec-11
    coverage: none
blockedBy:
  - 2026-08-25-approvals-2-flows-and-requirements.md
  - 2026-08-25-research-work-2-identifiers-and-references.md
---

# Approvals part 3 — revision binding and the gates

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** the revision-binding half of 029 §7.1 becomes enforced rather than
recorded — a material change reopens exactly the changed target, dependent
objects get an explicit impact review instead of blanket invalidation, the
governed reference chain an approval covered stays queryable, and the
policy-permitted self-review exception replaces part 1's unconditional
refusal — and the remaining §7.3 gates land: a `GET /api/v1/approvals` read
route, the CI prod-publish gate wired to it, and 025 §7.3's per-document
reviewer set converted to approvals rows. This closes the approvals series.

**The load-bearing property** (029 §7.1): every decision stays attached to
the exact revision the actor saw. A later commit, document version, or PR
push never inherits an approval — when the entity designates a newer
revision, the old decision keeps its revision and the new revision is a
*visible* unreviewed `awaiting` row. Part 1's property (a missing approval is
a row) extends to revisions: a stale approval is a resolved row beside an
open one, never a silently updated row.

**Series:** part 3 of 3. Part 1 (`2026-08-14-approvals-1-table-and-web-act`,
shipped) built the table, the PR ingest, and the session-gated decide act.
Part 2 (`2026-08-25-approvals-2-flows-and-requirements`, `blockedBy:` above)
owns 029 §7.2: flows, the rule engine, the `projects.approval_flow` snapshot
columns, widening `approvals.entity_kind` beyond `'pr'`, and the multi-lane
unique key. This plan assumes all of that exists and rebuilds none of it.

**One prose dependency this frontmatter cannot carry:** governed references
(Task 3) are rows in the `entity_edges` table that
`2026-08-25-research-work-2-identifiers-and-references` creates (migration
0055). That plan is being authored in parallel, so the edge is stated here
the way part 1 stated its plan-B dependency: Tasks 3, 6, and 9 carry a
verify-before-starting block, and the executor stops rather than stubbing
the table.

**The `defers:` entry:** the CMS half of §7.3 — the Payload publish button,
the per-post binding UI, the advisory degradation when worklode is down — is
code in the `sunstone-cms` repo, recorded per 026 §5.3 so the section reports
`deferred` with an owner instead of blending into unplanned. What worklode
owes that repo is exactly Task 8's stable read endpoint plus the documented
opt-in-per-post contract (bind by `entity_kind`/`entity_id`, treat any
non-200 or unreachable backbone as advisory, never hard-depend on it).

**Coverage gaps, declared:** for 029 §7.1, this plan finishes revision
binding (reopen, impact review, governed chain, the exception flow); the
table and the default refusal were part 1, requirement creation is part 2 —
hence `partial` with both named. For 029 §7.3, this plan delivers the CI
gate, the read endpoint, and the reviewer-set conversion; the web act and PR
ingest were part 1; the CMS half is the `defers:` entry. For 032 §7, this
plan builds the approval detail view — the evidence bundle, review graph,
and exception/impact facts beside the decision; deliverable readiness
context is `research-work-1-milestones`, lane creation is `approvals-2`.
032 §10 binds every page here (`./scripts/narrow-check.sh` measures them)
while this plan builds no accessibility machinery, and 032 §11 binds the e2e
style while its release demonstration belongs to no single plan: both
`none`.

**Tech stack:** Go 1.26, `net/http` mux, pgx against Postgres,
`templ`-rendered pages, Prometheus client, GitHub Actions YAML. Store and
`internal/api` tests need Postgres with pgvector.

**Read first:**
- `docs/specs/inlined/029-research-work-in-the-backbone.md` §7.1–§7.3
- `docs/specs/032-project-cockpit.md` §7
- `docs/specs/inlined/025-documents-in-the-backbone.md` §7.3 (the reviewer
  set and accept gate Task 11 converts)
- `internal/store/approvals.go` and `approval_rules.go` — part 1's tx
  functions and pure rules; everything here composes them
- `internal/hooks/github.go` — `applyPullRequest`'s action switch,
  `openApproval`, `resolveApprovalForReview`
- `internal/api/webform.go` — `decideApproval` and the `RecordEvent` write
  pattern; `internal/api/router.go` — `routeGuards`
- `internal/watcher/doclifecycle.go` — the pure-rule/executor split Task 11
  extends; `internal/api/docwatch.go` — the executor
- `.github/workflows/promote-prod.yml` — the manually-triggered prod publish
  workflow Task 10 gates

## Global Constraints

- **Exact spellings, quoted once.** `approvals.review_kind` ∈ `'review'`,
  `'impact'` (default `'review'`). A revision-bound entity reference is
  `<entity-id>@<revision>` (e.g. `COW-DEL-2@9f31c8e`), rendered only by
  `store.RevisionRef`; the governed-edge `rel` is `references_revision`. An
  impact row's `subject_revision` is
  `<dependent-revision>+<upstream-id>@<upstream-revision>` (e.g.
  `3+COW-DEL-2@9f31c8e`), rendered only by `store.ImpactRevision` — the pair
  under review, and what keeps repeated upstream moves from colliding on the
  unique key. Document approvals: `entity_kind = 'doc'`, `entity_id` = the
  doc IRI (`wlid:doc/<kind>-<project>-<number>`, plans by slug — the
  existing `store` spelling at `internal/store/docs.go:2648`),
  `subject_revision` = the decimal document version (`"3"`).
- **Routes and permissions.** `GET /approvals/{id}` → `permWebRead`.
  `GET /api/v1/approvals` → new `permApprovalRead = "approval.read"`.
  `POST /api/v1/approvals/designate` → new
  `permApprovalDesignate = "approval.designate"`.
  `POST /approvals/{id}/note` → new `permApprovalNote = "approval.note"`,
  session-gated. `POST /approvals/{id}/exception` → existing
  `permApprovalDecide`, session-gated. All new permissions get `grants`
  entries `{RoleUser, RoleAdmin}` — who may act on a *given* approval is a
  per-row fact the store checks, not a role.
- **Events.** Web acts and API mutations wrap their store write in
  `RecordEvent` exactly as `decideApproval` does: `approval.impact_noted`
  and `approval.exception_authorized` (source `web`),
  `approval.revision_designated` (source matching what
  `internal/api/deliverables.go`'s `recordDeliverable` uses for bearer-token
  writes), `approval.awaiting_materialized` (source `system`, the doc
  watcher). The PR ingest's writes ride the GitHub delivery's existing
  `RecordEvent` transaction and add no event of their own.
- **Metrics** (spec 022): extend, do not duplicate.
  `worklode_approvals_ingest_total{action}` gains actions `rebound`,
  `candidate`, `impact_opened`. `worklode_approval_decisions_total`'s
  `outcome` gains `refused_prior`. New counter
  `worklode_approval_acts_total{act,outcome}` in `internal/api/metrics.go`,
  `act` ∈ `{note, exception, designate}`, `outcome` ∈ `{applied, refused,
  not_found, invalid, error}`. Nil-safe, pre-initialised, bounded labels
  only — never an entity id, actor, or group.
- **Decide stays session-only; reads and designation are not decisions.**
  The gate reads (`GET /api/v1/approvals`) and the designation act are
  bearer-token surfaces — CI has no browser. Deciding, noting, and
  authorizing an exception remain web-session acts behind `requireSession`.
- **Migrations:** this plan owns exactly one pair, `0061` (nominal;
  `./scripts/check-migrations.sh` renumbers on collision), listed in
  `deploy/base/kustomization.yaml`; never edit a shipped migration —
  including part 2's.
- **One model (ADR 036):** the shapes `GET /api/v1/approvals` serializes are
  declared in `internal/model`; `internal/store` keeps its scan plumbing
  package-local.
- **Store tests need Postgres with pgvector** (`store.OpenTestStore`); a
  silent skip proved nothing. **`e2e/` drives public surfaces only.**
- **UI toolchain** is fixed by 032 §12: templ components compiled by
  `go generate ./...`, Tailwind styles compiled to `internal/ui/assets/app.css`
  by the pinned CLI; both generated artifacts are committed. Every new page
  passes `./scripts/narrow-check.sh` (032 §10 is measured, not aspirational).
- **Every task leaves `go test ./...` green** and ends with a commit.

## Decisions this plan executes (made in the approved design; do not reopen)

- **Designating a revision has three outcomes, decided by one pure rule.** A
  row already binding the revision → no-op (redelivery-safe). An open review
  row at an older revision → the row *rebinds* to the new revision: the
  requirement was never decided, and the decision must bind what the
  reviewer will actually see. Only decided history → a new `awaiting`
  *candidate* row copying `required_role`/`required_actor` from the newest
  decided row; the decided row keeps its revision. No rows at all → no-op:
  requirements are created by part 2's engine and part 1's ingest, never
  conjured by a push.
- **For PRs, a `synchronize` delivery is the designation.** The PR's
  governed revision is its head; a push designates the new head. Part 1's
  "a review resolves the open row however far the head moved" survives as
  the rebind outcome — one open row, now honestly labelled with the head it
  governs.
- **Impact reviews are approvals rows** (`review_kind = 'impact'`) on the
  dependent entity, materialized at designation time — visible awaiting
  work, per §7.1's philosophy — for every dependent holding an `approved`
  review row. The downstream owner supplies the note as a separate act; the
  decision rides the existing decide route: `approve` = the prior decision
  still holds, `request_changes`/`reject` = reopen, which also inserts a new
  `awaiting` review row on the dependent at its bound revision. Only a
  qualified prior approver (resolved an `approved` review row on that
  entity, and holds the impact row's `required_role`) may decide one.
- **Governed references are `entity_edges` rows with revision-bound
  endpoints** (`from_id`/`to_id` carry `RevisionRef` spellings, `rel =
  'references_revision'`). The table fits: it lacks a revision column, but
  the reference is *to a revision*, so the revision belongs in the endpoint
  identity — no new table, no column added to a table this plan's `covers:`
  does not motivate. Dependents of an entity match `to_id` on
  `<id>` or `<id>@%`.
- **0061 appends `review_kind` to the approvals uniqueness.** Without it, an
  impact row on a dependent's already-approved `(kind, id, revision)` would
  vanish into `ON CONFLICT DO NOTHING`. Part 2 owns the multi-lane key
  shape, so the migration reads the live schema and recreates whatever
  unique index it finds with `review_kind` appended, rather than assuming a
  column list.
- **The self-review exception is two recorded facts, both required.** The
  effective policy (part 2's `projects.approval_flow` snapshot) must allow
  it, and a different authorized actor must approve it before review —
  stored in `exception_authorized_by`, event-logged, and rendered beside the
  decision (032 §7). If the snapshot is absent or defines no self-review
  field, policy never allows; the exception path stays dormant, which is
  part 1's refusal unchanged.
- **The CI gate fails closed.** `promote-prod.yml` — this repo's
  manually-triggered prod publish workflow, and the reference wiring for
  story/analysis repos — takes required approval-entity inputs and fails
  when no `approved` row matches or worklode is unreachable. Advisory
  degradation is the CMS gate's property only (029 §7.3). No reusable
  workflow yet: the read endpoint is the stable contract; a second repo
  copies the one gate step.
- **025 §7.3's reviewer set *is* approvals rows.** Submitting a document
  materializes an awaiting `doc` row at that version; accepting is refused
  while any open row exists for the current version. Naming individual
  reviewers is adding rows — part 2's ad-hoc requirement surface, not
  rebuilt here.
- **No CLI verb.** The gates consume `GET /api/v1/approvals` with curl; the
  humans decide in the web UI. `lode` grows nothing in this plan.

## Tasks

### Task 1 — Migration 0061: review_kind, note, exception_authorized_by

```yaml
kind: feature
priority: high
skills:
  - golang-migrate:migration
  - golang-migrate:test-roundtrip
  - worklode-migrations
blockedBy: []
```

Create `deploy/base/migrations/0061_approval_revision_binding.up.sql` /
`.down.sql` (number nominal):

```sql
-- 029 §7.1 revision binding. An impact review is an approvals row too, so
-- "what is waiting on whom" stays one query over rows that exist.
-- review_kind separates an ordinary review from a dependent-object impact
-- review; note carries the downstream owner's impact note (§7.1: "the
-- downstream owner supplies an impact note"); exception_authorized_by is
-- the actor who approved a policy-permitted self-review before review —
-- one of the two facts 032 §7 renders beside the decision.
ALTER TABLE approvals ADD COLUMN review_kind text NOT NULL DEFAULT 'review'
    CHECK (review_kind IN ('review', 'impact'));
ALTER TABLE approvals ADD COLUMN note text;
ALTER TABLE approvals ADD COLUMN exception_authorized_by text
    REFERENCES actors (id) ON DELETE RESTRICT;
```

Then recreate the approvals uniqueness with `review_kind` appended.
**Read the live schema first** (`\d approvals` against a migrated scratch
database): part 2's 0057/0058 replaced part 1's
`UNIQUE (entity_kind, entity_id, subject_revision)` with its multi-lane key,
and this migration must drop *that* index by its actual name and recreate it
with its actual column list plus `review_kind` — do not transcribe a guessed
column list from this plan. Down: drop the widened index, restore the one
part 2 left, drop the three columns.

- [ ] Write both files; add all four lines under `worklode-migrations` in
      `deploy/base/kustomization.yaml`.
- [ ] `./scripts/check-migrations.sh --no-fix` — expect exit 0 (or accept
      the renumber and update the filename above in your commit).
- [ ] Roundtrip (golang-migrate:test-roundtrip): up → down → up applies
      cleanly against a scratch database.
- [ ] `go test -trimpath ./internal/store -run TestMigrations -count=1` —
      expect `ok`.
- [ ] Commit: `Approvals revision-binding columns and key (029 §7.1)`.

### Task 2 — Pure rules: revision outcomes, prior approvers, impact effect, exception validity

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: []
```

Extend `internal/store/approval_rules.go` + `approval_rules_test.go` with
the four pure decisions everything downstream composes. Table-tested, no
database — Tasks 3–7 and 11 all route through these, so the reopen and
impact semantics are pinned before any I/O exists.

```go
// RevisionOutcome is what designating a new revision does to an entity's
// approval history (029 §7.1).
type RevisionOutcome int

const (
	// RevisionNoop: a row already binds this revision, or nothing was ever
	// required — a push never conjures a requirement.
	RevisionNoop RevisionOutcome = iota
	// RevisionRebind: the open review row moves to the new revision. The
	// requirement was never decided; the decision must bind what the
	// reviewer will see.
	RevisionRebind
	// RevisionCandidate: only decided history exists. A new awaiting row is
	// the visibly unreviewed candidate; the decided rows keep their exact
	// revisions.
	RevisionCandidate
)

// OnNewRevision decides the outcome. open is the entity's open review-kind
// row (nil when none); hasDecided reports whether any decided review-kind
// row exists; boundAlready reports whether any review-kind row (open or
// decided) already carries exactly the new revision.
func OnNewRevision(open *Approval, hasDecided, boundAlready bool) RevisionOutcome

// PriorApprover reports whether actorID is the resolving actor of an
// 'approved' review-kind row in history (029 §7.1: "a qualified prior
// approver confirms the existing decision still holds or reopens it").
// Impact rows in history prove nothing and are ignored.
func PriorApprover(history []Approval, actorID string) bool

// ImpactEffect maps a decision state recorded on an impact row to its side
// effect on the dependent's review row.
type ImpactEffect int

const (
	ImpactConfirm ImpactEffect = iota // approved: the prior decision holds
	ImpactReopen                      // changes_requested | rejected: reopen
)

func ImpactDecisionEffect(state string) ImpactEffect

// SelfReviewExceptionValid reports whether an author may decide their own
// work (029 §7.1): only when the effective review policy allows it AND a
// different authorized actor approved the exception before review.
// authorizedBy == the decider is not "a different actor".
func SelfReviewExceptionValid(policyAllows bool, authorizedBy *string, decider string) bool
```

First test, verbatim shape:

```go
func TestOnNewRevision(t *testing.T) {
	open := &store.Approval{State: "awaiting", SubjectRevision: "aaa111"}
	cases := []struct {
		name         string
		open         *store.Approval
		hasDecided   bool
		boundAlready bool
		want         store.RevisionOutcome
	}{
		{"already bound is a noop", open, true, true, store.RevisionNoop},
		{"open row rebinds", open, false, false, store.RevisionRebind},
		{"open row rebinds even with decided history", open, true, false, store.RevisionRebind},
		{"decided only mints a candidate", nil, true, false, store.RevisionCandidate},
		{"no history is a noop", nil, false, false, store.RevisionNoop},
	}
	for _, c := range cases {
		if got := store.OnNewRevision(c.open, c.hasDecided, c.boundAlready); got != c.want {
			t.Errorf("%s: OnNewRevision = %v, want %v", c.name, got, c.want)
		}
	}
}
```

Also table-test: `PriorApprover` (approved review row match, impact row
ignored, changes_requested ignored, nil resolving_actor, empty history);
`ImpactDecisionEffect` over the three states; `SelfReviewExceptionValid`
(policy off, nil authorizer, authorizer == decider, valid case).

- [ ] `go test -trimpath ./internal/store -run 'TestOnNewRevision|TestPriorApprover|TestImpactDecisionEffect|TestSelfReviewException' -count=1`
      — expect `ok` (no Postgres needed).
- [ ] Commit: `Pure revision, impact, and exception rules (029 §7.1)`.

### Task 3 — Store: designation, entity history, governed references

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 2]
```

**Cross-plan dependency — verify before starting:** the reference helpers
write `entity_edges`, created by
`2026-08-25-research-work-2-identifiers-and-references` (migration 0056). If
the table is absent from a migrated scratch database when this task runs,
stop and escalate — do not create it here.

In `internal/store/approvals.go` (+ tests), the tx-scoped core every
designation surface calls:

```go
// RevisionRef renders a revision-bound entity reference, "<id>@<revision>".
// The one spelling for governed-edge endpoints and the CI gate's queries.
func RevisionRef(entityID, revision string) string

// ImpactRevision renders an impact row's subject_revision:
// "<dependentRevision>+<upstreamID>@<upstreamRevision>" — the pair the
// impact decision reviews, unique per upstream move.
func ImpactRevision(dependentRevision, upstreamID, upstreamRevision string) string

// ListApprovalsForEntity returns every row for (kind, id), newest first —
// the shared history for the detail page, the impact checks, and the read
// API. Includes the 0061 columns.
func ListApprovalsForEntity(tx *sql.Tx, entityKind, entityID string) ([]Approval, error)

// DesignateRevision applies OnNewRevision inside the caller's event
// transaction: rebinds the open review row's subject_revision, or inserts
// a candidate row copying required_role/required_actor from the newest
// decided review row. Returns the outcome for the caller's metric.
func DesignateRevision(tx *sql.Tx, now time.Time,
	entityKind, entityID, newRevision string) (RevisionOutcome, error)

// GovernedRef is one revision-bound reference the designation recorded.
type GovernedRef struct {
	Kind, ID, Revision string
}

// InsertGovernedRefs writes rel='references_revision' entity_edges rows
// from RevisionRef(fromID, fromRevision) to each RevisionRef(ref.ID,
// ref.Revision). Idempotent on the table's primary key.
func InsertGovernedRefs(tx *sql.Tx, now time.Time, createdBy *string,
	fromKind, fromID, fromRevision string, refs []GovernedRef) error

// GovernedRefsFor returns the references recorded from (kind, id) at
// revision, and DependentsOf the distinct entities holding a
// references_revision edge pointing at (kind, id) at any revision — the
// impact fan-out set.
func GovernedRefsFor(tx *sql.Tx, kind, id, revision string) ([]GovernedRef, error)
func DependentsOf(tx *sql.Tx, kind, id string) ([]GovernedRef, error)
```

Also add ctx (`*Store`) read variants where the API needs them, and extend
`scanApproval`/`approvalColumns` (and the `Approval` struct) with
`ReviewKind`, `Note *string`, `ExceptionAuthorizedBy *string` — every
existing caller compiles and its tests still pass.

First test (Postgres, the part-1 `mustBegin` pattern):

```go
func TestDesignateRevisionMintsCandidateAfterDecision(t *testing.T) {
	s := store.OpenTestStore(t)
	tx := mustBegin(t, s)
	now := time.Now().UTC()
	role := "science-leads"
	mustInsertAwaiting(t, tx, now, "pr", "acme/site#7", "aaa111", &role, nil)
	ap := mustOpenApproval(t, tx, "pr", "acme/site#7")
	mustResolve(t, tx, ap.ID, "approved", now)

	out, err := store.DesignateRevision(tx, now, "pr", "acme/site#7", "bbb222")
	if err != nil {
		t.Fatal(err)
	}
	if out != store.RevisionCandidate {
		t.Fatalf("outcome = %v, want RevisionCandidate", out)
	}
	rows, err := store.ListApprovalsForEntity(tx, "pr", "acme/site#7")
	if err != nil {
		t.Fatal(err)
	}
	// The approved row keeps aaa111; the candidate is awaiting at bbb222
	// and inherited the role requirement.
	if len(rows) != 2 || rows[0].State != "awaiting" ||
		rows[0].SubjectRevision != "bbb222" || rows[0].RequiredRole == nil ||
		rows[1].SubjectRevision != "aaa111" || rows[1].State != "approved" {
		t.Errorf("unexpected history: %+v", rows)
	}
}
```

Also cover: rebind moves the open row and leaves history length 1;
designating the already-bound revision twice is a no-op; no-history no-op;
`InsertGovernedRefs` then `GovernedRefsFor` round-trips and re-insert is
idempotent; `DependentsOf` finds the referrer whichever revision its edge
names, and returns each dependent once.

- [ ] `go test -trimpath ./internal/store -run 'TestDesignate|TestGovernedRef|TestDependentsOf|TestApproval' -count=1`
      against Postgres — expect `ok`, not a skip.
- [ ] Commit: `Revision designation, history, and governed references (029 §7.1)`.

### Task 4 — PR ingest: synchronize designates the new head

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

In `internal/hooks/github.go`, a new branch in `applyPullRequest`'s action
switch (the payload already parses `head.sha`; no new fields):

```go
case action == "synchronize":
	// A push to the PR branch designates the new head as the governed
	// revision (029 §7.1). An open row rebinds; a decided PR gets a
	// visibly unreviewed candidate row. Correlation never fails the
	// delivery.
	outcome, err := store.DesignateRevision(tx, now, "pr",
		store.PREntityID(repo, gh.Number), gh.Head.SHA)
	if err != nil {
		return err
	}
	switch outcome {
	case store.RevisionRebind:
		a.metrics.approvalIngest("rebound")
	case store.RevisionCandidate:
		a.metrics.approvalIngest("candidate")
	}
	return nil
```

The branch runs only when `pr.TaskID != nil` (it sits below the existing
guard). Extend the `worklode_approvals_ingest_total` bounded action set in
`internal/hooks/metrics.go` with `rebound`, `candidate`, and (for Task 6)
`impact_opened`; extend its metrics test.

Tests in `internal/hooks/github_test.go` with a `pr_synchronize.json`
fixture modelled on the existing `testdata/github/` PR fixtures (same
repo/number, new `head.sha`):

- opened → synchronize: still one row, `subject_revision` is the new head
  (rebind), state `awaiting`;
- opened → approved review → synchronize: two rows, the approved row keeps
  the old head, a new `awaiting` row carries the new head;
- synchronize on an uncorrelated PR writes nothing;
- redelivered synchronize (same head) changes nothing;
- metric increments per outcome.

- [ ] `go test -trimpath ./internal/hooks -count=1` against Postgres —
      expect `ok`.
- [ ] Commit: `PR synchronize designates the new head revision (029 §7.1)`.

### Task 5 — The approval detail page

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
  - worklode-cockpit-ui
blockedBy: [3]
```

The tracer, and 032 §7's presentation surface: `GET /approvals/{id}` renders
one approval with everything an actor needs to trust or revisit it.
Read-only in this task — Tasks 6 and 7 add their forms here once their
routes exist.

- `internal/ui/approvals.templ`: `ApprovalDetail(v ApprovalDetailView)` in
  the global shell. Renders: the entity (kind, id) with its jump-out link
  (PR URL for `pr` — GitHub stays the review surface, 029 §7.3; the
  cockpit doc page for `doc`); the bound `subject_revision`, stated
  verbatim; state, decided-by, decided-at; the full decision history from
  `ListApprovalsForEntity` — each row with its exact revision, so a stale
  approval beside an open candidate reads as exactly that; the governed
  references (`GovernedRefsFor`) as the review-graph list 032 §7 asks for,
  each rendered as `kind id @ revision`; for `pr` rows with a decided
  predecessor, the diff-from-previous as a GitHub compare jump-out link
  (`<pr-url-base>/compare/<prev>...<current>` derived from the stored PR
  URL); the impact note and `exception_authorized_by` display name when
  present (facts beside the decision — written by Tasks 6 and 7, rendered
  from this task on). Honest empties throughout: an analysis bundle shows
  the references the designation recorded, never a fabricated lineage.
- `internal/api/web.go`: `approvalPage` handler — `GetApproval`,
  `ListApprovalsForEntity`, `GovernedRefsFor`, the per-kind join for
  title/URL (reuse the part-1 queue reader's join constants); 404 on an
  unknown id. `internal/api/render.go`: the view mapping.
  `internal/api/server.go`: `r.web("GET /approvals/{id}", s.navWrap("reviews", s.approvalPage))`;
  `routeGuards` row `guarded(permWebRead)`.
- `/reviews` queue rows link each row's age/state cell to its detail page.
  **Cross-plan note:** part 2 generalizes the queue reader beyond the PR
  join; whatever entity kinds it lists when this task runs, the detail
  handler must render them without a PR row (nil-safe joins), because doc
  rows arrive in Task 11.

First test, `internal/api/web_test.go` (existing `newTestServer` harness,
store-seeded like sibling web tests):

```go
func TestApprovalDetailShowsRevisionHistory(t *testing.T) {
	st, h := newTestServer(t)
	id := seedDecidedThenCandidate(t, st, "acme/site#7", "aaa111", "bbb222")

	body := getOK(t, h, fmt.Sprintf("/approvals/%d", id))
	for _, want := range []string{"aaa111", "bbb222", "awaiting", "approved"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page missing %q", want)
		}
	}
}
```

Also cover: 404 on unknown id; governed references render; the compare link
appears only when a decided predecessor exists; one `aria-current="page"`.

- [ ] `go generate ./...`; commit regenerated `*_templ.go` (and `app.css`
      if the stylesheet changed).
- [ ] `./scripts/narrow-check.sh` — the new page passes at 320/375/768 px.
- [ ] `go test -trimpath ./internal/api -run 'TestApprovalDetail|TestWeb|TestReviews' -count=1`
      — expect `ok`.
- [ ] Commit: `Approval detail page: revision history and review graph (032 §7)`.

### Task 6 — The impact review lifecycle

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3, 5]
```

029 §7.1's explicit impact review, end to end: fan-out on designation, the
owner's note act, and the prior-approver decision — reusing the decide route
so there is exactly one way a row gets decided.

**Store** (`internal/store/approvals.go` + tests):

1. Extend `DesignateRevision`: on `RevisionRebind` or `RevisionCandidate`,
   for each `DependentsOf(kind, id)` entity holding an `approved`
   review-kind row, insert an `awaiting` impact row — `review_kind
   'impact'`, `subject_revision = ImpactRevision(dependent's approved
   revision, upstreamID, newRevision)`, `required_role` copied from the
   dependent's approved row, `required_actor` nil. An existing open impact
   row on the dependent absorbs the change (the insert conflicts, DO
   NOTHING). Return how many opened so Task 4's caller can count
   `impact_opened` per row.
2. `SetImpactNote(tx *sql.Tx, id int64, note string) error` — fills `note`
   on an *open* impact row; `ErrApprovalResolved` on a decided one,
   `ErrInvalidInput` on a review-kind row or empty note.
3. `DecideApproval` grows the impact branch: for `review_kind == 'impact'`,
   the decider must satisfy `PriorApprover(history, in.ActorID)` (new
   sentinel `ErrNotPriorApprover`) in addition to `QualifiedForRole`; after
   resolving, `ImpactDecisionEffect`: `ImpactConfirm` touches nothing else;
   `ImpactReopen` inserts a new `awaiting` review row on the dependent at
   the revision its approved row bound. The PR self-approval branch does not
   apply to impact rows.

**Web**: `POST /approvals/{id}/note` — handler in `webform.go` following
`decideApproval` (same-origin, `parseWebForm`, `requireSession`,
`RecordEvent` `approval.impact_noted` source `web`, redirect back to the
detail page); `routeGuards` row `guarded(permApprovalNote)`; permission and
grants entry per Global Constraints. `requireSession` currently hardcodes
`permApprovalDecide` in its denial telemetry — parameterize it
(`requireSession(perm Permission, next)`) and update the part-1 call site in
the same change. The detail page (Task 5's template) gains, on an open
impact row: the note textarea + submit, and the standard decide buttons
relabelled for the impact question ("Confirm decision holds" → `approve`,
"Reopen" → `request_changes`). Error mapping and metrics: `note` acts count
in `worklode_approval_acts_total`; the decide path's new refusal maps
`ErrNotPriorApprover` → 403 and outcome `refused_prior`.

Tests: store — fan-out opens one impact row per approved dependent and none
for undecided ones; repeated designation absorbs into the open impact row; a
confirm leaves the dependent's approved row untouched; a reopen mints the
dependent's awaiting review row; a non-prior-approver decide fails with
`ErrNotPriorApprover`; note on decided row refused. Web — note act 303s and
persists, records one event; note without session 403s; impact decide by a
seeded prior approver succeeds through the existing route.

- [ ] `go generate ./...`; commit regenerated artifacts.
- [ ] `go test -trimpath ./internal/store ./internal/api -run 'TestImpact|TestDecideApproval|TestRequireSession' -count=1`
      against Postgres — expect `ok`.
- [ ] `go test -trimpath ./... -count=1` — green (router boot checks prove
      the route/guard pair).
- [ ] Commit: `Impact reviews: fan-out, owner note, prior-approver decision (029 §7.1)`.

### Task 7 — The policy-permitted self-review exception

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 5]
```

Part 1 refuses author self-approval unconditionally
(`internal/store/approvals.go`, the `entity_kind == "pr"` branch of
`DecideApproval`). 029 §7.1 allows an exception only when the effective
policy permits it AND a different authorized actor approves it before
review; both facts are event-logged and rendered beside the decision.

**Store**:

```go
// AuthorizeSelfReviewException stamps exception_authorized_by on an open
// row (029 §7.1). Refused when: the row is decided (ErrApprovalResolved);
// the effective policy does not allow self-review (ErrPolicyForbids); the
// authorizer is the entity's author (ErrSelfApproval — you cannot authorize
// your own exception); or already authorized (ErrInvalidInput).
func AuthorizeSelfReviewException(tx *sql.Tx, id int64, actorID string) error
```

The policy read: resolve the row's project (for `pr` via
`pull_requests → tasks`; for `doc` via the doc row — extract a small
`projectForApproval` helper), then read the self-review permission from the
`projects.approval_flow` snapshot part 2 landed. **Check part 2's shipped
snapshot shape when this task runs** (its model type in `internal/model`)
and read the field it defines for self-review permission; if part 2 defined
none, `ErrPolicyForbids` always — dormant, and exactly part 1's behavior.

`DecideApproval`'s self-approval refusal becomes: refuse unless
`SelfReviewExceptionValid(policyAllows, a.ExceptionAuthorizedBy,
in.ActorID)` — the pure rule from Task 2, which already rejects an
authorizer equal to the decider.

**Web**: `POST /approvals/{id}/exception` — `requireSession`, guarded by
`permApprovalDecide` (authorizing an exception is a decision-grade act),
`RecordEvent` `approval.exception_authorized` source `web`, act metric
`exception`. The detail page shows, on an open row whose policy allows
self-review, an "Authorize self-review exception" button; and beside any
decision made under an exception, both facts: "self-review permitted by
flow <approval_flow_name>@<approval_flow_rev>" and "exception approved by
<display name>" (032 §7).

Tests: store — authorize then self-decide succeeds; self-decide without
authorization still `ErrSelfApproval`; authorizer == author refused;
policy-off refused; decided-row refused. Web — the act 303s, writes the
event, and the detail page renders both facts.

- [ ] `go generate ./...`; commit regenerated artifacts.
- [ ] `go test -trimpath ./internal/store ./internal/api -run 'TestSelfReview|TestAuthorizeException|TestDecideApproval' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Policy-permitted self-review exception (029 §7.1)`.

### Task 8 — model.Approval and the gate read: GET /api/v1/approvals

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1, 3]
```

There is no `/api/v1` approvals read today; this route is what the CI and
CMS gates query, so its shape is a contract.

**Model** (`internal/model/approval.go`, ADR 036 — wire names, stdlib only):

```go
// Approval is one row of the approvals table as it crosses the HTTP
// boundary — the shape the CI and CMS publish gates query (029 §7.3).
type Approval struct {
	ID                    int64      `json:"id"`
	EntityKind            string     `json:"entity_kind"`
	EntityID              string     `json:"entity_id"`
	SubjectRevision       string     `json:"subject_revision"`
	ReviewKind            string     `json:"review_kind"`
	RequiredRole          *string    `json:"required_role,omitempty"`
	RequiredActor         *string    `json:"required_actor,omitempty"`
	ResolvingActor        *string    `json:"resolving_actor,omitempty"`
	State                 string     `json:"state"`
	Note                  *string    `json:"note,omitempty"`
	ExceptionAuthorizedBy *string    `json:"exception_authorized_by,omitempty"`
	CreatedAt             time.Time  `json:"created_at"`
	ResolvedAt            *time.Time `json:"resolved_at,omitempty"`
}

// ApprovalListResponse is the response body of GET /api/v1/approvals.
type ApprovalListResponse struct {
	Approvals []Approval `json:"approvals"`
}
```

**API** (`internal/api/approvals.go`): handler for
`GET /api/v1/approvals?entity_kind=&entity_id=&subject_revision=&state=`.
`entity_kind` and `entity_id` are required — 400 without both; an
unfiltered dump is not the gate's question and is unbounded. Optional
`subject_revision` and `state` narrow further. Backed by a `*Store` variant
of `ListApprovalsForEntity` that applies the optional filters in SQL and
maps to `model.Approval`. `routeGuards` row
`"GET /api/v1/approvals": guarded(permApprovalRead)`; permission and grants
per Global Constraints. Route metrics ride the existing HTTP
instrumentation; no new family.

**The CMS contract, stated where the CMS team will find it:** a short
comment block on the handler naming what `sunstone-cms` relies on (see
`defers:`): the query shape above; `200` with an `approvals` array (possibly
empty — an empty array means no requirement was ever materialized, which the
opt-in binding treats as "not governed"); the gate question is "does any row
with `review_kind == "review"` and `state == "approved"` match the bound
entity (and revision, when the post pins one)"; any non-200 or unreachable
backbone degrades to advisory on the CMS side, never a hard failure.

First test (`internal/api/approvals_test.go`, bearer-token harness the
sibling `/api/v1` tests use):

```go
func TestListApprovalsFiltersByEntityAndRevision(t *testing.T) {
	st, h, token := newAPITestServer(t)
	seedDecidedThenCandidate(t, st, "acme/site#7", "aaa111", "bbb222")

	var resp model.ApprovalListResponse
	getJSON(t, h, token,
		"/api/v1/approvals?entity_kind=pr&entity_id=acme/site%237&subject_revision=aaa111&state=approved",
		&resp)
	if len(resp.Approvals) != 1 || resp.Approvals[0].SubjectRevision != "aaa111" {
		t.Fatalf("want exactly the approved aaa111 row, got %+v", resp.Approvals)
	}
}
```

Also cover: both rows without the revision filter; 400 when `entity_kind`
or `entity_id` is missing; 401 without a token; empty array (not 404) for
an unknown entity.

- [ ] `go test -trimpath ./internal/api -run TestListApprovals -count=1`
      against Postgres — expect `ok`.
- [ ] `go test -trimpath ./internal/model -count=1` — `rule_test.go` and
      `deps_test.go` accept the new file.
- [ ] Commit: `GET /api/v1/approvals: the gate read (029 §7.3)`.

### Task 9 — The designation act: POST /api/v1/approvals/designate

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 8]
```

PRs designate through the ingest (Task 4); documents through their version
(Task 11). Deliverable-backed targets — the analysis commit, the evidence
bundle — need an explicit act: "the deliverable designates a newer commit"
(029 §7.1), and the submission carries the bundle's governed references.
A bearer-token API act: submission is authorship, done from a CLI or
pipeline, and freshness of group claims is a deciding-side concern only.

**Model**: request body in `internal/model/approval.go`:

```go
// DesignateRevisionInput submits an entity's newly governed revision with
// the revision-bound references the review covers (029 §7.1: governed
// references, never links to "current").
type DesignateRevisionInput struct {
	EntityKind string         `json:"entity_kind"`
	EntityID   string         `json:"entity_id"`
	Revision   string         `json:"revision"`
	References []GovernedRef  `json:"references,omitempty"`
}

type GovernedRef struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision string `json:"revision"`
}
```

(Reconcile the store's Task-3 `GovernedRef` onto the model type per ADR
036 — store scans into `model.GovernedRef`, one declaration.)

**API**: handler validating all three required fields and each reference's
three fields (422 on gaps), wrapping in one event —
`RecordEvent(ctx, <source recordDeliverable uses>, extID,
"approval.revision_designated", payload, ...)` — that calls
`DesignateRevision` then `InsertGovernedRefs` (from the entity at the new
revision). Response: the entity's rows after designation
(`model.ApprovalListResponse`), so the submitter sees the candidate row it
minted. `routeGuards` row `guarded(permApprovalDesignate)`; act metric
`designate`. A `RevisionNoop` outcome is still 200 — idempotent
resubmission, references upserted.

Tests: designate against a decided entity returns the candidate row and
records references readable via `GovernedRefsFor`; the impact fan-out from
Task 6 fires (a dependent with an approved row gains an open impact row);
resubmission is stable; 422 on a missing field; 401 without a token.

- [ ] `go test -trimpath ./internal/api -run TestDesignate -count=1` against
      Postgres — expect `ok`.
- [ ] Commit: `Designation act: revision plus governed references (029 §7.1)`.

### Task 10 — The CI gate in promote-prod.yml

```yaml
kind: feature
priority: medium
skills: []
blockedBy: [8]
```

029 §7.3: the manually-triggered prod publish workflow queries worklode for
the entity's approval and fails without it — a read, no outbound machinery.
`.github/workflows/promote-prod.yml` is this repo's such workflow and the
reference wiring another repo copies.

In `promote-prod.yml`:

1. Three `workflow_dispatch` inputs: `approval-entity-kind` and
   `approval-entity-id` (`required: true`), `approval-revision` (optional —
   when set, the approved row must bind exactly that revision).
2. A first job `approval-gate` (the `promote` job gains
   `needs: approval-gate`), one step:

```yaml
- name: Require an approved worklode approval
  env:
    WORKLODE_URL: ${{ vars.WORKLODE_URL }}
    WORKLODE_TOKEN: ${{ secrets.WORKLODE_API_TOKEN }}
  run: |
    set -euo pipefail
    resp=$(curl -sf --max-time 30 \
      -H "Authorization: Bearer ${WORKLODE_TOKEN}" \
      --get "${WORKLODE_URL}/api/v1/approvals" \
      --data-urlencode "entity_kind=${{ inputs.approval-entity-kind }}" \
      --data-urlencode "entity_id=${{ inputs.approval-entity-id }}" \
      --data-urlencode "state=approved")
    echo "$resp" | jq -e --arg rev "${{ inputs.approval-revision }}" \
      '.approvals | map(select(.review_kind == "review"
         and ($rev == "" or .subject_revision == $rev))) | length > 0' \
      || { echo "::error::no approved worklode approval for ${{ inputs.approval-entity-id }}"; exit 1; }
```

`curl -sf` makes an unreachable or non-200 backbone a failure: this gate
fails closed (the advisory mode is the CMS gate's property, 029 §7.3). No
reusable workflow — the endpoint is the contract; a second repo copies this
step.

- [ ] `actionlint .github/workflows/promote-prod.yml` if installed,
      otherwise `python3 -c 'import yaml,sys; yaml.safe_load(open(sys.argv[1]))' .github/workflows/promote-prod.yml`
      — expect no output.
- [ ] Dry-run the gate logic locally: run the `jq` filter against a captured
      Task 8 response body for the match, no-match, and revision-mismatch
      cases — expect exit 0, 1, 1.
- [ ] Note in the PR description: the `prod` environment needs
      `WORKLODE_URL` (variable) and `WORKLODE_API_TOKEN` (secret) configured
      before the next promotion; the workflow now refuses to run without an
      entity input.
- [ ] Commit: `Prod publish gate: require an approved worklode approval (029 §7.3)`.

### Task 11 — Per-document reviewer set as approvals rows

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

025 §7.3's reviewer set and accept gate, converted to the one table
(029 §7.3, final bullet): submitting a document materializes an `awaiting`
`doc` row bound to that version; accept refuses while the current version's
rows are open.

1. **Watcher rule** (`internal/watcher/doclifecycle.go`): a third rule,
   `approval-on-submit` — on `TypeDocumentSubmitted`, emit an action to
   materialize the approval row, suppressed when the executor reports an
   open approval already binds `(doc, IRI)` at this version (a new
   `Input.OpenApprovalBound bool` guard fact, filled by the executor like
   `OpenReviewTask`). Pure, table-tested beside the existing rules; add the
   rule name to the `rules` metric label slice in `metrics.go`.
2. **Executor** (`internal/api/docwatch.go`): perform the action the way the
   existing mints record their work — `RecordEvent` source `system`, type
   `approval.awaiting_materialized`, calling `InsertAwaitingApproval(tx,
   now, "doc", docIRI, strconv.Itoa(version), nil, nil)`. Requirement rows
   for *named* reviewers are part 2's ad-hoc surface adding
   `required_actor` rows to the same `(doc, IRI, version)`; nothing here
   precludes them.
3. **Accept gate** (`internal/store/docs.go`, `AcceptDoc`): after the
   existing assignee gate, refuse while any open (`awaiting` or
   `changes_requested`) approvals row exists for `("doc", IRI)` at the
   document's current version — error text naming the count and the
   `/reviews` queue. Rows for older versions do not block (they were
   rebound or superseded by the newer version's row), and documents
   submitted before this change have no row, so nothing already in flight
   is trapped.
4. **Author self-approval for docs**: `DecideApproval` extends its
   entity-kind branch — for `doc`, the decider must not be the document's
   `created_by` (resolve the doc from its IRI; reuse the Task 2/7 exception
   rule exactly as for PRs). Deciding a doc row is part 1's existing web
   act; it works unchanged once the queue lists the row.

**Cross-plan note:** the `/reviews` queue reader's generalization beyond the
PR join is part 2's work. If, when this task runs, `ListAwaitingApprovals`
still hard-joins `pull_requests`, doc rows will be decidable via the detail
page (Task 5 renders them nil-safely) but absent from the queue — say so in
the task's PR rather than widening the reader here.

Tests: watcher — the truth table gains the third rule's mint and suppress
cases; docwatch — a submitted doc yields the row exactly once across
redelivery; store — `AcceptDoc` refuses with an open row at the current
version, accepts once it is approved, ignores an older version's open row;
`DecideApproval` refuses the doc author without an exception.

- [ ] `go test -trimpath ./internal/watcher ./internal/api ./internal/store -run 'TestEvaluate|TestDocLifecycle|TestAcceptDoc|TestDecideApproval' -count=1`
      against Postgres — expect `ok`.
- [ ] Commit: `Document reviewer set as approvals rows; accept gate (025 §7.3, 029 §7.3)`.

### Task 12 — e2e: revision binding and the gate read over public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4, 6, 8, 9, 11]
```

`e2e/approvals_revision_test.go` (build tag `e2e`), extending the part-1
harness (`deliverGitHub`, `getPage`, bearer `/api/v1` calls) — public
surfaces only:

1. Bootstrap → project/repo/task, claim; signed `pull_request` `opened` →
   signed `pull_request_review` `approved` → `GET /reviews` no longer lists
   the row (part 1's lifecycle still holds).
2. Signed `pull_request` `synchronize` with a new head →
   `GET /reviews` lists the candidate row again, and
   `GET /api/v1/approvals?entity_kind=pr&entity_id=<repo>%23<n>` returns two
   rows whose `subject_revision`s are the two exact heads — the stale
   approval visible beside the unreviewed candidate.
3. The CI gate's exact question on the wire: the same read with
   `state=approved&subject_revision=<old head>` returns one row; with
   `subject_revision=<new head>` returns none (the Task 10 `jq` filter's
   match and no-match cases, driven against the real server).
4. `POST /api/v1/approvals/designate` for a second entity with references →
   the response carries the candidate row; the detail page
   (`GET /approvals/<id>`) shows the reference and both revisions.
5. Doc path: create and submit a doc via `/api/v1`, then the same read for
   `entity_kind=doc` shows the awaiting row bound to the submitted version;
   accepting via the API is refused while it is open.

- [ ] `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestApprovalRevision`
      against Postgres — expect `ok`.
- [ ] Full suite: `go test -trimpath -race -count=1 -tags e2e ./e2e/` — green.
- [ ] Commit: `e2e: revision binding, the gate read, and doc review rows`.

## Verification

- `go test -trimpath ./... -count=1` green with Postgres reachable (a
  silent skip proved nothing); `go test -trimpath -race -count=1 -tags e2e ./e2e/`
  green.
- `curl -s localhost:9090/metrics | grep -E 'worklode_approval(s_ingest|_decisions|_acts)_total'`
  shows the extended action/outcome sets after exercising the flows.
- `./scripts/narrow-check.sh` passes with the detail page included.
- The Task 10 `jq` dry-run outputs recorded in that task's PR.
- `lode doc anchors docs/plans/2026-08-25-approvals-3-revision-binding-and-gates.md`
  reports no errors.

## Deferred — stated so each gap is a decision

- **The CMS half of §7.3** — the Payload publish button, per-post binding,
  advisory degradation — is the frontmatter `defers:` entry, owned by
  `sunstone-cms` against Task 8's endpoint and contract comment.
- **A web designation surface** (a "submit for review" form on the
  deliverable page composing Task 9's act) — the API act and the deliverable
  detail work in `2026-08-25-research-work-4-deliverable-state` are where
  that form naturally lands; nothing here blocks it.
- **Named-reviewer ad-hoc rows** on documents ride part 2's ad-hoc
  requirement surface; Task 11 deliberately materializes only the base row.
- **Replacing GitHub as the PR review surface** stays explicitly undecided
  (029 §7.3); jump-out links remain the interaction model.
