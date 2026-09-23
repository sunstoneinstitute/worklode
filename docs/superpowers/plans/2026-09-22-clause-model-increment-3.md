# Clause Model, Increment 3 (plans as arrangements, plan lifecycle) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A plan arranges the clauses its `covers` entries reach, so coverage becomes a membership fact the store holds; a plan has a full lifecycle (`stale`, `withdrawn`, `spent`) with terminal plans hidden by default and every task of a closed plan governed; a withdrawn clause makes its plans stale; re-planning a stale plan is a deliberate act (`lode work next --replan`); and plan size is bounded by a token budget with a per-project override.

**Architecture:** Plan arrangements reuse the tables and the walk increment 1 built: `arrangePlan` writes `doc_clauses` rows for a plan from its `covers` edges with the same anchor-and-subtree walk `planClauses` already does, and `planClauses` becomes a read of that arrangement. The lifecycle work is three small store hooks (a `spent` check after every task state transition, governance of ungoverned tasks when a plan closes, a staleness trigger inside the clause status setter) plus one filter flag. Re-planning is one store function that mints and claims a `design` task about the stale plan, exposed as one route and one flag. The token budget is two server config values, a per-project override in a new `projects.settings` JSONB column behind a key allowlist, and a check in the two API handlers that write plan bodies.

**Tech Stack:** Go, Postgres via `database/sql`, cobra CLI, templ for the cockpit, Turtle for `ns/`, `scripts/nsgen.py` for the generated concept tables.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` (S5, S6, S16, S19, S23, S25, S28) and GitHub issue #687's "Increment 3" section. Executors read both.

**Stacked on:** branch `clause-increment-2` at `88174a01` (increment 2 at its Task 6; Tasks 7 to 9 of that plan land after this branch was cut). This branch is `clause-increment-3`; its PR targets `clause-increment-2` until that lands, then `clause-increment-1b` (#688), then `spec-refactor` (#686), then `main`. A sibling branch `clause-increment-4a` is built from the same base at the same time; the coordination notes in the preface say what it shares with this one.

## Rulings made while planning

The user was not available for brainstorming, so these are decided here. Each names what it costs if wrong.

- **R1 A plan arranges the clauses its `covers` edges reach; it mints none of its own.** `arrangePlan(tx, planID)` runs at the end of `rebuildEdges` when the document is a plan, so every plan body write (create, edit, import) rewrites the plan's `doc_clauses` rows: one row per reached clause in covers order, `depth` and `anchor` copied from the source arrangement, `clause_version` the clause's current version. The `## Tasks` section stays prose. `acceptPlanDoc` calls `arrangePlan` again before reading the arrangement, so a plan accepted after its spec grew still governs the current clauses. `planClauses` becomes `arrangedClauses(tx, planID)` reduced to ids. Costs one extra arrangement rewrite per accept.
- **R2 A shared clause is edited through its spec or ADR arrangement.** 1b's R3 refusal ("arranged in more than one document") is replaced: `EditClause` picks the one arrangement whose document kind is not `plan` and writes through it; plan arrangements only reference. A clause with two non-plan arrangements still refuses with the existing message, since nothing produces that. Costs nothing.
- **R3 S25 ships as the membership fact, and the level readers keep working unchanged.** After R1 the question "which plans cover clause X" is `SELECT doc_id FROM doc_clauses WHERE clause_id = X` joined to plans, and `model.Clause.ArrangedIn` from increment 1 already lists every arranging document, so the clause detail, `lode show WL-CL-<n>` and the cockpit clause page show a clause's plans with no new code. Retiring `coverage:` and `fullCoverageWith` means rewriting every reader of coverage levels, and there are many: `internal/designdoc/coverage.go` (`PlanIndex`, behind `lode doc todo`, `lode doc lint` and the corpus tools), `internal/store/docplanning.go` around line 401 (the needs-planning coverage query and `doc_coverage_completed_with`), `internal/store/progress.go` and `internal/progress/progress.go` (the Progress page), `internal/overview`, `internal/api/runboard.go`, `internal/cli/docs.go`, `internal/cli/overview.go`, `internal/ui/docs.templ`, `internal/cmd/doctodo.go`, `internal/cmd/graph.go`, `internal/derive/layout.go`, `internal/eventbus/emit.go`, `internal/ns` (`wlc:CoverageLevel`), the `lode:splitting-specs-into-plans` and `spec-coverage` plugin skills, and specs 05 and 06. That is a plan of its own, named "coverage is membership" and deferred; this increment leaves `coverage:` parsing, storage and every reader as they are and records in spec 12 that the levels are now redundant with the arrangement. Costs nothing shipped here; the follow-up plan deletes rather than reworks.
- **R4 `spent` is set by the store when a plan's last task closes.** `settlePlan(tx, now, taskID, eventID)` runs at the end of `transitionKnown`, after `resolveParent`: it reads the task's `plan_doc`; when the plan is `accepted` or `stale` and no live task minted from it is still open (the existing `taskClosed` predicate decides "closed"), it governs every ungoverned task of the plan from the plan's arrangement (S5's rule that a closed plan's tasks each carry a link), flips the plan to `spent` and logs the change. A plan with no minted tasks never becomes `spent` (a coverage-only plan stays `accepted`). Costs one cheap query per task transition on tasks that have a `plan_doc`, none on the rest.
- **R5 `withdrawn` for a plan is the existing `lode doc withdraw`.** `WithdrawDoc` already exists (025 §8.7) and already accepts `accepted` and `stale` documents. It gains the same governance step as R4 when the document is a plan. No new verb. Costs nothing.
- **R6 Terminal plans are hidden by default.** `DocFilter.IncludeTerminal` is false by default and hides plans whose status is `withdrawn` or `spent` when no explicit `Status` is asked for. `lode doc list --status all` and the cockpit docs list with `?status=all` set it; `--status <one status>` bypasses the hiding because it names what to show. Superseded and withdrawn specs are unaffected by this increment: S5 speaks of plans. Costs nothing.
- **R7 `SetClauseStatus` is the one writer of clause status outside acceptance, and it carries the S23 trigger.** `SetClauseStatus(tx, now, clauseID int64, status string, eventID int64) error` validates against the clause status CHECK set, writes the status, and when the new status is `withdrawn` marks every `accepted` plan arranging the clause `stale` through `MarkPlansStale`, which gains a `cause` parameter (`amended` today, `clause_withdrawn` here). Nothing in this increment calls it with `withdrawn` outside its test; increment 4 (split and merge lineage) will. Increment 4a asked for none of this. Costs nothing.
- **R8 Re-planning is `lode work next --replan [plan-ref]`.** With `--replan` and no argument the server picks the stale plan with the oldest `updated_at` in the project; with a ref it takes that plan. `ReplanNext` mints one `design` task (`AboutDoc` the plan, title "Re-plan <plan title>", body naming the plan and pointing at `lode show <ref> --inline`) unless an open `design` task about that plan exists, in which case it claims that one, then claims the task for the caller with the existing `Claim`. The response reuses `model.ClaimNextResponse`, so the command renders through the same path as an ordinary `lode work next`. No automatic minting anywhere (S28). Costs a second flag if the pick order needs to become rank-based.
- **R9 The plan token budget is server config with a per-project override in a JSONB settings column.** `LODE_PLAN_TOKENS_SOFT` (default 32000) and `LODE_PLAN_TOKENS_HARD` (default 64000), parsed at boot the way `embeddingBudget` parses its value. The override lives in `projects.settings jsonb NOT NULL DEFAULT '{}'` under keys `plan_tokens_soft` and `plan_tokens_hard`, behind a server-side allowlist so a typo cannot land. The sibling increment 4a puts its gate transition flag in the same column under its own key; this increment ships the mechanism (`PATCH /api/v1/projects/{id}/settings`, `lode project set <id> <key>=<value>...`, `model.Project.Settings`). Tokens are estimated from the body's rune count with the inverse of `corpusindex.BudgetFor`'s ratio: `tokens = runes * 4 / 7`. `createDoc` and `updateDocBody` in `internal/api` refuse a plan body over the hard ceiling with 422 and attach a warning to the returned document over the soft one; `lode doc add` and `lode doc edit` print the warning to stderr. Only those two handlers: plans are edited in place, so patches and revisions never carry a plan body. Costs one column if a typed settings table is wanted later.
- **R10 The `spent` status is added to `wlc:DesignDocStatus`.** `ns/concept.ttl` gains `wlc:spent` and the ordered collection, `scripts/nsgen.py` regenerates `internal/ns/gen.go`, and migration 0084 widens `docs_status_check`. Costs nothing.

Coordination with the sibling branch `clause-increment-4a`, recorded in `/private/tmp/claude-501/-Users-stig-git-sunstone-worklode/108a46dd-7633-48b1-ac40-7fc60e806436/scratchpad/increment-coordination.md` (the coordinator reads it before Task 1 and before the final rebase): this branch claims migration `0084_plan_lifecycle`; 4a claims 0085; whoever rebases second lets `check-migrations.sh` renumber. This branch renames spec 12's "Recorded from increment 1" heading to "Recorded from implementation" and takes S37 to S44; 4a takes S45 to S49. This branch adds no new L3 verb. 4a adds a key to `allowedProjectSettings` on its own branch after this one exists. Neither touches `internal/eventbus`, `internal/api/docwatch.go` or `internal/watcher`.

Deferred to later plans, stated so no reviewer reports them missing: retiring `coverage:` levels and rewriting their readers (R3; a plan named "coverage is membership"), the reconciler and gate trailer (S4, S18, S29, S30; increment 4a), split and merge lineage and the clause `withdrawn` writer (S22; increment 4), the migration of the old specs (A2), the BlockNote editor, the sprawl metrics.

## Global Constraints

- Build and test with `-trimpath` only: `make build`, `make test`, or `go test -trimpath ./internal/<pkg> -run <Test>`. Never bare `go test`.
- Store and API tests need Postgres with pgvector at `postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (override `TEST_POSTGRES_DSN`). They skip silently without it, so confirm the suite ran (`-v`, look for `--- PASS`, no `SKIP`). Two branches share that Postgres right now: a package that times out on an unrelated test is re-run once in isolation before it counts as a failure.
- Every shape that crosses HTTP is declared once in `internal/model` with wire field names (ADR 036). `internal/model/rule_test.go` and `deps_test.go` enforce it.
- Every route appears in `internal/api/router.go`'s `routeGuards`; `NewServer` refuses to boot otherwise. Task-claiming routes use whatever guard `POST /api/v1/tasks/claim-next` uses today; project settings writes use the guard `PUT /api/v1/projects/{id}/focus` uses.
- `internal/store/AGENTS.md`: a write a caller can make collide returns a sentinel from `errors.go`, mapped in `mapStoreErr`; never a raw pgx error for a caller-triggerable condition.
- `internal/cmd` decides, `internal/cli` renders. `internal/cli` never imports cobra. New CLI names satisfy `internal/cmd/namerule_test.go`; this plan adds no new verb (`set` is L3 canonical, `--replan` and `--status all` are flags and values).
- Migrations: the next number is whatever `./scripts/check-migrations.sh` accepts (0084 at planning time; 0083 is increment 2's). Add the pair to `deploy/base/kustomization.yaml` in the same commit. The down file fully reverses the up. Read `deploy/base/AGENTS.md` first.
- `ns/concept.ttl` is the source of `internal/ns/gen.go`: edit the Turtle, run `./scripts/nsgen.py`, commit both. `riot --validate ns/*.ttl` (riot is at `/opt/homebrew/bin/riot`) must pass. Spec prose is amended in the same commit as the Turtle (025 §17).
- Prose in `docs/specs2/` follows the `lode:anti-smartass` plain-language style: no em dashes, no "X, not Y" antithesis.
- Commit messages: imperative subject, no Co-authored-by or any self-reference.
- Feature-stem file naming: `store/planarrangement.go` (+ test), `store/planlifecycle.go` (+ test), `store/replan.go` (+ test), `store/projectsettings.go` (+ test), `api/replan.go`, `api/projectsettings.go`, `api/planbudget.go`, `cli/replan.go`, `cli/projectsettings.go`; existing files extended otherwise. No file over 2000 lines.
- Increment 2's Task 7 changes `Govern` to `Govern(tx, taskID, clauseID, source string, pin bool)`. The base of this branch still has the four-argument form. Write calls in the four-argument form; the final rebase onto `clause-increment-2` adds `, false` at each call site this plan introduces (`settlePlan`, `governPlanTasks`) if the five-argument form has landed. The coordinator records which form shipped.
- Plugin skill files under `plugins/claude/lode/skills/` are agent surfaces: after editing one, run `go test -trimpath ./internal/cmd -run 'TestAgentSurfaces|TestCommandReference'` and `./scripts/sync-codex-marketplace.py`, and commit what they regenerate (read `.claude/skills/worklode-lode-plugin/SKILL.md` first).

---

### Task 1: Migration 0084, `wlc:spent`, `projects.settings` (R9, R10)

**Files:**
- Create: `deploy/base/migrations/0084_plan_lifecycle.up.sql`, `deploy/base/migrations/0084_plan_lifecycle.down.sql`
- Modify: `deploy/base/kustomization.yaml` (append the pair after the 0083 lines)
- Modify: `ns/concept.ttl` (the `wlc:DesignDocStatus` scheme and `wlc:DesignDocStatusOrder`)
- Modify: `internal/ns/gen.go` (regenerated, not hand-edited)
- Modify: `docs/specs2/05-documents.md` (the status list where `withdrawn` is defined; add `spent` in the same sentence or table row)

**Interfaces:**
- Produces: `docs.status` accepts `spent`; `projects.settings jsonb NOT NULL DEFAULT '{}'`; `ns.DesignDocStatuses` contains `spent`.

- [ ] **Step 1: Read `deploy/base/AGENTS.md` and `deploy/base/migrations/0078_doc_lifecycle_fields.up.sql`** (the last widening of `docs_status_check`; copy its shape).

- [ ] **Step 2: Write the up migration**

```sql
-- Plan lifecycle (docs/specs2/12-spec-refactoring-design-tree.md S5, S19):
-- a plan whose every minted task has closed is spent, and a project may
-- override the server's plan token budget. settings is a small JSON object
-- behind a server-side key allowlist (internal/store/projectsettings.go).
ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn', 'spent'));

ALTER TABLE projects ADD COLUMN settings jsonb NOT NULL DEFAULT '{}';
```

- [ ] **Step 3: Write the down migration**

```sql
-- A spent plan is closed either way; withdrawn is the closest status the
-- narrower CHECK allows.
UPDATE docs SET status = 'withdrawn' WHERE status = 'spent';
ALTER TABLE docs DROP CONSTRAINT docs_status_check;
ALTER TABLE docs ADD CONSTRAINT docs_status_check
    CHECK (status IN ('draft', 'accepted', 'stale', 'superseded', 'withdrawn'));

ALTER TABLE projects DROP COLUMN settings;
```

- [ ] **Step 4: Add `wlc:spent` to `ns/concept.ttl`**

After the `wlc:withdrawn` concept (line 42 area), following its exact shape (concept, `skos:inScheme wlc:DesignDocStatus`, `skos:prefLabel "spent"`, a `skos:definition` sentence: "A plan whose every minted task has closed (12 S5)."), and add `wlc:spent` at the end of `wlc:DesignDocStatusOrder`'s `skos:memberList`.

- [ ] **Step 5: Regenerate and validate**

Run: `./scripts/nsgen.py && riot --validate ns/*.ttl && ./scripts/check-migrations.sh --no-fix && go test -trimpath ./internal/ns ./internal/store -run 'TestDocStatuses|TestValidDocStatus|TestNS' -v`
Expected: `internal/ns/gen.go` now lists `spent` in `DesignDocStatuses`; riot prints nothing; the migration check prints nothing; tests pass or report no matching tests (then run `go test -trimpath ./internal/ns`).

- [ ] **Step 6: Spec 05**

Find where 05 enumerates document statuses (grep `withdrawn` in `docs/specs2/05-documents.md`). Add `spent` beside it: "`spent`: a plan whose every minted task has closed; set by the store, terminal, hidden from the default listing (12 S5)." Match the surrounding form (table row or list item).

- [ ] **Step 7: Commit**

```bash
git add deploy/base/migrations/0084_plan_lifecycle.up.sql deploy/base/migrations/0084_plan_lifecycle.down.sql deploy/base/kustomization.yaml ns/concept.ttl internal/ns/gen.go docs/specs2/05-documents.md
git commit -m "Add the spent plan status and a project settings column"
```

---

### Task 2: Plans arrange the clauses they cover (R1, R2, S16)

**Files:**
- Create: `internal/store/planarrangement.go`, `internal/store/planarrangement_test.go`
- Modify: `internal/store/docedges.go` (`rebuildEdges`: call `arrangePlan` at the end when `kind == "plan"`)
- Modify: `internal/store/governedby.go` (`planClauses` reads the arrangement; the covers walk and `ensureClauses` move to `planarrangement.go`)
- Modify: `internal/store/docplanning.go` (`acceptPlanDoc` calls `arrangePlan` before `planClauses`)
- Modify: `internal/store/clauses.go` (`EditClause`: pick the non-plan arrangement)
- Modify: `internal/store/governedby_test.go` (`TestAcceptPlanGovernsMintedTasks`, `TestAcceptPlanSplitsCoveredDocOnFirstUse` keep passing; add nothing there)

**Interfaces:**
- Produces: `arrangePlan(tx *sql.Tx, planID int64) error`; `planClauses(tx *sql.Tx, planID int64) ([]int64, error)` (same signature, now a read).
- Consumes: `arrangedClauses(tx, docID) ([]clauseRow, error)`, `ensureClauses(tx, docID) error`, `rebuildEdges(tx, now, docID, kind, project, fm)`.

- [ ] **Step 1: Write the failing test**

`internal/store/planarrangement_test.go`. Reuse `clauseDocV1`, `arrangementOf`, `assertArrangement`, `mustCreateDoc`, `updateDocBody` and `governedPlanBody` (in `governedby_test.go`; read it for the plan body shape whose `covers: [P1-SPEC-1#sec-1]` resolves).

```go
package store

import (
	"strings"
	"testing"
)

// TestArrangePlanFromCovers: a plan arranges the clauses its covers entries
// reach (S16): a section-scoped entry reaches the clause at that anchor and
// the clauses under it; a document-scoped entry reaches every clause; the
// arrangement is rewritten on each plan body write and by accept.
func TestArrangePlanFromCovers(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	// clauseDocV1 arranges sec-1 (clause 1), sec-1.1 (clause 2), sec-2 (clause 3).
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	got := arrangementOf(t, s, plan.ID)
	if len(got) != 2 || got[0].Number != 1 || got[0].Anchor != "sec-1" || got[0].Depth != 2 ||
		got[1].Number != 2 || got[1].Anchor != "sec-1.1" || got[1].Depth != 3 {
		t.Fatalf("plan covering sec-1 should arrange clauses 1 and 2 with the spec's anchors and depths: %+v", got)
	}

	whole := strings.Replace(governedPlanBody, "P1-SPEC-1#sec-1", "P1-SPEC-1", 1)
	if _, err := updateDocBody(t, s, plan.ID, whole); err != nil {
		t.Fatal(err)
	}
	got = arrangementOf(t, s, plan.ID)
	if len(got) != 3 || got[2].Number != 3 {
		t.Fatalf("plan covering the whole spec should arrange all three clauses: %+v", got)
	}

	// The spec gains a clause after the plan was written; accept re-arranges.
	if _, err := updateDocBody(t, s, spec.ID, clauseDocV1+"\n## 3. Three {#sec-3}\n\nD.\n"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, tasks, err := acceptDoc(t, s, plan.ID, "stig"); err != nil || len(tasks) == 0 {
		t.Fatalf("accept plan: tasks=%d err=%v", len(tasks), err)
	}
	got = arrangementOf(t, s, plan.ID)
	if len(got) != 4 || got[3].Number != 4 {
		t.Fatalf("accepting the plan should re-arrange it against the current spec: %+v", got)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[0].ID)
	if err != nil || len(gov) != 4 {
		t.Fatalf("minted task should be governed by all four clauses: %d %v", len(gov), err)
	}
}

// TestEditClauseWritesThroughTheSpec: a clause arranged in a spec and in a
// plan is edited through the spec (R2); the plan arrangement only references.
func TestEditClauseWritesThroughTheSpec(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, err := editClause(t, s, "P1", 1, "One", "\nChanged.\n\n"); err != nil {
		t.Fatalf("edit of a clause arranged in a spec and a plan: %v", err)
	}
	d, err := s.GetDoc(t.Context(), spec.ID)
	if err != nil || !strings.Contains(d.Body, "Changed.") {
		t.Fatalf("spec body should carry the edit: %v %q", err, d.Body)
	}
}
```

If `clauses_test.go` (1b) has no `editClause` helper running `EditClause` through `RecordDocEvent`, add one there modelled on `updateRevision` in `docs_test.go`. Check the heading of clause 1 in `clauseDocV1` and use it verbatim in the call.

- [ ] **Step 2: Run to verify it fails**

Run: `go test -trimpath ./internal/store -run 'TestArrangePlanFromCovers|TestEditClauseWritesThroughTheSpec' -v`
Expected: `TestArrangePlanFromCovers` fails at the first assertion (a plan arranges nothing today); the second fails with the "arranged in 2 documents" refusal.

- [ ] **Step 3: Move the covers walk into `planarrangement.go` and write the arrangement**

Create `internal/store/planarrangement.go`. Move `ensureClauses` and the body of today's `planClauses` (the edges read and the anchor-and-subtree walk) here from `governedby.go`, then reshape:

```go
package store

import (
	"database/sql"
	"fmt"
)

// planEntry is one clause a plan's covers edges reach, with the anchor and
// depth it has in the spec that arranges it, in covers order.
type planEntry struct {
	id, version int64
	depth       int
	anchor      string
}

// coveredClauses walks a plan's resolved covers edges the way 03 §4 states
// it: a section-scoped edge reaches the clause at that anchor and the clauses
// arranged under it; a document-scoped edge reaches every clause the document
// arranges; an unresolved edge reaches nothing. A covered spec that predates
// the clause tables is split first (ensureClauses).
func coveredClauses(tx *sql.Tx, planID int64) ([]planEntry, error) {
	// ... the edges read and the arrangements map from today's planClauses,
	// collecting planEntry{c.id, int64(c.version), c.depth, c.anchor}
	// instead of bare ids, deduplicated by id in first-seen order.
}

// arrangePlan rewrites a plan's doc_clauses rows from its covers edges (S16,
// increment 3 R1). A plan arranges the clauses it covers and mints none of its
// own; its ## Tasks prose is not a clause. Runs on every plan body write from
// rebuildEdges and again from acceptPlanDoc, so accept sees the spec as it is.
func arrangePlan(tx *sql.Tx, planID int64) error {
	entries, err := coveredClauses(tx, planID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM doc_clauses WHERE doc_id = $1`, planID); err != nil {
		return fmt.Errorf("clear arrangement of plan %d: %w", planID, err)
	}
	for i, e := range entries {
		if _, err := tx.Exec(
			`INSERT INTO doc_clauses (doc_id, position, clause_id, clause_version, depth, anchor)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			planID, i, e.id, e.version, e.depth, e.anchor); err != nil {
			return fmt.Errorf("arrange clause %d in plan %d: %w", e.id, planID, err)
		}
	}
	return nil
}
```

Note `clauseRow.version` is `int` in `clauses.go`; keep types consistent with it (use `int` in `planEntry` if that is simpler). The `(doc_id, position)` primary key and the `(clause_id, clause_version)` foreign key already exist on `doc_clauses`; the clause's current version always has a `clause_versions` row.

In `governedby.go`, `planClauses` becomes:

```go
// planClauses is the plan's arranged clause ids in arrangement order. The
// arrangement is written by arrangePlan on every plan body write and again
// at accept (increment 3 R1).
func planClauses(tx *sql.Tx, planID int64) ([]int64, error) {
	rows, err := arrangedClauses(tx, planID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.id)
	}
	return out, nil
}
```

In `docedges.go`'s `rebuildEdges`, after the last write of the function and before its final `return nil`:

```go
	if kind == "plan" {
		if err := arrangePlan(tx, docID); err != nil {
			return err
		}
	}
```

In `docplanning.go`'s `acceptPlanDoc`, before `governing, err := planClauses(tx, id)`:

```go
	if err := arrangePlan(tx, id); err != nil {
		return nil, nil, fmt.Errorf("arrange plan %d: %w", id, err)
	}
```

`rebuildSectionsFrom` keeps its `kind == "plan"` early return: a plan has no `doc_sections` and mints no clauses; only `rebuildEdges` arranges it.

- [ ] **Step 4: `EditClause` writes through the non-plan arrangement**

In `internal/store/clauses.go`, where `EditClause` collects `docIDs` from `doc_clauses`, join `docs` and keep only rows with `kind <> 'plan'`:

```go
	rows, err := tx.Query(
		`SELECT dc.doc_id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.clause_id = $1 AND d.kind <> 'plan' AND d.deleted_at IS NULL
		  ORDER BY dc.doc_id`, clauseID)
```

Keep the `switch len(docIDs)` as it is; update the two refusal messages so the multi-document one reads "is arranged in %d specs or ADRs; editing a clause shared between them is not supported" and the doc comment above `EditClause` says plans only reference (increment 3 R2).

- [ ] **Step 5: Run the tests**

Run: `go test -trimpath ./internal/store -run 'TestArrangePlan|TestEditClause|TestAcceptPlan|TestGovern|TestEnsureClauses|TestSyncClauses|TestDocAcceptPlan' -v`
Expected: all PASS. `TestAcceptPlanGovernsMintedTasks` and `TestAcceptPlanSplitsCoveredDocOnFirstUse` pass unchanged since accept now arranges before governing.

- [ ] **Step 6: Full store package, then commit**

Run: `go test -trimpath -race -count=1 ./internal/store`

```bash
git add internal/store/planarrangement.go internal/store/planarrangement_test.go internal/store/governedby.go internal/store/docedges.go internal/store/docplanning.go internal/store/clauses.go internal/store/clauses_test.go
git commit -m "Arrange a plan from the clauses its covers entries reach (S16)"
```

---

### Task 3: Plan lifecycle: spent, governed on close, stale on clause withdrawal, hidden when terminal (R4 to R7, S5, S23)

**Files:**
- Create: `internal/store/planlifecycle.go`, `internal/store/planlifecycle_test.go`
- Modify: `internal/store/tasks.go` (`transitionKnown` calls `settlePlan` after `resolveParent`)
- Modify: `internal/store/docgroom.go` (`WithdrawDoc` governs a plan's tasks; `MarkPlansStale` gains `cause`)
- Modify: `internal/api/docs.go` (the one `MarkPlansStale` call passes `"amended"`)
- Modify: `internal/store/clauses.go` (append `SetClauseStatus`)
- Modify: `internal/store/docs.go` (`DocFilter.IncludeTerminal`; `ListDocs` hides terminal plans)
- Modify: `internal/api/web.go` (`docFilterFrom` reads `status=all`), `internal/api/docs.go` (the API list selector reads `status=all` the same way; find `sel.filter`)
- Modify: `internal/cmd/doc.go` (`--status all`), `internal/cli/docs.go` if the client encodes the status query

**Interfaces:**
- Produces: `settlePlan(tx *sql.Tx, now time.Time, taskID string, eventID int64) error`; `governPlanTasks(tx *sql.Tx, planID int64) error`; `SetClauseStatus(tx *sql.Tx, now time.Time, clauseID int64, status string, eventID int64) error`; `MarkPlansStale(tx, now, planIDs, cause, specSlug, anchors, eventID)`; `DocFilter.IncludeTerminal bool`.
- Consumes: `taskClosed(alias string) string`, `LogChange(tx, "doc", id, eventID, map[string]string{...})`, `Govern`, `arrangedClauses`, `lockDoc`.

- [ ] **Step 1: Write the failing tests**

`internal/store/planlifecycle_test.go`. Read `tasksclosed_test.go` and `taskstransitions_test.go` for how a test closes a task (the helper that runs `Transition` through `RecordEvent`, and which states count as closed without a merged commit: `abandoned` always does).

```go
package store

import "testing"

// TestPlanSpentWhenLastTaskCloses: closing the last open task minted by a
// plan flips the plan to spent and governs any ungoverned task from the
// plan's arrangement (S5, increment 3 R4).
func TestPlanSpentWhenLastTaskCloses(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: twoTaskPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) != 2 {
		t.Fatalf("accept: %d tasks, %v", len(tasks), err)
	}
	// Drop one task's governance by hand so the close has something to repair.
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM task_governed_by WHERE task_id = $1`, tasks[1].ID); err != nil {
		t.Fatal(err)
	}
	abandon(t, s, tasks[0].ID)
	if d, _ := s.GetDoc(t.Context(), plan.ID); d.Status != "accepted" {
		t.Fatalf("one task still open: plan should stay accepted, got %s", d.Status)
	}
	abandon(t, s, tasks[1].ID)
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "spent" {
		t.Fatalf("last task closed: plan should be spent, got %s %v", d.Status, err)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[1].ID)
	if err != nil || len(gov) == 0 {
		t.Fatalf("closing the plan should govern its ungoverned task: %d %v", len(gov), err)
	}
}

// TestWithdrawPlanGovernsItsTasks: lode doc withdraw on a plan governs every
// ungoverned task from the arrangement before closing (S5).
func TestWithdrawPlanGovernsItsTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	_, tasks, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil || len(tasks) == 0 {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(t.Context(), `DELETE FROM task_governed_by WHERE task_id = $1`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := withdrawDoc(t, s, plan.ID); err != nil {
		t.Fatal(err)
	}
	gov, err := s.GovernedBy(t.Context(), tasks[0].ID)
	if err != nil || len(gov) == 0 {
		t.Fatalf("withdrawn plan's task should be governed: %d %v", len(gov), err)
	}
}

// TestWithdrawnClauseMakesPlansStale: SetClauseStatus withdrawn marks every
// accepted plan arranging the clause stale (S23).
func TestWithdrawnClauseMakesPlansStale(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	id, err := clauseID(t, s, "P1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := setClauseStatus(t, s, id, "withdrawn"); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDoc(t.Context(), plan.ID)
	if err != nil || d.Status != "stale" {
		t.Fatalf("plan arranging a withdrawn clause should be stale, got %s %v", d.Status, err)
	}
	if err := setClauseStatus(t, s, id, "nonsense"); err == nil {
		t.Fatal("unknown status should be refused")
	}
}

// TestListDocsHidesTerminalPlans: withdrawn and spent plans are hidden unless
// IncludeTerminal or an explicit Status asks for them (S5, R6).
func TestListDocsHidesTerminalPlans(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := withdrawDoc(t, s, plan.ID); err != nil {
		t.Fatal(err)
	}
	def, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan"})
	all, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan", IncludeTerminal: true})
	byStatus, _ := s.ListDocs(t.Context(), DocFilter{Project: "p1", Kind: "plan", Status: "withdrawn"})
	if len(def) != 0 || len(all) != 1 || len(byStatus) != 1 {
		t.Fatalf("default %d (want 0), all %d (want 1), by status %d (want 1)", len(def), len(all), len(byStatus))
	}
}
```

Helpers to add in this file if they do not exist elsewhere in the package's tests: `twoTaskPlanBody` (copy `governedPlanBody` and add a second `### Task 2 — Second` declaration with its own `kind: chore` fence), `abandon(t, s, id)` (run `Transition(tx, s.Now(), id, "ready", "abandoned", eventID)` through `s.RecordEvent`; check the minted tasks' state first with `TaskState` and use it as `from`), `withdrawDoc(t, s, id)` (run `WithdrawDoc` through `RecordDocEvent` like `discardRevision` does), `clauseID(t, s, key, n)` (`ClauseIDByRef` in a transaction), `setClauseStatus(t, s, id, status)` (run `SetClauseStatus` through `s.RecordEvent`).

- [ ] **Step 2: Run to verify they fail**

Run: `go test -trimpath ./internal/store -run 'TestPlanSpent|TestWithdrawPlan|TestWithdrawnClause|TestListDocsHidesTerminal' -v`
Expected: compile failure on `SetClauseStatus`, `IncludeTerminal`.

- [ ] **Step 3: `planlifecycle.go`**

```go
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// governPlanTasks gives every task minted by the plan that carries no
// governing link the plan's arranged clauses (S5: a closed plan's tasks each
// carry at least one link; S3: the store is the second writer of links, in
// its minimal form). Tasks that already have links are left alone.
func governPlanTasks(tx *sql.Tx, planID int64) error {
	clauses, err := planClauses(tx, planID)
	if err != nil {
		return err
	}
	if len(clauses) == 0 {
		return nil
	}
	rows, err := tx.Query(
		`SELECT t.id FROM tasks t
		  WHERE t.plan_doc = $1 AND t.deleted_at IS NULL
		    AND NOT EXISTS (SELECT 1 FROM task_governed_by g WHERE g.task_id = t.id)`, planID)
	if err != nil {
		return fmt.Errorf("ungoverned tasks of plan %d: %w", planID, err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		for _, c := range clauses {
			if err := Govern(tx, id, c, "plan"); err != nil {
				return fmt.Errorf("govern task %s from plan %d: %w", id, planID, err)
			}
		}
	}
	return nil
}

// settlePlan runs after every task state change: when the task came from a
// plan and that plan has no open task left, the plan is spent (S5, increment
// 3 R4). A plan with no minted task never gets here with a task, so a
// coverage-only plan stays accepted.
func settlePlan(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
	var planID sql.NullInt64
	if err := tx.QueryRow(`SELECT plan_doc FROM tasks WHERE id = $1`, taskID).Scan(&planID); err != nil {
		return fmt.Errorf("plan of task %s: %w", taskID, err)
	}
	if !planID.Valid {
		return nil
	}
	var status string
	err := tx.QueryRow(`SELECT status FROM docs WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`, planID.Int64).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("status of plan %d: %w", planID.Int64, err)
	}
	if status != "accepted" && status != "stale" {
		return nil
	}
	var open int
	if err := tx.QueryRow(
		`SELECT count(*) FROM tasks t WHERE t.plan_doc = $1 AND t.deleted_at IS NULL AND NOT `+taskClosed("t"),
		planID.Int64).Scan(&open); err != nil {
		return fmt.Errorf("open tasks of plan %d: %w", planID.Int64, err)
	}
	if open > 0 {
		return nil
	}
	if err := governPlanTasks(tx, planID.Int64); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE docs SET status = 'spent', updated_at = $2 WHERE id = $1`, planID.Int64, now.UTC()); err != nil {
		return fmt.Errorf("spend plan %d: %w", planID.Int64, err)
	}
	return LogChange(tx, "doc", planID.Int64, eventID, map[string]string{"field": "status", "old": status, "new": "spent"})
}
```

Check `LogChange`'s id parameter type (tasks pass a string, docs may pass `strconv.FormatInt`); match whatever `docgroom.go`'s stale write passes. Check the exact type of `tasks.plan_doc` and whether the `taskClosed` subqueries' aliases (`ch`, `cht`, `tc`, `mc`, `pr`) collide with `t` (they do not; `t` is free).

In `tasks.go`, `transitionKnown` ends with `return resolveParent(tx, now, taskID, eventID)`. Replace with:

```go
	if err := resolveParent(tx, now, taskID, eventID); err != nil {
		return err
	}
	return settlePlan(tx, now, taskID, eventID)
```

- [ ] **Step 4: `WithdrawDoc` governs a plan's tasks; `MarkPlansStale` takes a cause**

In `docgroom.go`'s `WithdrawDoc`, after `lockDoc` and its status check, before the status UPDATE: `if d.kind == "plan" { if err := governPlanTasks(tx, id); err != nil { return nil, err } }`.

Change `MarkPlansStale(tx, now, planIDs []int64, specSlug string, anchors []string, eventID int64)` to `MarkPlansStale(tx, now, planIDs []int64, cause, specSlug string, anchors []string, eventID int64)` and use `cause` where the payload writes `"cause": "amended"`. Update the one caller in `internal/api/docs.go` (line 740 area) to pass `"amended"`. Update the doc comment: the cause is `amended` for 025 §8.6 and `clause_withdrawn` for S23.

- [ ] **Step 5: `SetClauseStatus` in `clauses.go`**

```go
// clauseStatuses mirrors the clauses.status CHECK in migration 0082.
var clauseStatuses = map[string]bool{"draft": true, "accepted": true, "superseded": true, "withdrawn": true}

// SetClauseStatus is the one writer of a clause's status outside document
// acceptance (increment 3 R7). Withdrawing a clause marks every accepted plan
// arranging it stale (S23): the plan's frozen list names text that no longer
// holds. Increment 4's split and merge lineage is the caller that withdraws.
func SetClauseStatus(tx *sql.Tx, now time.Time, clauseID int64, status string, eventID int64) error {
	if !clauseStatuses[status] {
		return fmt.Errorf("clause status %q: %w", status, ErrInvalidInput)
	}
	var old, project string
	var number int64
	err := tx.QueryRow(`SELECT status, project_id, number FROM clauses WHERE id = $1 FOR UPDATE`, clauseID).Scan(&old, &project, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("clause %d: %w", clauseID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read clause %d: %w", clauseID, err)
	}
	if old == status {
		return nil
	}
	if _, err := tx.Exec(`UPDATE clauses SET status = $2, updated_at = $3 WHERE id = $1`, clauseID, status, now.UTC()); err != nil {
		return fmt.Errorf("set clause %d status: %w", clauseID, err)
	}
	if status != "withdrawn" {
		return nil
	}
	rows, err := tx.Query(
		`SELECT DISTINCT d.id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.clause_id = $1 AND d.kind = 'plan' AND d.status = 'accepted' AND d.deleted_at IS NULL`, clauseID)
	if err != nil {
		return fmt.Errorf("plans arranging clause %d: %w", clauseID, err)
	}
	var plans []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		plans = append(plans, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	ref := fmt.Sprintf("%s-CL-%d", projectKeyOf(tx, project), number)
	_, err = MarkPlansStale(tx, now, plans, "clause_withdrawn", ref, nil, eventID)
	return err
}
```

Read how `MarkPlansStale` uses `specSlug` (it goes into the payload and `StaleExternalID`); passing the clause ref there is fine as long as the external id stays unique per plan version. If `projectKeyOf` does not exist, look up how `GetClause` builds the `WL-CL-<n>` ref and reuse that query inline. Consider the increment 2 clause events: if `clauses.go` on the branch has a clause event helper, use it; otherwise the status change is recorded by the caller's event and the plan's `doc.stale` event.

- [ ] **Step 6: Hide terminal plans**

`DocFilter` gains:

```go
	// IncludeTerminal shows plans that are withdrawn or spent, which the
	// default listing hides (12 S5). An explicit Status also shows them.
	IncludeTerminal bool
```

In `ListDocs`, where the WHERE clauses are assembled, add when `f.Status == "" && !f.IncludeTerminal`: `NOT (kind = 'plan' AND status IN ('withdrawn', 'spent'))`.

In `internal/cmd/doc.go`'s list command: the `--status` flag's completion values are `ns.DesignDocStatuses`; append `"all"` the way `internal/cmd/task.go:374` does for tasks (`docStatusValues = append(slices.Clone(ns.DesignDocStatuses), "all")`), and when the value is `all` send no status and `include_terminal=true` (or whatever query key the API selector reads; add it next to `status` in `sel.filter`'s parsing in `internal/api/docs.go` and in `docFilterFrom` in `web.go`, reading `status=all` as `IncludeTerminal: true, Status: ""`). Update the flag help: "filter by status: <list>, or all to include withdrawn and spent plans". `checkDocSelectors` must accept `all`.

- [ ] **Step 7: Run the tests, then the store and api packages**

Run: `go test -trimpath ./internal/store -run 'TestPlanSpent|TestWithdrawPlan|TestWithdrawnClause|TestListDocsHidesTerminal|TestMarkPlansStale|TestDocAcceptPlan|TestTransition' -v` then `go test -trimpath -race -count=1 ./internal/store ./internal/api ./internal/cmd`.

- [ ] **Step 8: Commit**

```bash
git add internal/store/planlifecycle.go internal/store/planlifecycle_test.go internal/store/tasks.go internal/store/docgroom.go internal/store/clauses.go internal/store/docs.go internal/api/docs.go internal/api/web.go internal/cmd/doc.go internal/cli/docs.go
git commit -m "Spend a plan when its last task closes, stale it when a clause is withdrawn, hide terminal plans (S5, S23)"
```

---

### Task 4: `lode work next --replan` (R8, S28)

**Files:**
- Create: `internal/store/replan.go`, `internal/store/replan_test.go`, `internal/api/replan.go`, `internal/api/replan_test.go`, `internal/cli/replan.go`
- Modify: `internal/model/claim.go` (append `ReplanInput`)
- Modify: `internal/api/router.go`, `internal/api/server.go` (route)
- Modify: `internal/cmd/lifecycle.go` (`--replan` flag on `newNextCmd`, branch in `runNext`)

**Interfaces:**
- Produces: `model.ReplanInput{Project, Plan, Worktree string; TTLSeconds int}`; `(*Store).ReplanNext(ctx, ReplanOpts{ProjectID, Plan, ActorID, Worktree string; TTL time.Duration}) (*ClaimNextResult, error)`; `POST /api/v1/work/replan` 200 `model.ClaimNextResponse`; `(c *Client) Replan(ctx, in model.ReplanInput) (model.ClaimNextResponse, []byte, error)`.
- Consumes: `(*Store).Claim(ctx, taskID, actorID, worktree, ttl) (*Lease, error)`, `CreateTask`, `RecordEvent`, `resolveDocRef` or whatever `lode show` uses to turn a ref into a doc id inside the store (find the exported resolver `ListDocs`-adjacent code uses; `internal/api/docref.go` shows the API side), `OpenTaskForDoc(ctx, docID, kind)`, and how `claimNext` in `internal/api` turns a `ClaimNextResult` into `model.ClaimNextResponse` (reuse that function).

- [ ] **Step 1: Read first**

`internal/store/ranking.go` from `ClaimNext` to the end of `ClaimNextResult`'s construction (how the pick and lease become the result), the `claimNext` handler in `internal/api` (find it: `grep -n "claim-next" internal/api/router.go`), `runNext` in `internal/cmd/lifecycle.go` (the `resp.Task` branch that renders a claim), `watcher.PlanningTitle`/`PlanningBody` for the shape of a doc-about task body.

- [ ] **Step 2: Write the failing store test**

```go
// TestReplanNextMintsAndClaimsOneDesignTask: a stale plan is handed out as a
// claimed design task about the plan; a second call finds the open task and
// claims that one rather than minting again; no stale plan means not claimed
// (S28).
func TestReplanNextMintsAndClaimsOneDesignTask(t *testing.T) {
	s := openTaskStore(t) // seeds a participant; check it also allows docs, else openDocStore plus the actor seed
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "pl", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	none, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || none.Claimed {
		t.Fatalf("no stale plan: want not claimed, got %+v %v", none, err)
	}
	markStale(t, s, plan.ID) // UPDATE docs SET status='stale' through s.db is enough here
	first, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !first.Claimed || first.Task == nil {
		t.Fatalf("stale plan: want a claimed task, got %+v %v", first, err)
	}
	task, err := s.GetTask(t.Context(), first.Task.ID)
	if err != nil || task.Kind != "design" || task.AboutDoc != plan.ID {
		t.Fatalf("want a design task about the plan: %+v %v", task, err)
	}
	release(t, s, first.Task.ID) // the lease helper tasks tests use
	second, err := s.ReplanNext(t.Context(), ReplanOpts{ProjectID: "p1", Plan: "P1-PLAN-1", ActorID: "stig", TTL: time.Hour})
	if err != nil || !second.Claimed || second.Task.ID != first.Task.ID {
		t.Fatalf("open design task exists: want it claimed again, got %+v %v", second, err)
	}
}
```

Adjust field names to `ClaimNextResult` and `model.Task` as they are (`AboutDoc` may be a pointer or `int64`; read `model/task.go`). Use the existing lease release helper from `leases_test.go`.

- [ ] **Step 3: Run to verify it fails**, then implement `replan.go`

```go
package store

// ReplanOpts names the project and, optionally, the stale plan to hand out.
type ReplanOpts struct {
	ProjectID string
	Plan      string // a plan ref or slug; empty picks the stale plan with the oldest updated_at
	ActorID   string
	Worktree  string
	TTL       time.Duration
}

// ReplanNext hands a stale plan out as claimed re-planning work (S28): it
// finds the plan, reuses an open design task about it or mints one, then
// claims that task for the actor. Nothing here runs unasked; the eventbus
// mints no re-planning task.
func (s *Store) ReplanNext(ctx context.Context, o ReplanOpts) (*ClaimNextResult, error) {
	planID, title, slug, err := s.stalePlan(ctx, o.ProjectID, o.Plan)
	if errors.Is(err, ErrNotFound) {
		return &ClaimNextResult{Claimed: false, Reason: "no stale plan"}, nil
	}
	if err != nil {
		return nil, err
	}
	taskID, err := s.OpenTaskForDoc(ctx, planID, "design")
	if errors.Is(err, ErrNotFound) || taskID == "" {
		taskID, err = s.mintReplanTask(ctx, o, planID, title, slug)
	}
	if err != nil {
		return nil, err
	}
	lease, err := s.Claim(ctx, taskID, o.ActorID, o.Worktree, o.TTL)
	if err != nil {
		return nil, err
	}
	return s.claimNextResultFor(ctx, taskID, lease) // build the same result ClaimNext builds; extract a helper from ClaimNext if none exists
}
```

`stalePlan` selects `id, title, slug FROM docs WHERE project_id = $1 AND kind = 'plan' AND status = 'stale' AND deleted_at IS NULL` ordered by `updated_at`, `LIMIT 1`, or by resolved ref when `o.Plan` is set (resolve with the same store resolver the API's `docref.go` path ends in; a ref naming a plan that is not stale is `ErrInvalidInput` with the message "plan <ref> is <status>, not stale"). Rewrite that message without the antithesis: "plan <ref> has status <status>; only a stale plan can be re-planned". `mintReplanTask` runs `CreateTask` through `s.RecordEvent("cli", "replan-<planID>-<unix>", "task.created", payload, ...)` with `TaskInput{ProjectID, Title: "Re-plan " + title, Body: "Plan <KEY>-PLAN-<n> (" + slug + ") is stale. Read it with lode show <ref> --inline, decide which declarations stand, edit the plan with lode doc edit, and re-accept it.", Kind: "design", Priority: "medium", CreatedBy: o.ActorID, AboutDoc: planID}` and returns the task id. Check `OpenTaskForDoc`'s exact not-found behaviour (it may return `"", nil`).

- [ ] **Step 4: API and CLI**

`internal/model/claim.go`:

```go
// ReplanInput is POST /api/v1/work/replan: hand a stale plan out as a claimed
// design task (12 S28). Plan is optional; empty picks the oldest stale plan.
type ReplanInput struct {
	Project    string `json:"project"`
	Plan       string `json:"plan,omitempty"`
	Worktree   string `json:"worktree,omitempty"`
	TTLSeconds int    `json:"ttl_seconds,omitempty"`
}
```

`internal/api/replan.go`: `replan` handler reads `ReplanInput`, requires `Project`, calls `s.st.ReplanNext` with the subject's actor id and the same TTL defaulting the claim-next handler uses, and writes the response through the same conversion the claim-next handler uses. Route: `"POST /api/v1/work/replan"` with the guard `POST /api/v1/tasks/claim-next` has, registered in `router.go` and `server.go` next to it. Test: a stale plan in a seeded project yields `claimed: true` and a task of kind `design`; no stale plan yields `claimed: false`.

`internal/cli/replan.go`: `(c *Client) Replan(ctx, in model.ReplanInput) (model.ClaimNextResponse, []byte, error)` via `doJSON` like `ClaimNext`.

`internal/cmd/lifecycle.go`: `cmd.Flags().BoolVar(&replan, "replan", false, "hand out a stale plan to re-plan: mints (or reuses) a design task about it and claims it; with an id argument, that plan")`. In `runNext`, before the ordinary claim branches: when `replan`, call `c.Replan(ctx, model.ReplanInput{Project: sc.Project, Plan: id, Worktree: pending})` and continue into the existing `resp.Task` rendering (the not-claimed branch prints `resp.Reason`). Do not combine `--replan` with `--kind` or `--strict-focus` (`MarkFlagsMutuallyExclusive`).

- [ ] **Step 5: Tests, surfaces, commit**

Run: `go test -trimpath ./internal/store -run TestReplan -v && go test -trimpath ./internal/api -run 'TestReplan|TestNewServer' -v && go test -trimpath ./internal/cmd -run 'TestName|TestCommandReference|TestAgentSurfaces' -v`. Commit the regenerated `plugins/claude/lode/skills/worklode/references/commands.md` with the code.

```bash
git add internal/store/replan.go internal/store/replan_test.go internal/api/replan.go internal/api/replan_test.go internal/cli/replan.go internal/model/claim.go internal/api/router.go internal/api/server.go internal/cmd/lifecycle.go plugins/claude/lode/skills/worklode/references/commands.md
git commit -m "Hand a stale plan out for re-planning with lode work next --replan (S28)"
```

---

### Task 5: Plan token budget and project settings (R9, S6, S19)

**Files:**
- Create: `internal/store/projectsettings.go`, `internal/store/projectsettings_test.go`, `internal/api/projectsettings.go`, `internal/api/planbudget.go`, `internal/api/planbudget_test.go`, `internal/cli/projectsettings.go`
- Modify: `internal/model/project.go` (`Settings map[string]any`), `internal/model/doc.go` (`Warnings []string` on `Doc`, omitempty), a new `model.ProjectSettingsInput` (in `project.go`)
- Modify: `internal/api/server.go` (`Config.PlanTokensSoft`, `PlanTokensHard`; `planBudget(cfg)` parsed at boot next to `embeddingBudget`), `internal/api/docs.go` (`createDoc`, `updateDocBody`), `internal/api/router.go`
- Modify: `internal/cmd/project.go` (`lode project set <id> <key>=<value>...`), `internal/cmd/doc.go` (print `Warnings` after `add` and `edit`)
- Modify: `deploy/base/configmap.yaml`, `docker-compose.yml`, `README.md` (the two env vars, as increment 1's Task 2 did for `LODE_EMBEDDING_CONTEXT_TOKENS`)
- Modify: `plugins/claude/lode/skills/splitting-specs-into-plans/SKILL.md` ("Choosing the split" gains the two numbers), `plugins/claude/lode/skills/plan-spec/SKILL.md` (Step 3 one sentence)

**Interfaces:**
- Produces: `allowedProjectSettings map[string]func(json.RawMessage) error` (package-local); `(*Store).SetProjectSettings(ctx, projectID string, patch map[string]json.RawMessage) error`; `model.Project.Settings map[string]any`; `PATCH /api/v1/projects/{id}/settings` 200 `model.Project`; `(c *Client) SetProjectSettings(ctx, id string, patch map[string]any) (model.Project, []byte, error)`; `planTokens(body string) int`; `(s *server) planBudgetFor(ctx, projectID) (soft, hard int, err error)`; `model.Doc.Warnings []string`.

- [ ] **Step 1: Store settings**

`projectsettings.go`:

```go
// allowedProjectSettings is the server-side allowlist of projects.settings
// keys (increment 3 R9). A key not listed is refused, so a typo cannot land.
// Sibling increments add their keys here; the value validator says what shape
// the key takes.
var allowedProjectSettings = map[string]func(json.RawMessage) error{
	"plan_tokens_soft": positiveInt,
	"plan_tokens_hard": positiveInt,
}

func positiveInt(v json.RawMessage) error {
	var n int
	if err := json.Unmarshal(v, &n); err != nil || n <= 0 {
		return fmt.Errorf("want a positive integer, got %s", string(v))
	}
	return nil
}

// SetProjectSettings merges patch into projects.settings. A null value
// removes the key. Unknown keys and values of the wrong shape are
// ErrInvalidInput naming the key.
func (s *Store) SetProjectSettings(ctx context.Context, projectID string, patch map[string]json.RawMessage) error {
	for k, v := range patch {
		check, ok := allowedProjectSettings[k]
		if !ok {
			return fmt.Errorf("unknown project setting %q: %w", k, ErrInvalidInput)
		}
		if string(v) == "null" {
			continue
		}
		if err := check(v); err != nil {
			return fmt.Errorf("project setting %q: %v: %w", k, err, ErrInvalidInput)
		}
	}
	raw, _ := json.Marshal(patch)
	res, err := s.db.ExecContext(ctx,
		`UPDATE projects SET settings = jsonb_strip_nulls(settings || $2::jsonb) WHERE id = $1`, projectID, raw)
	if err != nil {
		return fmt.Errorf("set settings of project %s: %w", projectID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("project %s: %w", projectID, ErrNotFound)
	}
	return nil
}
```

`GetProject` and `ListProjects` scan `settings` into `model.Project.Settings` (scan the jsonb into `[]byte`, unmarshal into `map[string]any`; an empty object gives an empty map). Test: set both keys, read back, unknown key refused, wrong shape refused, null removes.

- [ ] **Step 2: Config and the budget check**

`Config` gains `PlanTokensSoft string \`env:"LODE_PLAN_TOKENS_SOFT"\`` and `PlanTokensHard string \`env:"LODE_PLAN_TOKENS_HARD"\``. `planbudget.go`:

```go
// planBudget parses the plan token budget from config (S6, S19): soft 32000
// and hard 64000 unless set; both positive, soft not above hard.
func planBudget(cfg Config) (soft, hard int, err error) { ... strconv.Atoi with defaults, errors naming the variable ... }

// planTokens estimates a body's tokens with the inverse of corpusindex.BudgetFor's
// runes-per-token ratio (7 runes to 4 tokens).
func planTokens(body string) int { return utf8.RuneCountInString(body) * 4 / 7 }

// planBudgetFor is the server budget with the project's settings overriding
// either bound.
func (s *server) planBudgetFor(ctx context.Context, projectID string) (soft, hard int, err error) {
	soft, hard = s.planSoft, s.planHard
	p, err := s.st.GetProject(ctx, projectID)
	if err != nil {
		return 0, 0, err
	}
	if v, ok := p.Settings["plan_tokens_soft"].(float64); ok { soft = int(v) }
	if v, ok := p.Settings["plan_tokens_hard"].(float64); ok { hard = int(v) }
	return soft, hard, nil
}

// checkPlanBudget refuses a plan body over the hard ceiling and returns the
// soft warning when over the soft one. Only plan bodies are measured.
func (s *server) checkPlanBudget(ctx context.Context, kind, projectID, body string) (warnings []string, err error) {
	if kind != "plan" {
		return nil, nil
	}
	soft, hard, err := s.planBudgetFor(ctx, projectID)
	if err != nil {
		return nil, err
	}
	n := planTokens(body)
	if n > hard {
		return nil, fmt.Errorf("plan body is about %d tokens, over the hard ceiling of %d (12 S19); split it (lode:splitting-specs-into-plans): %w", n, hard, store.ErrInvalidInput)
	}
	if n > soft {
		warnings = append(warnings, fmt.Sprintf("plan body is about %d tokens, over the soft budget of %d (12 S19); consider splitting it", n, soft))
	}
	return warnings, nil
}
```

`NewServer` parses `planBudget(cfg)` at boot and refuses to start on an error, storing the two ints on `server`. In `createDoc`, after the kind and project checks and before the write: `warnings, err := s.checkPlanBudget(ctx, req.Kind, req.Project, req.Body)`; an error is written through `mapStoreErr` (422). In `updateDocBody`: read the doc first (`s.st.GetDoc(ctx, id)`) to learn kind and project, then the same call with `req.Body`. Both handlers set `Warnings` on the returned `model.Doc` before `writeJSON`. `model.Doc` gains `Warnings []string \`json:"warnings,omitempty"\`` with a comment: "advisories about this write, never stored".

Tests in `planbudget_test.go`: `planTokens("")` is 0 and `planTokens(strings.Repeat("a", 7000))` is 4000; with a test server whose config sets `LODE_PLAN_TOKENS_SOFT=10` and `HARD=20`, creating a plan with a 100-rune body is 422, a 30-rune body returns a warning, a spec with a 100-rune body is fine; a project setting `plan_tokens_hard: 1000` lifts the refusal.

- [ ] **Step 3: API route and CLI**

`PATCH /api/v1/projects/{id}/settings` in `projectsettings.go`: body is a JSON object, decoded into `map[string]json.RawMessage`; `SetProjectSettings`; respond with `GetProject`. Guard: the same as the focus route. `model.ProjectSettingsInput` is `map[string]any` documented on the route in `project.go` (a named type keeps ADR 036's rule that request bodies are declared in `internal/model`).

`internal/cli/projectsettings.go`: `(c *Client) SetProjectSettings(ctx, id string, patch map[string]any) (model.Project, []byte, error)`.

`internal/cmd/project.go`: `lode project set <id> <key>=<value>...`. Values parse as JSON when they are valid JSON (`32000`, `true`, `"x"`), else as a string; `key=` with an empty value sends null (removes the key). Prints the project through the existing project render function. Add the `set` subcommand next to `focus`.

`lode doc add` and `lode doc edit` (in `cmd/doc.go`): after the success line, `for _, w := range d.Warnings { fmt.Fprintln(cmd.ErrOrStderr(), "warning:", w) }`.

- [ ] **Step 4: Env documentation and the skills**

Add the two variables to `deploy/base/configmap.yaml`, `docker-compose.yml` and the README's env table, following how `LODE_EMBEDDING_CONTEXT_TOKENS` was added (values 32000 and 64000, one-line description "plan token budget, soft and hard (12 S19)").

`splitting-specs-into-plans/SKILL.md`, in "Choosing the split": add one paragraph: "A plan is bounded by a token budget the server enforces (12 S19): about 32,000 tokens is the soft budget, past which `lode doc add` and `lode doc edit` print a warning, and 64,000 is the hard ceiling, past which the write is refused. A project may set `plan_tokens_soft` and `plan_tokens_hard` with `lode project set`. Split before the warning, not after the refusal." Rewrite that last sentence without the antithesis: "Split when the warning appears." `plan-spec/SKILL.md` Step 3: one sentence pointing at the budget.

Run `go test -trimpath ./internal/cmd -run 'TestAgentSurfaces|TestCommandReference|TestName' -v` and `./scripts/sync-codex-marketplace.py`; commit what they regenerate.

- [ ] **Step 5: Tests and commit**

Run: `go test -trimpath -race -count=1 ./internal/store ./internal/api ./internal/cmd ./internal/cli ./internal/model`.

```bash
git add internal/store/projectsettings.go internal/store/projectsettings_test.go internal/store/projects.go internal/api/projectsettings.go internal/api/planbudget.go internal/api/planbudget_test.go internal/api/server.go internal/api/docs.go internal/api/router.go internal/cli/projectsettings.go internal/model/project.go internal/model/doc.go internal/cmd/project.go internal/cmd/doc.go deploy/base/configmap.yaml docker-compose.yml README.md plugins/
git commit -m "Bound a plan by a token budget with a per-project settings override (S6, S19)"
```

---

### Task 6: Specs 03, 05, 09 and 12 describe what shipped

**Files:**
- Modify: `docs/specs2/03-tasks-and-execution.md` (§4 "Governing clauses" paragraph)
- Modify: `docs/specs2/05-documents.md` (§3 `doc_clauses` row; §4 clause paragraph; the plan lifecycle wherever plan statuses are described; the token budget)
- Modify: `docs/specs2/09-cli-and-skills.md` (the `work` row gains `next --replan`; the `project` row gains `set`; the `doc` row notes `list --status all`)
- Modify: `docs/specs2/12-spec-refactoring-design-tree.md` (heading rename; S37 to S44)

**Interfaces:** none.

- [ ] **Step 1: Read the coordination file** at the path in the preface; if increment 4a has already edited spec 09 or 12 on its branch, note the lines it named so the final rebase is expected to conflict only there.

- [ ] **Step 2: 03 §4**

Append to the "Governing clauses" paragraph: "When a plan closes, by `lode doc withdraw` or by its last task closing, every task minted from it that carries no governing link is linked to the plan's arranged clauses before the status moves, so a closed plan's tasks each carry at least one link (12 S5, S38)."

- [ ] **Step 3: 05**

§3 `doc_clauses` row: replace "the document's current arrangement" text with "the document's current arrangement. A spec or ADR arranges its own clauses; a plan arranges the clauses its `covers` entries reach, rewritten on every plan body write and again at accept (S16)". Add after the clause paragraph in §4: "**A plan arranges the clauses it covers** (12 S16, S37). Its `covers` entries are resolved to clauses with the walk 03 §4 states and written as its arrangement; the plan's own prose stays prose and mints no clause. Which plans cover a clause is therefore a membership fact read from the arrangement (S25), and the clause detail lists them. The `coverage:` levels and `fullCoverageWith` keep working as they are in this stage; retiring them is later work (S39)." Plan statuses: where the lifecycle is described, add: "A plan becomes `spent` when its last minted task closes (S5); `withdrawn` and `spent` plans are hidden from `lode doc list` and the cockpit list unless `--status all` or a status is named (S40). A plan whose arranged clause is withdrawn becomes `stale` (S23, S41). Re-planning a stale plan is asked for with `lode work next --replan` and never minted on its own (S28, S42)." Token budget: "A plan body is measured against a token budget (S19): the server's soft and hard values come from `LODE_PLAN_TOKENS_SOFT` and `LODE_PLAN_TOKENS_HARD`, a project may override either under `plan_tokens_soft` and `plan_tokens_hard` in its settings, a write over the soft budget carries a warning and a write over the hard ceiling is refused (S43, S44)."

- [ ] **Step 4: 09**

Find the `work`, `project` and `doc` rows in the command table and add `next --replan`, `set`, and `list --status all` in the existing style. No L3 change.

- [ ] **Step 5: 12**

Rename `## Recorded from increment 1` to `## Recorded from implementation` and change its lead sentence to "Behaviours the implementation increments fixed that the rounds above did not name." Append after S34 and S35 (keep S35 last of the existing block, then):

- S37 (S16 applied): a plan arranges the clauses its `covers` entries reach and mints none of its own; the arrangement is rewritten on every plan body write and again at accept, so a plan accepted after its spec grew governs the current clauses.
- S38 (S5 applied): a closed plan's tasks each carry a governing link because the store links every ungoverned task of the plan to the plan's arrangement when the plan is withdrawn or spent. This is the minimal form of S3's second writer.
- S39 (S25 staged): coverage as membership is the arrangement from S37; the `coverage:` levels and `fullCoverageWith` stay in force for their readers (listed in the increment 3 plan) until a plan retires them.
- S40 (S5 applied): `spent` is set by the store when a plan's last minted task closes; a plan with no minted task never becomes spent. Terminal plans are hidden from the default listings.
- S41 (S23 applied): `SetClauseStatus` is the one writer of clause status outside acceptance, and withdrawing a clause marks its accepted plans stale with cause `clause_withdrawn`.
- S42 (S28 applied): `lode work next --replan [ref]` mints one design task about the stale plan, or reuses the open one, and claims it. Nothing mints one unasked.
- S43 (S19 applied): the token estimate is the rune count times four sevenths, the inverse of the chunk budget's ratio, so the two budgets agree on what a token is.
- S44 (S6 applied): per-project settings are one JSONB column behind a server-side key allowlist; the plan budget keys are the first two. Other increments add keys, never columns.

Then update the Frontier paragraph's "S31 to S35" to "S31 to S44".

- [ ] **Step 6: Style check and commit**

Run: `git diff | grep '^+' | grep -nE '—|, not |rather than|instead of'`
Expected: no output.

```bash
git add docs/specs2/03-tasks-and-execution.md docs/specs2/05-documents.md docs/specs2/09-cli-and-skills.md docs/specs2/12-spec-refactoring-design-tree.md
git commit -m "Record plan arrangements, the plan lifecycle and the token budget in specs 03, 05, 09 and 12"
```

---

## Self-review

Spec coverage: S16 is Task 2 (R1); S25 is Task 2 plus the R3 staging ruling recorded in Task 6 (S39); S5 is Tasks 1 and 3 (`spent`, hidden terminal plans, governed on close; `withdrawn` already exists); S23 is Task 3 (`SetClauseStatus`); S28 is Task 4; S6 and S19 are Task 5. The "Increment 3" section of #687 lists exactly these seven.

Type consistency: `arrangePlan(tx, planID) error` is produced in Task 2 and called from `rebuildEdges` and `acceptPlanDoc` there; `planClauses(tx, planID) ([]int64, error)` keeps its signature and is consumed by `governPlanTasks` in Task 3; `governPlanTasks(tx, planID) error` is called by `settlePlan` and `WithdrawDoc` in Task 3; `MarkPlansStale` gains `cause` in Task 3 and its one caller changes in the same task; `SetClauseStatus(tx, now, clauseID, status, eventID)` is produced in Task 3 and tested there; `DocFilter.IncludeTerminal` is produced in Task 3 and read by the API selectors and the CLI flag in the same task; `ReplanOpts`/`ReplanNext`/`model.ReplanInput`/`Replan` are produced and consumed inside Task 4; `SetProjectSettings`, `model.Project.Settings`, `planBudgetFor`, `checkPlanBudget`, `model.Doc.Warnings` are produced and consumed inside Task 5. `Govern` is called in the four-argument form; the Global Constraints say how the rebase reconciles it.

Placeholder scan: the code blocks that say "read X first" name the file and the function to copy from; no step says "add validation" or "handle edge cases" without the code or the exact rule.
