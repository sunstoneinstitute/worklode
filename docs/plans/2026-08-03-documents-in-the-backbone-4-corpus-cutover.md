---
status: accepted
covers:
  - docs/specs/025-documents-in-the-backbone.md#sec-2
  - docs/specs/025-documents-in-the-backbone.md#sec-11
requires:
  - 2026-08-03-documents-in-the-backbone-3-plan-acceptance.md
---
# Documents in the backbone 4/4: corpus cutover

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 4 of 4 (4 tasks; numbering restarts at 1 per part). See
part 1 for the series map. Parts 1–3 must be merged and deployed first.

**Goal:** End the transitional mirror (025 §2): import the git corpus into
the backbone, delete `docs/specs/` and `docs/plans/`, and retire the `sec*`
scripts and hooks — the backbone is then the only authoring surface.

**Architecture:** `lode doc import` is a client-side walker over the
`internal/designdoc` parser, writing through the public API only (the same
re-runnable-backfill shape as `lode inbox import`). Import is two passes —
create every document, then wire every edge — because frontmatter references
point in both directions across the corpus. Historical documents keep their
frontmatter `status` verbatim, and importing an accepted **plan** must not
mint tasks: the import path sets status directly, bypassing the accept
transaction. Only after verification does the deletion commit remove the
files and the hooks that guarded them.

**Tech Stack:** Go 1.25+, cobra CLI.

**Spec:** `docs/specs/025-documents-in-the-backbone.md` §2 (transitional
block), §11, §12

**On 025 §12:** corpus import is out of the spec's *design* scope, and §12
itself assigns it to "the implementation plan's final phase" — this part is
that phase. Because the spec deliberately designs none of it, every rule
below is a stated assumption, flagged in the preamble. The other §12
exclusions — Milestone, graph projection of docs, review tooling internals —
are not touched anywhere in this series.

**Gate — an explicit human go decision, not a merge.** Task 1 is ordinary
code and ships like any other. Tasks 2–4 change what the org's source of
truth *is* and run only when Stig has confirmed, in order:

1. The production Worklode instance is durable (backed up Postgres) and the
   org's agents reach it.
2. Parts 1–3 are deployed and the e2e suite is green against them.
3. A dry-run import against a scratch database reproduces the corpus
   (Task 2's verification) with zero defects.
4. The in-flight plans of this very series are either executed or re-imported
   consciously — the import snapshot is the cutover moment.

**Read first:**
- 025 §2 (the transitional paragraph — what this part ends), §11 (the
  CLAUDE.md / authoring-docs row)
- `internal/api/inbox_import.go` and `lode inbox import` — the re-runnable
  backfill precedent this command copies
- `docs/authoring-design-docs.md` — the checklist being retired (its plan
  task format section is canonical and survives, see Task 4)
- `.pre-commit-config.yaml`, `.github/workflows/_lint.yml` (the hooks and CI
  steps that go), `scripts/secfmt.py`, `scripts/secindex.py`,
  `scripts/currentspec.py` (the scripts that go — plus `scripts/secfrozen.py`
  if spec 026's plan has landed it by then; 026 §4.1 already rules it "is
  deleted with the files rather than ported")

**Assumptions the spec does not settle (flagged, not silently adopted):**

- **Executed plans import without tasks.** Forty spent plans must not mint
  thousands of tasks, so imported docs bypass the accept transaction — which
  means AC2's `accepted ⟺ tasks exist` holds *for documents accepted through
  `lode doc accept`*, not for imported history. The spec should say this;
  reported as a gap.
- **Plans with no `status` import as `accepted`.** The corpus's executed
  plans predate the status key (026 §2.2 calls them legacy); importing them
  as draft would list shipped work as pending review. Their task sets stay
  empty, and `--needs-execution` deliberately does not report empty sets
  (part 3 Task 5) — a spent plan is not pending work.
- **What stays in git:** `.worklode/implements.yaml` claims (025 §2 says so
  explicitly), `docs/follow-ups.md` (execution notes, not a design doc),
  `CLAUDE.md`, and a slimmed authoring guide. `docs/plans/index.yaml` and
  `docs/specs/index.yaml` are generated from the files and die with them.

**Non-goals:** registering the adjacent repos as projects so tier-2 shorthand
resolution comes alive (`docs/follow-ups.md` tracks it; it *needs* these
rows, but it is its own decision per repo); rewriting `WL-SPEC-nn` shorthand
handling (owned by `2026-08-03-spec-shorthand-references.md`); a web
consolidation view (026 §9).

---

## Tasks

### Task 1 — Build `lode doc import`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/cmd/docimport.go` (or a subcommand in `doc.go`),
  `internal/cli/client.go` methods
- Modify: `internal/api/docs.go`, `internal/store/docs.go` (import-only
  status, edge replacement endpoint)
- Test: `internal/cmd/doc_test.go` against a fixture corpus,
  `internal/api/docs_test.go`

- [ ] **Step 1: Write the failing tests**

Fixture corpus in `testdata/` (a spec with anchors and amendments, an
accepted plan with a `task:` key, a draft spec, a plan with no status, a
frontmatter reference to a doc later in the walk, and one unresolvable
cross-corpus shorthand):

| Behaviour | Assertion |
|---|---|
| two-pass import | the forward reference resolves to a real `to_doc`, not `to_external` |
| statuses | frontmatter status kept; status-less plan imported `accepted`; draft stays `draft` |
| no minting | zero tasks exist after importing the accepted plan |
| sections | imported accepted specs have every anchor `published`; `last_revised_in` = 1 (history is not reconstructed — pinned claims re-baseline at import, stated in the command's help) |
| idempotent | running the import twice changes nothing (external id per file path; second run is a no-op through `RecordEvent`'s dedupe, updates on drift) |
| unresolvable reference | stored in `to_external`, printed to stderr, exit 0 (the rdf-registry:ADR-0006 case) |
| `task:` key | recorded nowhere — the binding it stood for is `plan_doc`, which spent plans do not get (frontmatter key retired, 025 §11) |

- [ ] **Step 2: Implement**

- API: `POST /api/v1/docs` accepts `status` and `published_sections` only
  from an **admin** token (the same gate the admin endpoints use); the
  part-2 422 stays for everyone else. Add
  `PUT /api/v1/docs/{id}/edges` replacing the doc's outgoing edge set —
  pass 2's surface, reusing part 2's `rebuildEdges`.
- CLI: `lode doc import [--docs docs/] [--project <id>] [--dry-run]` — walk
  `docs/specs/*.md` + `docs/plans/*.md`, `designdoc.Parse` each, derive
  kind (`docs/specs/` + optional `kind: adr` frontmatter → spec/adr;
  `docs/plans/` → plan), number and slug from the filename, then pass 1
  creates, pass 2 wires edges. `--dry-run` prints the would-be corpus and
  every reference that will not resolve.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/cmd/ ./internal/api/ -count=1
```

---

### Task 2 — Import the real corpus and verify

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [1]
```

Runbook-shaped: no repo changes except recording the result. Runs only after
the gate above is green.

- [ ] **Step 1: Dry-run against a scratch database**

`lode doc import --dry-run` on the tip of `main`. Zero unexpected
unresolvables (the known one: `rdf-registry:ADR-0006`, `docs/follow-ups.md`
already tracks it). Document counts match the tree
(`ls docs/specs/*.md | wc -l`, same for plans).

- [ ] **Step 2: Import into production and verify**

Run the import. Then, against the server:

- `lode doc list --kind spec` count and statuses match the tree's
  frontmatter.
- `lode doc show` on three spot-checked documents is byte-identical to the
  files (the body column stores the source whole).
- `lode doc list --needs-planning` reproduces what 026's definition gives on
  the tree (000 plus whatever the corpus then holds).
- Every anchor of every accepted spec is `published` (SQL spot check).

- [ ] **Step 3: Record**

Append the import date, counts, and known unresolvables to
`docs/follow-ups.md` (which survives the cutover) so the cutover moment is
findable later.

---

### Task 3 — Delete the mirror, retire the guards

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [2]
```

**Files:**
- Delete: `docs/specs/`, `docs/plans/` (all files including both
  `index.yaml`s), `scripts/secfmt.py`, `scripts/secindex.py`,
  `scripts/currentspec.py` (and `scripts/secfrozen.py` if present)
- Modify: `.pre-commit-config.yaml`, `.github/workflows/_lint.yml`,
  `.github/workflows/pr-checks.yml`

- [ ] **Step 1: Remove the hooks in the same commit as the files**

- `.pre-commit-config.yaml`: drop the `section-numbers` entry (and 026's
  `section-permanence` if present). `migration-numbers` stays.
- `_lint.yml`: drop the `section numbers` step; the `ns codegen drift` step
  stays. Check `pr-checks.yml`'s docs-only-skip path filters: `docs/` keeps
  existing (follow-ups, the slimmed guide), so the filter survives, but
  verify nothing else references the deleted scripts
  (`grep -rn 'secfmt\|secindex\|currentspec' --exclude-dir=.git .`).
- `git rm -r docs/specs docs/plans scripts/secfmt.py scripts/secindex.py
  scripts/currentspec.py`.

The commit message names the import event (Task 2's date) as the authority
transfer.

- [ ] **Step 2: Verify**

The grep above returns nothing; pre-commit runs clean on the deletion
commit; `go test ./...` is green — in particular
`internal/designdoc/designdoc_test.go` and any test that round-tripped the
real corpus must be re-pointed at fixture corpora in `testdata/`, not
deleted (the parser is now the import/authoring path's, and keeps its
tests).

- [ ] **Step 3: Commit**

---

### Task 4 — Catch the written record up

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [3]
```

**Files:**
- Modify: `CLAUDE.md`, `docs/authoring-design-docs.md`
- Test: none — prose; reviewed by a human

Per 025 §11's last row. Keep both files short; they describe, they no longer
gate.

- [ ] **Step 1: `CLAUDE.md`**

- The architecture paragraph's ownership sentence loses its "once spec 025
  is implemented" hedge: the backbone owns execution facts **and**
  design-document artifacts; the graph is the projection.
- "Specs, plans, tasks": drop "(files under `docs/` are the transitional
  mirror until it is implemented)" and the kind parentheticals — the model
  paragraph now states current fact: `kind = 'design'`, plan acceptance
  mints the set, no root task. Replace the `secfmt.py` command line in the
  Commands block with `lode doc` equivalents; drop the frozen-anchor
  authoring bullet's file mechanics and point at `lode doc` + the accept
  gate.
- The Conventions bullet about `ns/` updates: the codegen step now exists —
  "amend the spec, edit the Turtle, run `scripts/nsgen.py`, ship the
  migration in the same commit".

- [ ] **Step 2: `docs/authoring-design-docs.md`**

Rewrite as the backbone authoring guide (a third the length): how to draft
(`lode doc new`), reference (unchanged forms plus shorthand, minus
file-path mechanics), amend/supersede (same three-part discipline, now
edges + inline notes in the body), and accept. **The plan task format
section is the canonical definition of what plan acceptance mints (part 3
Task 1 parses it) — it survives the rewrite verbatim.** The `task:` key
section is deleted — retired with the files (025 §11). The frozen-anchor
rules stay, restated as what the accept gate enforces rather than what a
hook rewrites.

- [ ] **Step 3: Commit**

---

## Done when

1. 025 §2's transitional paragraph is history: no `docs/specs/` or
   `docs/plans/` in the tree, and the corpus answers from `lode doc`.
2. Every remaining reference to the `sec*` scripts is gone; pre-commit and
   CI are green without them.
3. `docs/follow-ups.md` records the import; `CLAUDE.md` and the authoring
   guide describe the backbone flow with no mirror-era instructions left.
