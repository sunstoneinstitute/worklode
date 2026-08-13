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

## Obligations from 029 sec-9 on parts 3 and 4

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
recorded in this plan's "Obligations from 029 sec-9" section, so there is
nothing for this task to carry; do not resurrect the table. Old §10 (Out of
scope) closes the gap as §9.

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

## Known at hand-off

Carried out of part 2's execution, parked deliberately rather than fixed.

**25 bare mentions of retired spec numbers survive in the folded corpus**, across
seven of the nine documents: 016 (5), 017 (2), 019 (1), 020 (7), 021 (3), 029
(6), 032 (1). They name 014, 015, 018, 023, 027 and 028 — all documents that fold
into a survivor in parts 3 or 4 — in prose shapes the per-document grep never
matched: `rides spec 014`, `since spec 018`, `027 mints`, `014's document IRI`.
The ones carrying a `§` (`015 §5`, `014 §11.3`, `018 §8`, `028 §6`) are within
`refmap.py`'s prose form and get rewritten at cutover; the rest are invisible to
it and to the grep both, because rule 5 deliberately does not widen the regex — a
bare three-digit number matches dates, version strings and port numbers, and
widening it has already caused two critical bugs. **Part 5 must sweep these by
hand once parts 3 and 4 have established where each retired number lands.** They
were not repointed during part 2 because that destination did not exist yet.

**The folded corpus keeps retired `ls:` spellings wherever its sources used
them.** 016 §0's note telling readers to read `ls:` as `wl:` stays, because
`ls:Skill` and `ls:recommendsSkill` genuinely survive as backtick spans in §1, §3
and §7 — respelling them would drop text rule 7 and `--check --ids` both require,
and 016 declares no `allow_dropped_ids`. The `ls:`→`wl:` respelling is a
corpus-wide cleanup this fold deliberately does not attempt; part 1 hit the same
wall when it chose to drop 008's preamble rather than place it.

**The part-1 plan's `requires:` wording is ambiguous and produced a bug.** Line
~290 describes the union as "minus references that resolve inside this fold",
which was implemented as dropping any target declared anywhere in the fold rather
than only one folding into the *same* document. It silently deleted 017's edge to
016, and would have cost 020→019, 021→020, 022→016 and three of 032's. Fixed in
`fold.py` with a regression test; the part-1 plan is left as the record of what
part 1 decided. **The corrected rule: drop a `requires:` target only when it is a
source of the same document** — that is a self-require after cutover — and
otherwise keep it pointing at `docs/specs/` for `refmap.py` to repoint.

**Rule 4 has an axis the plan does not name.** It reaches document-status
qualifiers, because drafts count as current in this fold and a document's status
is a fact the fold changes: "014 is draft", "lands when 014 does". It does not
reach implementation-status ones, because whether code has shipped is untouched
by consolidating the corpus: "until 013 ships", "when 014 lands", "today it half
does". Rewriting one of the latter into an unconditional present-tense assertion
changed a claim's truth conditions and was part 2's only Critical finding. Where
one phrase glues both together — "which is Status `draft` with no
implementation" — drop only the document-status half and keep the rest verbatim.

**Two precedents parts 3 and 4 will need.** A merge needs glue text only when the
subsection opens on material with no thematic bridge to its parent — 005's did,
021's two did not, and both of 021's came out byte-identical to source once the
headings were removed. And a pointer from a surviving section into `dropped:`
material is rule 5's escalation case, not a deletion; 029 §3.3/§4/§7.3 were
resolved by deleting the pointer and keeping the claim, which review allowed to
stand but which parts 3 and 4 should escalate rather than repeat.

**013's `dropped:` reasons record a supersession wider than 013 itself claims,
and part 5 must resolve it before the source is gone.** 013's frontmatter
supersedes §3, §5 and §8 wholesale to 014 §6, but its inline notes scope the
supersession far more narrowly: §3's says only `task_docs` gives way and
`events.applied_at` survives; §5's and §8's sit after the Spec-drift bullet
only. So the folded 013 loses the `events.applied_at` column declaration and its
down-migration sentence — which folded §0 still refers to and §2.1 still relies
on, with nothing left in the corpus declaring it — plus the replay and poll
tests, the both-doctors test, the replay/poll/doctor acceptance criteria and
"Every command emits deterministic `--json`". 013 ends up the only folded
document with neither a Testing nor an Acceptance criteria section. `fold.yaml`
had no legal alternative: `--check` rejects a retired anchor in a `from:`, so
this is rot inherited from 013's accepted frontmatter that the fold makes
permanent rather than causes. The three `dropped:` reasons now name what is
lost. **Resolve it before part 5's `git rm -r docs/specs` makes it
irreversible** — that is the last moment the surviving material can be recovered
from the source.

**Letter-suffix cross-references are invisible to `refmap.py`, and fix 3 was
only one of them.** The prose regex is `\d+(?:\.\d+)*`, which cannot match a
letter suffix, so a reference like `019 §4.3a` is neither substituted nor
reported unmapped — and part 2's bare-number grep cannot see it either, which is
how a stale `(019 §4.3a)` survived every gate in folded 029. The same shape sits
at `docs/specs/026-design-doc-queries.md:207` and
`internal/cmd/show_test.go:774`, and `026 §4.2a` appears at six more sites
(`CLAUDE.md`, `docs/authoring-design-docs.md`,
`docs/specs/033-plan-section-coverage.md`, a 2026-08-03 plan, and twice in
`internal/designdoc/resolve.go`). **Part 5 must handle these by hand or widen
the regex deliberately** — widening is a planning-tier decision, since rule 5
records that widening this regex has already caused two critical bugs.

**Do not hand-repoint one inside `docs/specs2/`.** The final review called folded
029's `(019 §4.3a)` stale and it was repointed to `(019 §4.4)`; that was reverted,
and the revert is the rule to carry forward. `refmap.py` scans `docs/specs2/` on
purpose, because a folded document's prose still carries the *old* corpus's
section numbers and cutover is where they get converted. A letter-suffix
reference is exempt from that pass only while it stays unreadable. Repointing it
to `4.4` made it visible to the prose regex — and `mapping.yaml` maps old `4.4`
to new `4.5`, so the cutover run would have rewritten the now-correct pointer
into a wrong one, which `--dry-run` duly listed.

The invariant that keeps this coherent: **hand-repointing inside `docs/specs2/`
is only ever done for references `refmap.py` cannot see**, which is exactly what
the per-document grep selects for. Repointing a reference the rewriter *can* see
double-maps it. And a stale `§4.3a` is not an anomaly in a folded document —
every one of the nine still cites the pre-fold corpus wherever it names a
document parts 3 and 4 have not folded yet (`014 §11.3`, `028 §6`, `027`). One
more pre-fold-shaped reference is consistent with that state, and part 5 resolves
the whole set together rather than carrying one hand-made exception that needs
its own exclusion.

**Amendment absorptions took three shapes across the nine, and the citation
should not be one of the variables.** Rule 3 says the note goes away and what it
said does not, but it does not say where the amended text lands or whether the
amending spec stays cited: 013 §0 absorbed in place with no citation, 019 §2
appended with no citation, 016 §1 appended keeping the citation. Placement
varying with content is fine — some amendments belong where the amended claim
sits, others read as a coda. The citation varying is not: a reader cannot tell a
dropped citation from an amendment that never had one. **Parts 3 and 4 need one
convention, and choosing it is planning-tier work**, not a rewriter's call.

**016 §1 says "Two mints" and then adds a third.** The sentence introduces two
mints added to 006's mint set and disjointness axiom, the Turtle block declares
`wl:Skill` and `wl:recommendsSkill`, and the disjointness member list is a third
addition that never enters the block as its own declaration; §7's dependency
bullet repeats the undercount. Rule 6 correctly stopped the rewriter fabricating
Turtle to fix it, so this is a substantive spec correction and therefore
planning-tier work.

**013 keeps scars whose referents left the corpus.** Folded §0 says "the
`task_docs` link and the spec-drift engine it supported are superseded" and
folded §5 (Open questions) refers to "the (now-superseded) engine 3's third
finding". Neither `task_docs` nor engine 3 appears anywhere else in the folded
corpus — the reader is pointed at material that no longer exists to be pointed
at. Same bucket as the 013 over-scoped drops above: resolve both together
before part 5 removes the source.

**`epic` is live vocabulary in folded 020 but removed by folded 029.** 020 uses
it in §3.3 (`kind = "epic"` is rejected with 422, `validKinds`,
`epicForbiddenStates`) and at four other sites (§0, §2.1, and twice in §4's
table), while 029 §2 states `epic` is removed from `TaskKind`. 020's
transcription is faithful — 029's own §9 table named 018, 025, 028 and `ns/` as
the documents its change touches, and not 020 — so the gap is 029's, not the
fold's. **Parts 3 and 4 should add 020 to the epic sweep** they already owe
from that table.

**The `ls:` disposition should have gone back to planning tier rather than
closing as "stays".** `allow_dropped_ids` is the authorised mechanism for a
deliberate identifier drop with a stated reason, and three entries would have
let 016 ship respelled to `wl:`. Refusing to edit `fold.yaml` was right *for a
rewrite task* — rule 10 and the task checklist both forbid it — but the missing
step was escalating the choice, not the refusal. It is worth one `fold.yaml`
entry in part 5, not a corpus-wide project.
