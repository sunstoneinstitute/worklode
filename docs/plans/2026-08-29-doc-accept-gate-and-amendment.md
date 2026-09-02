---
status: accepted
covers:
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-6.1
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-7.3
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-escalation-and-grooming.md
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.2
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.3
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.4
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-8.5
    coverage: full
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-15.5
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-escalation-and-grooming.md
  - spec: docs/specs/025-documents-in-the-backbone.md#sec-24
    coverage: partial
    fullCoverageWith:
      - docs/plans/2026-08-29-decision-tasks.md
      - docs/plans/2026-08-29-escalation-and-grooming.md
      - docs/plans/2026-08-29-doc-version-graphs.md
---

# Documents part 2 — reviewer gate and in-place amendment

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** 025 §7.3's accept gate becomes mechanical — a stored reviewer set
on the document, acceptance refused until every stored reviewer has approved
the current version — and the §8.2–§8.5 in-place amendment path exists at
all: today `store.UpdateDocBody` refuses every edit to a non-draft spec or
ADR, so a fixer that resolves a plan gap has no way to land the fix short of
a full §7.2 revision. After this plan, `lode doc edit` on an accepted spec or
ADR runs the server-enforced §8.3 rule split: mechanically substantive edits
are refused naming the rule that fired, judged-substantive edits land with
the changed sections marked `patched` and review re-requested for the
original approvers, non-substantive edits land with a note attached. `lode
doc note` records anchored, never-blocking notes, and the §6.1 anchor depth
limit becomes a server setting with the one-way-safe raise/lower rule.

**Series:** part 2 of 4 over spec 025's unplanned sections. The siblings —
`decision-tasks-plan` (§10.1), `doc-version-graphs-plan` (§4.x), and
`escalation-and-grooming-plan` (§8.1, §8.6–§8.8, the rest of §15.5) — are
separate documents. The escalation part is `blockedBy:` this one (declared on
its side, per docs/authoring-design-docs.md): its §8.6 stale-marking runs off
this plan's edit path. The interfaces it builds on are listed below; keep
their names as stated or update that plan in the same breath.

**WL-359 has already shipped** ("Documents have no stored reviewer set: 025
§7.3's accept gate is not mechanical") — PR #367, merged, task closed
`deployed_dev`. It landed the `doc_reviewers` table (migration 0059),
`RequestDocApproval` reading the stored set instead of taking
`reviewers []string`, `SetDocReviewers` (whole-list replace), and
`lode doc reviewers <ref> --set a,b,c`. This plan no longer builds any of
that (WL-451 caught the two tasks below still describing it as unbuilt);
Task 5 is now a small adaptation checkpoint confirming Tasks 6-7 call the
real shipped shapes.

**Coordination with `2026-08-25-approvals-3-revision-binding-and-gates`
(draft, tasks unminted):** its Task 11 plans an accept gate over approvals
rows but leaves the reviewer set unstored ("a document's reviewers are
whatever the caller assigns"). Task 6 here delivers the same refuse-while-open
gate *reading the stored set*. If approvals-3 lands first, Task 6 narrows to
wiring the stored set into its gate; if this plan lands first, approvals-3's
Task 11 items 3–4 are discharged and its reviewer note goes stale — the
executor of whichever runs second says so in the PR instead of building the
gate twice.

**Boundary against WL-372** (review-tool design: meat for code, crit-style
anchored comments): §8.5's notes are deliberately smaller — one anchored
body, no threads, no per-note resolve state, never blocking anything. Task 4
must not grow toward WL-372; a request for threads or resolution on notes is
that design task's scope, not an extension here.

## Interfaces the escalation part builds on

Named here so `escalation-and-grooming-plan` can be written against this
document. Task numbers in parentheses.

- `store.PatchDoc(tx *sql.Tx, now time.Time, in store.DocPatchInput, eventID int64) (*model.Doc, *model.DocPatchResult, error)` (Task 7)
  with `store.DocPatchInput{ID, Body, Substantive, Note, ActorID, TaskID, SessionID}`
  and `model.DocPatchResult{ChangedAnchors []string, Classification string, RuleFired string, NewVersion int, UnexecutedCoveringPlans []int64}`.
  `UnexecutedCoveringPlans` is the §8.6 stale-marking seam: accepted plans
  covering a changed anchor with no claimed task — excluded from referrers by
  §8.2 — returned so stale-marking can run in the same transaction. The API
  handler for `POST /api/v1/docs/{id}/patch` is where the escalation part
  adds that call.
- `designdoc.MechanicalFindings(old, new *Doc) []Finding` and
  `designdoc.Finding{Rule, Anchor, Detail string}` (Task 2); rule names
  `new-dependency`, `ns-term`, `surface-token`, `acceptance-criteria`, plus
  the store-side `referrer`.
- `store.DocSectionReferrers(ctx, docID int64, anchor string) ([]model.DocReferrer, error)`
  and `model.DocReferrer{Kind, Ref, Rel, Title string}` (Task 3).
- Event `doc.patched`, payload
  `{"anchors": [...], "classification": "substantive"|"non-substantive", "rule": "<rule or 'judged' or 'none'>"}` (Task 7).
- `doc_sections.patched boolean` and
  `store.ClearPatchedSections(tx, docID int64, version int)` (Tasks 1, 8).
- `store.AddDocNote` / `store.ListDocNotes`, `model.DocNote` (Task 4).
- CLI: `lode doc edit <ref> --file f [--substantive] [--note "..."]` (Task 7),
  `lode doc note <ref>#sec-N --body "..."` (Task 4).

## Global Constraints

- ADR 036 layering holds: every wire shape in `internal/model` (stdlib-only),
  store scans into it, `internal/cmd` decides, `internal/cli` renders. New
  human-readable views are `cli.*Table`/`cli.*Render` functions in
  `internal/cli/docs.go`; no tabwriter or timestamp formatting in
  `internal/cmd`.
- New routes join `routeGuards` in `internal/api/router.go` or the server
  refuses to boot; all new doc routes here are `guardedAny(permDocWrite)` for
  mutations and `guardedAny(permDocRead)` for reads, matching their
  neighbours.
- Migrations: one new numbered pair, listed in
  `deploy/base/kustomization.yaml`, never editing a shipped file; run
  `./scripts/check-migrations.sh --no-fix` before committing (this plan is
  authored alongside sibling plans that may claim the same number — the
  pre-commit hook renumbers).
- Store mutations record outcomes on `worklode_doc_operations_total{op,outcome}`
  (`internal/store/metrics.go`) via the existing `RecordDocOp` path — extend
  the `op` label set, never add a per-document label. Nil-safe, tested.
- Store and API tests need Postgres with pgvector (`TEST_POSTGRES_DSN`); a
  silently skipped store test proved nothing. Every task leaves
  `go test -trimpath -race -count=1 ./...` green.
- `e2e/` drives public surfaces only.
- File naming: this feature's stems already exist
  (`model/doc.go`, `store/docs.go`, `api/docs.go`, `cli/docs.go`,
  `cmd/doc.go`); new store code that would push `store/docs.go` past the
  2000-line ceiling goes in feature-named siblings (`store/docnotes.go`,
  `store/docpatch.go` — WL-359 already put the reviewer-set code in
  `store/approvals.go` rather than a `docreviewers.go` this plan does not
  create).

## Decisions this plan executes (made against the spec; do not reopen)

- **Reviewer set lives in a `doc_reviewers` table**, keyed
  `(doc_id, reviewer)`, durable across versions — §7.3 mints a review task
  "for the original approvers" after a patch, which presupposes the set
  survives the version bump. Not a column on `docs` (it is a set), not
  derived from crew roles (who reviews stays a social choice per §7.3; roles
  can seed it later without schema change). This answers WL-359's "where the
  set lives" question.
- **An empty reviewer set keeps today's assignee-only gate.** Every existing
  document has no reviewers; making acceptance impossible for all of them is
  not a migration anyone asked for. The gate is mechanical exactly when a set
  is assigned.
- **The §8.3 mechanical rules, concretely** (the spec names categories; the
  server needs predicates — all evaluated over the *changed* sections, which
  `designdoc` computes by comparing old and new section text):
  - `referrer` — a changed anchor has a §8.2 referrer (store query, Task 3);
  - `new-dependency` — the frontmatter `requires` list gains an entry.
    Prose-level dependency detection is not mechanical; a prose dependency is
    the fixer's judged call, and §8.3's "uncertain counts as substantive"
    is the backstop;
  - `ns-term` — the set of `wl:`/`wlc:` tokens
    (`\bwlc?:[A-Za-z][A-Za-z0-9_]*\b`) in changed sections differs;
  - `surface-token` — inline code spans or fenced-block lines in changed
    sections differ. Deliberately coarse: schema, DDL, API routes, CLI
    flags, event names, IRIs and enum values all live in code formatting in
    this corpus, and a false positive routes to review, which is the side
    §8.3 tells us to fail toward;
  - `acceptance-criteria` — a changed section whose heading contains
    "acceptance criteria" or "definition of done" (case-insensitive).
- **§8.2 referrers, concretely:** accepted documents holding a `doc_edges`
  row with `rel IN ('requires','covers','amends','replaces')` whose
  `to_anchor` names the section, plus tasks in a claimed-but-unfinished state
  whose `plan_doc` is an accepted plan covering the section (read the exact
  state list from `internal/model`'s task states — every state a claim holds
  short of `done`/`dropped`). A document-level edge with no anchor claims the
  document, not the section, and does not count. Excluded, per §8.2: the plan
  whose task is doing the patching (`DocPatchInput.TaskID`'s `plan_doc`), and
  accepted covering plans with no claimed task — those are returned in
  `UnexecutedCoveringPlans` for the sibling's §8.6 stale-marking.
- **Patch is a new verb, not a widened `UpdateDocBody`.** `PUT
  /api/v1/docs/{id}/body` keeps its draft/plan semantics; `POST
  /api/v1/docs/{id}/patch` is the accepted-spec/ADR path. One route per
  meaning keeps the §8.4 refusal from leaking into draft editing. The CLI
  keeps one verb — `lode doc edit` picks the endpoint by document status.
- **A patch is a publication.** It snapshots the prior version (§4.5,
  `snapshotDocVersion`), bumps `docs.version`, sets `last_revised_in` on
  exactly the changed sections (§6 rule 5), and runs the §6 anchor gates —
  in-place amendment relaxes *review*, never anchor freeze.
- **`patched` clears on re-approval:** when the last open `doc` approval lane
  at the patched version resolves `approved`, `ClearPatchedSections` resets
  the flag (Task 8). Landing a full §7.2 revision also clears it —
  `rebuildSections` recreates rows with the column default.
- **Depth limit is process-wide server config** (`LODE_DOC_DEPTH_LIMIT`,
  default 3), set once at boot — §18 calls it a server setting on the
  existing admin configuration path, which for `lode-server` is env/flags.
  No settings table. The one-way-safety of §6.1 needs no stored history:
  every publication re-checks the current limit, and a violation on a
  section already `published` is the "lowered limit orphans accepted
  anchors" rejection, named as such.

## Tasks

### Task 1 — Migration: doc_notes, doc_sections.patched

```yaml
kind: feature
priority: high
skills:
  - worklode-migrations
```

**WL-451 note:** this task originally also created `doc_reviewers` — that
table shipped independently via WL-359 (migration 0059, merged before this
plan's acceptance) and is dropped from here. `doc_notes` and
`doc_sections.patched` are still unbuilt as of this revision; re-verify
against `origin/main` before executing, the way this correction did,
rather than trusting this document's word for it.

One migration pair at the next free number (run
`./scripts/check-migrations.sh --no-fix`; sibling plans are claiming numbers
concurrently). Up:

```sql
-- 025 §8.5: anchored, non-blocking notes. task_id/session_id link the note
-- to what raised it; both nullable — a human at a prompt has neither.
CREATE TABLE doc_notes (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_id     bigint NOT NULL REFERENCES docs(id) ON DELETE CASCADE,
    anchor     text NOT NULL,
    body       text NOT NULL,
    task_id    text REFERENCES tasks(id),
    session_id text,
    created_by text REFERENCES actors(id),
    created_at timestamptz NOT NULL
);
CREATE INDEX doc_notes_doc_anchor ON doc_notes (doc_id, anchor);

-- 025 §7.3: approved text, modified since. Set by the §8.2 patch path,
-- cleared when the original approvers re-approve.
ALTER TABLE doc_sections ADD COLUMN patched boolean NOT NULL DEFAULT false;
```

Down drops the table and the column. List both files in
`deploy/base/kustomization.yaml`. No Go code in this task beyond what the
migration test harness already applies.

- [ ] `./scripts/check-migrations.sh --no-fix` — no collision reported.
- [ ] `make test` against Postgres — existing store tests still green (they
      apply all migrations).
- [ ] Commit: `Migration: doc_notes, doc_sections.patched (025 §8.5, §7.3)`.

### Task 2 — Pure edit classification: designdoc.MechanicalFindings

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

`internal/designdoc/patch.go`: the §8.3 mechanical rules that are text
properties, as a pure function — no DB, no HTTP, so the escalation part and
the store both call it and the table test needs no harness.

```go
// Finding is one §8.3 mechanical-substantive rule firing on one section.
type Finding struct {
    Rule   string // new-dependency | ns-term | surface-token | acceptance-criteria
    Anchor string // "" for a document-level finding (new-dependency)
    Detail string // what changed, for the refusal message
}

// ChangedAnchors returns the anchors whose section text differs between old
// and new, in document order. A section present in only one of them counts.
func ChangedAnchors(old, new *Doc) []string

// MechanicalFindings runs the text-level §8.3 rules over the changed
// sections. The referrer rule is a corpus query and lives in the store.
func MechanicalFindings(old, new *Doc) []Finding
```

Rule predicates exactly as fixed in "Decisions" above. Reuse the section
parsing this package already has; `CompareSections`
(`internal/designdoc/diff.go`) is the anchor-diff precedent — extract or
extend rather than re-parsing. First test, table-driven:

```go
func TestMechanicalFindings(t *testing.T) {
    cases := []struct {
        name      string
        old, new  string // full markdown bodies
        wantRules []string
    }{
        {"reworded prose, no assertion changed", specWith("plain text"), specWith("plain text, clarified"), nil},
        {"requires gains an entry", specRequiring(), specRequiring("029-research-work.md"), []string{"new-dependency"}},
        {"wl: token added", specWith("nothing"), specWith("emits `wl:DocumentAccepted`"), []string{"ns-term", "surface-token"}},
        {"code span changed", specWith("run `lode doc show`"), specWith("run `lode doc get`"), []string{"surface-token"}},
        {"fenced DDL line changed", specWithFence("a text"), specWithFence("a bigint"), []string{"surface-token"}},
        {"acceptance criteria reworded", specSection("Acceptance criteria", "old"), specSection("Acceptance criteria", "new"), []string{"acceptance-criteria"}},
        {"unchanged section not scanned", /* change §2 only; §3 holds a wl: token in both */ },
    }
    // ...
}
```

Also table-test `ChangedAnchors`: reword one section → exactly its anchor;
add a section → its anchor; byte-identical bodies → empty.

- [ ] `go test -trimpath ./internal/designdoc -run 'TestMechanicalFindings|TestChangedAnchors' -count=1` — `ok`.
- [ ] Commit: `designdoc: §8.3 mechanical edit classification, pure and table-tested`.

### Task 3 — The §8.2 referrer query, store to CLI

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

One reader for the fact family "which open work points at this section", used
by Task 7's gate and exposed as a surface so a fixer can check before
editing.

- `internal/model/doc.go`: `DocReferrer{Kind, Ref, Rel, Title string}` —
  `Kind` is `doc` or `task`; `Ref` is the citable id (doc slug, task id).
- `internal/store/docs.go` (or `docpatch.go` if docs.go is near the
  ceiling): `DocSectionReferrers(ctx, docID, anchor)` plus a tx-scoped
  variant Task 7 calls inside the patch transaction. The query, per the
  "Decisions" interpretation: accepted docs via anchored
  `requires|covers|amends|replaces` edges; claimed-but-unfinished tasks via
  `plan_doc` → accepted plan → anchored `covers` edge.
- `internal/api`: `GET /api/v1/docs/{id}/referrers?anchor=sec-N` →
  `{"referrers": [...]}`; route guarded `guardedAny(permDocRead)`.
- `internal/cli/docs.go`: client method + `cli.DocReferrersTable`;
  `internal/cmd/doc.go`: `lode doc referrers <ref>#sec-N [--json]`. The
  section fragment is required — a referrer is a section-level fact.

First store test:

```go
func TestDocSectionReferrers(t *testing.T) {
    // spec A accepted with sec-2; spec B accepted, requires A#sec-2;
    // plan P accepted covering A#sec-2 with task T claimed;
    // plan Q accepted covering A#sec-2, tasks unclaimed.
    refs, err := st.DocSectionReferrers(ctx, specA.ID, "sec-2")
    // want: B (doc, requires) and T (task, covers) — and never Q.
}
```

Also test: draft doc with the same edge → not a referrer; doc-level edge
(no anchor) → not a referrer; done task on P → not a referrer.

- [ ] `go test -trimpath ./internal/store -run TestDocSectionReferrers -count=1` against Postgres — `ok`.
- [ ] `go test -trimpath ./internal/api ./internal/cmd -run Referrer -count=1` — `ok`.
- [ ] Commit: `Referrer query over doc sections: store, GET route, lode doc referrers (025 §8.2)`.

### Task 4 — lode doc note: anchored, non-blocking notes

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

§8.5 exactly, and no more: one body against a frozen anchor, linked to the
task and session that raised it. No threads, no resolve state, blocks
nothing — that larger surface is WL-372's design task.

- `internal/model/doc.go`: `DocNote{ID, Doc, Anchor, Body, Task, Session,
  CreatedBy string/int64..., CreatedAt time.Time}` and request shape
  `AddDocNoteInput{Anchor, Body, Task, Session string}`.
- `internal/store/docnotes.go`: `AddDocNote(tx, now, docID, in, actorID,
  eventID)` — refuses an anchor the document does not have (a note is
  anchored or it is noise) and an empty body; `ListDocNotes(ctx, docID)`;
  `RecordDocOp` op `note`. Plans have no sections, so a note on a plan is
  ErrInvalidInput.
- `internal/api/docs.go`: `POST /api/v1/docs/{id}/notes`
  (`guardedAny(permDocWrite)`, event `doc.note_added` via `recordDocEvent`);
  `GET /api/v1/docs/{id}/notes` (`guardedAny(permDocRead)`); notes included
  on the doc detail response so `lode doc get --json` carries them.
- `internal/cli/docs.go` + `internal/cmd/doc.go`:
  `lode doc note <ref>#sec-N --body "..."` (`--body-file` per the §18
  convention; task/session ids resolved the way other in-worktree commands
  resolve them — reuse, don't re-derive), and `lode doc list --has-notes`
  (server-side filter, one EXISTS).
- Rendering: `lode show <ref>` / `lode doc get` render a section's notes
  inline under it — attributed one-liners, not a second document.

First store test: add a note to an accepted spec's real anchor → listed with
task and actor; anchor `sec-99` → ErrInvalidInput; note on a plan →
ErrInvalidInput; `--has-notes` filter returns exactly the noted doc.

- [ ] `go test -trimpath ./internal/store -run TestDocNote -count=1` against Postgres — `ok`.
- [ ] `go test -trimpath ./internal/api ./internal/cmd -run 'DocNote|HasNotes' -count=1` — `ok`.
- [ ] Manual: `lode doc note WL-SPEC-25#sec-8.5 --body "smoke"` then
      `lode show WL-SPEC-25 -s sec-8.5` shows the note inline.
- [ ] Commit: `lode doc note: anchored non-blocking notes with doc_notes (025 §8.5)`.

### Task 5 — Adapt to the shipped reviewer-set storage (WL-359 landed independently)

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**WL-451 correction:** this task originally re-specified WL-359's whole
scope (a `doc_reviewers` table, `RequestDocApproval` reading it,
`SetDocReviewers`, `lode doc reviewers`) as unbuilt. WL-359 shipped
independently (PR #367, merged, migration `0059_doc_reviewers`) before
this plan reached acceptance. Nothing here builds that storage again —
this task is a checkpoint confirming Tasks 6-7 call the real shipped
shapes, since they differ from what this task originally proposed:

- `internal/store/approvals.go`'s `RequestDocApproval(tx, now, docID,
  version) error` — no `reviewers` parameter; it reads the stored set
  internally and refuses `"doc %d has no assigned reviewers; set them
  with `lode doc reviewers` first"` when empty. Task 7's re-request-review
  call site must match this signature (four args, no reviewer list).
- `internal/store/approvals.go`'s `SetDocReviewers(tx, now, docID,
  actorID, reviewers []string, eventID)` — a **whole-list replace**, owner-
  or-admin gated, not an add/remove diff. Nothing in this plan calls it
  directly (Task 6's gate only reads approvals, never assigns reviewers),
  so this is a read-only confirmation, not new code.
- `internal/cmd/docreviewers.go`'s `lode doc reviewers <ref> [--set
  a,b,c]` and `POST /api/v1/docs/{id}/reviewers` already exist and need no
  changes from this plan.

Re-verify against `origin/main` before starting Task 6 — this section is a
snapshot at revision time, not a live source of truth.

- [ ] Diff Task 6's and Task 7's draft implementations against the real
      `RequestDocApproval`/`SetDocReviewers` signatures above; adjust call
      sites, not the store functions themselves.
- [ ] `go test -trimpath ./internal/store -run 'TestRequestDocApproval|TestSetDocReviewers' -count=1` against Postgres — `ok` (no new tests; confirms the existing WL-359 tests still cover what this plan leans on).
- [ ] Commit only if a call-site adjustment was needed: `Adapt to WL-359's shipped reviewer-set storage`.

### Task 6 — The mechanical accept gate

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [5]
```

`AcceptDoc` (`internal/store/docs.go`), spec/ADR branch, after the existing
`checkDocAssignee`: when the document has stored reviewers, refuse unless
every one of them holds an `approved` approvals row for
`("doc", DocEntityID(id))` at the document's current version, and refuse
while any `awaiting` or `changes_requested` row is open at that version. The
refusal names who is missing and points at `/reviews`. Rows at older
versions never block (they were superseded by the version bump). An empty
reviewer set keeps today's assignee-only behavior — nothing in flight is
trapped. Extend `CheckDocAcceptable` identically so the pre-flight and the
act cannot disagree.

Metrics: the gate's outcomes ride the existing
`worklode_doc_operations_total{op="accept"}` counter — add outcomes
`refused-reviewers` (missing approvals) and keep `invalid`/`ok` as-is;
extend the metric's help text and its test.

Coordination note for the executor: if
`2026-08-25-approvals-3-revision-binding-and-gates` Task 11 has landed by
the time this runs, its gate items (3)–(4) already exist — this task then
only swaps its "any open row" check to also require the stored set's
approvals, and says so in the PR.

First store test:

```go
func TestAcceptDocReviewerGate(t *testing.T) {
    // reviewers {a, b}; submit → two awaiting lanes at v1.
    _, _, err := store.AcceptDoc(tx, now, doc.ID, assignee, ev)
    // want ErrForbidden naming a and b.
    // approve a's lane; accept again → still refused, names b.
    // approve b's lane; accept → succeeds.
    // no reviewers on another doc → accept succeeds as today.
}
```

- [ ] `go test -trimpath ./internal/store -run 'TestAcceptDoc|TestCheckDocAcceptable' -count=1` against Postgres — `ok`.
- [ ] `make test` — green (existing accept tests use docs with no reviewers
      and must pass unchanged).
- [ ] Commit: `AcceptDoc: mechanical multi-approval gate over the stored reviewer set (025 §7.3)`.

### Task 7 — PatchDoc: the §8.4 in-place amendment path, store to CLI

```yaml
kind: feature
priority: critical
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3, 4, 5]
```

The load-bearing task: the path `UpdateDocBody` refuses today. One mutation
across every surface — store write, event, metric, API route, CLI verb —
per the layering rules.

**Store** (`internal/store/docpatch.go`): `PatchDoc(tx, now, in
DocPatchInput, eventID)` for an *accepted spec or ADR* only (drafts and
plans keep `UpdateDocBody`; superseded is refused):

1. Lock the row (`lockDoc`); parse old and new bodies; compute
   `designdoc.ChangedAnchors`. No changed sections and identical frontmatter
   → ErrInvalidInput ("nothing changed").
2. Run the §6 anchor gates the way `AcceptRevision` does (anchors
   append-only, no renumber, depth limit) — in-place amendment never relaxes
   anchor freeze.
3. Mechanical rules (§8.4 — server-side, the caller has no say):
   `designdoc.MechanicalFindings(old, new)` plus the referrer rule via
   Task 3's tx-scoped query over each changed anchor, minus the exclusions
   (`in.TaskID`'s own plan; unexecuted covering plans). Any finding →
   ErrInvalidInput naming rule, anchor and detail, and telling the caller
   `lode doc revise` is the path. Refusal outcome `refused-mechanical` on
   `RecordDocOp` op `patch`.
4. `in.Substantive == false` requires `in.Note` non-empty (§8.5: a fixer
   records what it changed and why); the note lands via Task 4's
   `AddDocNote` against each changed anchor (one note, first changed anchor,
   body prefixed with the anchor list — keep it one row).
5. Publish: `snapshotDocVersion`, `version++`, UPDATE body/title,
   `rebuildSections` **preserving** `published` and existing `patched`
   flags and setting `last_revised_in = newVersion` on exactly the changed
   anchors (§6 rule 5), `rebuildEdges`, `logDocChange`.
6. `in.Substantive == true`: set `patched = true` on the changed anchors and
   call the already-shipped `RequestDocApproval(tx, now, id, newVersion)`
   (WL-359; confirmed by Task 5) — review
   re-requested for the original approvers; the document **stays
   `accepted`** (§7.3). A substantive patch on a document with no stored
   reviewers is refused up front — there is no one to re-approve it, so the
   honest paths are assigning reviewers or `lode doc revise`.
7. Return the doc plus `model.DocPatchResult` (shape in the Interfaces
   section) — `UnexecutedCoveringPlans` filled from the referrer exclusion
   set, for the escalation part.

**API** (`internal/api/docs.go`): `POST /api/v1/docs/{id}/patch`,
`guardedAny(permDocWrite)`, request `model.PatchDocInput{Body, Substantive,
Note, Task, Session}`, event `doc.patched` via `recordDocEvent` with payload
`anchors` / `classification` / `rule` (§15.5: "with the substantive
classification and the rule that decided it" — `rule` is the refusing rule
on a 4xx, `judged` or `none` on success). Response: the doc plus the patch
result.

**CLI**: `lode doc edit <ref> --file f` gains `--substantive` and
`--note "..."`. `internal/cmd` picks the endpoint by fetched status/kind:
draft or plan → `PUT .../body` exactly as today; accepted spec/ADR →
`POST .../patch`. The mechanical refusal renders as the server's message —
no client-side pre-check that could drift from §8.4's server-side rule.
Confirmation line names the classification and, for substantive, the
re-opened reviewer lanes.

First store test, the rule-split table:

```go
func TestPatchDocRuleSplit(t *testing.T) {
    cases := []struct {
        name        string
        mutate      func(body string) string
        substantive bool
        note        string
        wantErr     string // "" = lands
        wantPatched []string
    }{
        {"non-substantive wording with note", reword, false, "clarified §2", "", nil},
        {"non-substantive without note", reword, false, "", "note", nil},
        {"referenced section refused", rewordSec2, false, "n", "referrer", nil}, // spec B requires A#sec-2
        {"requires grows refused", addRequire, false, "n", "new-dependency", nil},
        {"judged substantive marks patched", narrowRule, true, "", "", []string{"sec-3"}},
        {"substantive with no reviewers refused", narrowRule, true, "", "no reviewers", nil},
    }
    // after the substantive case: doc still accepted, version bumped,
    // last_revised_in == new version on sec-3 only, awaiting lanes open.
}
```

- [ ] `go test -trimpath ./internal/store -run TestPatchDoc -count=1` against Postgres — `ok`.
- [ ] `go test -trimpath ./internal/api ./internal/cmd -run 'PatchDoc|DocEdit' -count=1` — `ok`.
- [ ] `go test -trimpath ./internal/cmd -run TestRenderRule -count=1` — `ok` (no hand-rendering crept in).
- [ ] Commit: `lode doc edit patches accepted specs in place: §8.3 rule split, doc.patched, patched sections (025 §8.2–§8.4, §15.5)`.

### Task 8 — Review follow-through: mint the re-review, clear patched

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [7]
```

§7.3's sentence "a `review` task is minted for the original approvers", and
its closing bracket.

- **Watcher rule** (`internal/watcher/doclifecycle.go`): `review-on-patch` —
  on a `doc.patched` event with `classification: substantive`, mint a
  `review` task on the document ("Re-review patched sections of <ref>:
  sec-…"), suppressed while an open `review` task already references the
  document (the same `OpenReviewTask` guard the submit rule uses). Pure,
  table-tested beside the existing rules; add the rule name to the bounded
  `rule` label list in `internal/watcher`'s metrics.
- **Executor** (`internal/api/docwatch.go`): perform the mint the way the
  existing rules do; idempotent across event redelivery.
- **Clearing** (`internal/store`): `ClearPatchedSections(tx, docID, version)`
  — reset `patched` on sections with `last_revised_in <= version`. Call it
  from the approvals decide path when the decision resolves the *last* open
  `doc` lane at that version as `approved`. A later §7.2 revision landing
  also clears (rebuild default) — assert it in the test rather than trusting
  the observation.
- `lode doc show` / `lode show`: a patched section renders with a `patched`
  marker and its notes inline (§7.3) — extend the Task 4 rendering, one
  marker, no new render path.

Tests: watcher truth table (mint on substantive, suppress on
non-substantive, suppress while open); docwatch redelivery mints once;
store test — approve one of two lanes → still patched, approve the second →
cleared.

- [ ] `go test -trimpath ./internal/watcher ./internal/api ./internal/store -run 'TestDocLifecycle|TestReviewOnPatch|TestClearPatched' -count=1` against Postgres — `ok`.
- [ ] Commit: `review-on-patch watcher rule; re-approval clears patched (025 §7.3)`.

### Task 9 — Depth limit as server configuration

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
```

§6.1: the limit is server-configurable, default 3, one-way-safe by
construction.

- `cmd/lode-server/main.go` + `internal/serverapp`: `LODE_DOC_DEPTH_LIMIT`
  (and a `-doc-depth-limit` flag mirroring the dsn pattern); reject values
  < 1 at boot with a clear error. Document it where the server's other env
  is documented.
- `internal/store`: a package-level limit set once at boot before serving
  (`store.SetDocDepthLimit(n int)`, defaulting to `designdoc.DepthLimit`) —
  the three call sites (`docs.go:164`, `docs.go:396`,
  `docrevisions.go:186`) read it instead of the const. Boot-time-only
  mutation, stated on the setter; tests that change it restore via
  `t.Cleanup` and don't run parallel.
- The lowering rule falls out of re-checking at publication, but the error
  must name the §6.1 case: when a depth violation involves a section already
  `published`, the message is
  `depth limit N orphans accepted anchors: sec-… (§6.1: lower the limit only for documents never accepted)`
  — distinct from the plain draft refusal, naming the anchors (acceptance
  criterion 12).
- `lode doc anchors` (client-side lint, `internal/cmd/doc.go`) keeps the
  compiled default and says so in its output when it flags depth — the
  server is the authority; the lint is advisory pre-flight.

Tests (store, table over limits): limit 4 accepts an anchored `####` that
limit 3 refuses; a doc accepted with depth-3 anchors, then limit lowered to
2 → `AcceptRevision` of a candidate refused naming the depth-3 anchors; a
never-accepted draft under limit 2 → plain refusal, no "orphans" wording.

- [ ] `go test -trimpath ./internal/store -run TestDocDepthLimit -count=1` against Postgres — `ok`.
- [ ] `LODE_DOC_DEPTH_LIMIT=0 bin/lode-server` refuses to boot, naming the variable.
- [ ] Commit: `Anchor depth limit is server config, one-way safe (025 §6.1)`.

### Task 10 — e2e: the gate and the amendment path over public surfaces

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4, 5, 6, 7, 8]
```

`e2e/doc_patch_test.go` (build tag `e2e`), public surfaces only, extending
the existing doc e2e harness:

1. Create a spec via `/api/v1/docs`, `POST .../reviewers` add two actors,
   submit → `GET /api/v1/approvals?entity_kind=doc` shows two awaiting lanes
   at v1.
2. `POST .../accept` → 4xx naming both reviewers. Approve both lanes through
   the existing decide surface → accept succeeds.
3. `POST .../patch` non-substantive with a note → 200, version bumped,
   `GET .../notes` carries the note, doc page renders it.
4. Create a second accepted spec that `requires` the first's section; patch
   that section again → 4xx naming `referrer` and the section.
5. Substantive patch on an unreferenced section → 200; the doc detail shows
   the section `patched`, awaiting lanes reopen at the new version, and the
   `review` task exists; approve the lanes → patched flag gone.
6. `lode event tail` (or the API read) shows `doc.patched` with
   classification and rule.

- [ ] `go test -trimpath -race -count=1 -tags e2e ./e2e/ -run TestDocPatch` against Postgres — `ok`.
- [ ] Full suite: `make test-e2e` — green.
- [ ] Commit: `e2e: reviewer gate, in-place amendment, notes and patched lifecycle`.

## Verification

- `make test` and `make test-e2e` green against Postgres with pgvector.
- `lode doc todo WL-SPEC-25 --json` no longer lists `sec-6.1`, `sec-7.3`,
  `sec-8.2`, `sec-8.3`, `sec-8.4`, `sec-8.5` as unplanned once this plan is
  in the backbone; `sec-8`, `sec-15.5`, `sec-24` report partial with the
  named siblings.
- Acceptance criteria touched here: 025 §24 items 6 (accept stays manual and
  assignee-gated — now also reviewer-gated), 12 (depth limit raise/lower),
  26 (submit unchanged), 30 (metrics registered and tested).

## Deferred — stated so each gap is a decision

- **§8.1 `lode task escalate`, §8.6 stale plans, §8.7 grooming, §8.8 tier
  routing, and §15.5's `task.gap_found`/`fix.*`/`doc.stale` events** — the
  escalation-and-grooming sibling, built on the interfaces above.
- **Prose-level dependency detection** (§8.3 "in its prose") — not
  mechanical; the judged path plus "uncertain counts as substantive" covers
  it, and the fixer-facing skill (escalation part) carries the wording.
- **`lode task brief` rendering of patched sections** (§7.3 names it beside
  `doc doc show`) — brief assembly is the escalation part's §8.7 rendering
  ground; the patched flag and notes it needs are stored and served here.
- **Reviewer-set seeding from crew roles** — schema permits it later;
  assigning stays manual per §7.3.
- **Threads/resolution on notes, meat-abridged review** — WL-372's design
  task, deliberately out.
