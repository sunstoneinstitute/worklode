---
status: draft
covers: NO-SPEC
---
# Spec corpus consolidation — part 2: near-1:1 folds

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Fold the nine near-1:1 documents — 013, 016, 017, 019, 020, 021, 022,
029, 032 — from `docs/specs/` into `docs/specs2/`. Each keeps its own number and
filename and folds only its own source. The contract is the part-1 plan,
`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`: its
**Rewrite rules** 1–11 and **The per-document task template** bind every task
below verbatim; nothing here amends them.

**`fold.yaml` is already authored for these nine documents and is not to be
edited by any task.** The placement decisions — section merges, renumbering,
drops, the one `allow_dropped_ids` entry — are planning-tier work done on this
branch, and `docs/specs2/mapping.yaml` is already regenerated from them. A
rewrite task that cannot follow a rule stops and reports a `fold.yaml` defect
rather than editing `fold.yaml`.

Model per `MODEL_SELECTION.md`: implementation `sonnet` (each task is fully
specified with an objective gate), per-document review `sonnet` (near-1:1
mechanical diffs), whole-branch review before merge `opus`.

## 029 sec-9 obligations on parts 3 and 4

The one non-mechanical decision in this part. `029#sec-9` ("What this spec
changes elsewhere") is dropped as `absorbed`, like the corpus's other four
`Amendments to existing specs` sections — but unlike 014 §12, 015 §7, 025 §11
and 027 §11, spec 029 carries no `amends:` frontmatter and no target document
carries an inline `> **Amended by spec 029.**` note. Its claims are recorded
nowhere else in the corpus, so dropping the section is a promise that parts 3
and 4 fold them into the surviving documents. Drafts count as current in this
fold (part-1 decision), so 029's "at acceptance" table is folded as in force
despite its "until acceptance, none of the targets change" clause. The rows:

| 029 §9 row | Obligation | Part |
|---|---|---|
| 018 — epic-as-container retired; `checkHierarchy` parent rule; decompose no longer converts to epic | the 004 fold (018 is one of its sources) states the post-029 rule | 3 |
| 025 §6 — convergent, the epic is already dropped there | note only; the 025 fold's author verifies convergence | 4 |
| 025 §8 — replaced, not amended: Project is redefined and `milestone_id` is stored (029 §1, §2); 025's deletion of `wl:OngoingMaintenance` stands (029 §1 `horizon`) | the 025 fold states 029's definition, not 025 §8's | 4 |
| 025 plans 2–4 — re-planned: document identity moves to 029 §4's per-kind sequences | plan documents, outside the fold; part 4's planning pass records the disposition | 4 |
| 028 §2 — assignee exists, requirement satisfied | note only, verified when 028's material is folded | 4 |
| 028 §6 — reviewer sets re-expressed as 029 §7.1 approval rows | the fold of 028 §6's material states the approval-row form | 4 |
| `ns/` — `wl:Milestone`, `wl:Deliverable`, `wlc:TaskKind` minus `epic`, participants/approvals vocabulary | lands on the ontology files, not on any spec; recorded in `docs/follow-ups.md` on this branch | — |

## Global Constraints

Inherited from part 1, restated so a task reading only this plan misses none:

- `mapping.yaml` is generated from `fold.yaml` alone. Never hand-edit it, and
  never derive it by reading `docs/specs2/*.md`. It is already regenerated for
  part 2; no task runs `fold.py --mapping`.
- `fold.py --scaffold` refuses to overwrite an existing `docs/specs2/*.md`.
  Regenerating a document after the rewrite has started is a data-loss bug.
- Every anchor in the nine sources' `--with-drafts` view appears exactly once
  across `from:` and `dropped:` — already satisfied by `fold.yaml`;
  `--check --partial` must stay clean after every task.
- Do not renumber by hand. `new:` numbers come from `fold.yaml`; `secfmt.py`
  owns the `{#sec-N}` anchors in the written files.
- Introduce no new Python dependency beyond PyYAML.
- Part 2 creates the nine `docs/specs2/` documents and nothing else. It does
  not modify `docs/specs/`, `scripts/fold.py` or `scripts/refmap.py`, and it
  repoints no references outside `docs/specs2/` — the corpus-wide reference
  rewrite is part 5.
- `docs/plans/` gets its spec references repointed at cutover (part 5) and
  nothing else.

## Tasks

Each task is the part-1 plan's frozen per-document task template with the
document substituted, plus a context line recording what `fold.yaml` decided
for that document. Two template notes apply to all nine: the pre-commit hooks
cover `docs/specs2/` for `secfmt.py`/`secmeta.py` but nothing runs
`secindex.py` for you, so run the steps and see them clean before reporting;
and `--check` reporting `undeclared` on a heading you added is rewrite rule 10
working — delete the heading or escalate, never extend `fold.yaml`.

### Task 1 — Fold `013-reconciliation.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/013-reconciliation.md`.

**Context:** 11 live anchors, all placed 1:1. Four source sections (2.3, 3, 5,
8) were replaced wholesale by 014 §6 and are `dropped:`; the survivors close
the gaps (2.4→2.3, 4→3, 6→4, 7→5). §0 carries an inline amendment note from
014 §6 to absorb (rule 3). The "Engine 1"/"Engine 2" headings stay — two
engines remain after Engine 3's retirement — but body prose counting three
engines must state the two-engine reality (rule 4). No Acceptance criteria
section survives; that is a `fold.yaml` decision, not a loss to report.

- [ ] `./scripts/fold.py --scaffold --only 013-reconciliation.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/013-reconciliation.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 2 — Fold `016-org-wide-skills.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/016-org-wide-skills.md`.

**Context:** 10 live anchors, 1:1, numbers unchanged. §1 carries an inline
amendment note from 025 §4.1 to absorb (rule 3).

- [ ] `./scripts/fold.py --scaffold --only 016-org-wide-skills.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/016-org-wide-skills.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 3 — Fold `017-task-secrets.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/017-task-secrets.md`.

**Context:** 11 live anchors, 1:1, numbers unchanged, no amendments to absorb.

- [ ] `./scripts/fold.py --scaffold --only 017-task-secrets.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/017-task-secrets.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 4 — Fold `019-project-scoping.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/019-project-scoping.md`.

**Context:** 15 live anchors, all placed; the accepted corpus's letter-suffix
4.3a becomes a gapless 4.4 and old 4.4 becomes 4.5. §2 carries an inline
amendment note from 026 §4.2 to absorb (rule 3). `fold.yaml` pre-seeds one
`allow_dropped_ids` entry for §4.3a's source-side span artifact (the wrapped
`` `lode show 12` `` span); it covers the source side only — keep the span on
one line in the rewrite (rule 9).

- [ ] `./scripts/fold.py --scaffold --only 019-project-scoping.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/019-project-scoping.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 5 — Fold `020-inbox-import.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/020-inbox-import.md`.

**Context:** 15 live anchors, 1:1, numbers unchanged, no amendments to absorb.

- [ ] `./scripts/fold.py --scaffold --only 020-inbox-import.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/020-inbox-import.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 6 — Fold `021-images-in-task-bodies.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/021-images-in-task-bodies.md`.

**Context:** 22 live anchors into 20 sections: the two single-subsection
shapes merge into their parents — 1.1 (the two-booleans rationale) into
1 Storage, 5.1 (Media types) into 5 Upload — with rule 2 governing each merge
seam. Parents keep their numbers, so nothing renumbers; 8.1/8.2 and 11.1/11.2
stay as declared structure.

- [ ] `./scripts/fold.py --scaffold --only 021-images-in-task-bodies.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/021-images-in-task-bodies.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half). `refmap.py`
      cannot see a bare number, so these are the references nothing else will
      catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 7 — Fold `022-prometheus-metrics.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/022-prometheus-metrics.md`.

**Context:** 10 live anchors, 1:1, numbers unchanged, no amendments to absorb.

- [ ] `./scripts/fold.py --scaffold --only 022-prometheus-metrics.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/022-prometheus-metrics.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half). `refmap.py`
      cannot see a bare number, so these are the references nothing else will
      catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 8 — Fold `029-research-work-in-the-backbone.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/029-research-work-in-the-backbone.md`.

**Context:** 22 live anchors: 21 placed 1:1, and §9 ("What this spec changes
elsewhere") is `dropped:` as `absorbed` — its obligations on parts 3 and 4 are
recorded in this plan's "029 sec-9 obligations" section, so there is nothing
for this task to carry; do not resurrect the table. Old §10 (Out of scope)
closes the gap as §9.

- [ ] `./scripts/fold.py --scaffold --only 029-research-work-in-the-backbone.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/029-research-work-in-the-backbone.md` — repoint each hit by
      hand against `mapping.yaml` (rewrite rule 5's manual half). `refmap.py`
      cannot see a bare number, so these are the references nothing else will
      catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 9 — Fold `032-project-cockpit.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/032-project-cockpit.md`.

**Context:** 14 live anchors, 1:1, numbers unchanged, no amendments to absorb.

- [ ] `./scripts/fold.py --scaffold --only 032-project-cockpit.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/032-project-cockpit.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see
      a bare number, so these are the references nothing else will catch.
- [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
      required** until the last document is folded; without it the check
      reports every anchor of the unfolded corpus as unplaced.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.
