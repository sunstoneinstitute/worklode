---
status: draft
covers: NO-SPEC
---
# Spec corpus consolidation — part 4: the graph and document clusters

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Fold the last four documents —
`006-knowledge-graph.md` ← {006, 003, 009, 015},
`007-drift-and-overview.md` ← {007},
`025-documents-in-the-backbone.md` ← {025, 014, 027, 028, 034}, and
`026-design-doc-queries.md` ← {026, 033} — from `docs/specs/` into
`docs/specs2/`. The contract is the part-1 plan,
`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`: its
**Rewrite rules** 1–11 and **The per-document task template** bind every task
below verbatim; nothing here amends them. Part 3's rulings 1–5 are inherited
as its own text says they are.

**`fold.yaml` is already authored for these four documents and is not to be
edited by any task.** With part 4's entries merged, `fold.py --check` passes
**without `--partial`** for the first time: every live anchor in `docs/specs/`
is now placed or dropped, and `docs/specs2/mapping.yaml` is regenerated from
the complete placement before any rewrite task runs. A rewrite task that
cannot follow a rule stops and reports a `fold.yaml` defect rather than
editing `fold.yaml`.

Model per `MODEL_SELECTION.md`: tasks 1 (006), 3 (025) and 4 (026) are
implemented at `opus` — each applies substantive merge rulings across sources
that amend each other, so the Sonnet row's "fully specified, no open design
decisions" bar is not met. Task 2 (007) is `sonnet`: one source, 1:1
placement, a renumber and a mechanical respell. Per-document review and the
whole-branch review before merge are `opus`.

## Rulings from the planning pass

These bind every task below. Three of them are the placements themselves,
taken because a source belongs to exactly one target (part-3 ruling 2) and
recorded here because each had a real alternative:

1. **014 folds into 025.** 025's sections are very largely amendments to
   014's — §0, §2, §4, §5 and §10 amended, §8 superseded — so folding them
   together is what lets the end state be stated once instead of as a claim
   and its reversal. The cost is paid in two places and both are handled
   below: 014 §1 (the prefix rename) lands under `ns/` is the schema source,
   and 014 §6 (the implements manifest and its deriver) sits in the document
   store rather than beside 007's other derivers, which cite it.
2. **027 and 028 fold into 025.** The event log's natural home, 004, is
   already folded and closed. 027's only subscriber is `doc-lifecycle` and
   both documents amend 025's lifecycle, so the document spec carries the
   machinery its lifecycle rides on.
3. **033 folds into 026**, where its three-valued coverage resolution merges
   into the `--needs-planning` query that consumes it. Its amendments of
   025 §4 and §7 therefore reach a different target and keep their citations
   (ruling 1 of part 3).
4. **015 §7 is placed, not absorbed.** Part 1 recorded five "Amendments to
   existing specs" sections as `absorbed` on the grounds that folding the
   amendments in leaves nothing to state. That premise fails for 015 §7: it
   appends four SHACL node shapes (Artifact, Deployment, Environment, Commit)
   that are not amendment and are stated nowhere else, so dropping it would
   drop normative claims (rule 6). It merges into folded 006 §7, which holds
   the shape list it extends; its amendment table dissolves under rule 3.
5. **Folded 006 and 007 enact 014 §1's prefix rename.** Every `ls:` / `lsc:`
   / `lsid:` span in the placed sections is respelled `wl:` / `wlc:` /
   `wlid:`, in prose, tables and Turtle alike; `rdf/ls/` becomes `rdf/wl/`,
   `ls-shapes.ttl` becomes `wl-shapes.ttl`, and the published-IRI rows lose
   the `/ls/` segment (`…/ontology#`, `…/concept/`, `…/id/`) per 014 §1's
   "the published base carries no ontology-name segment". 006 is the document
   the rename was written about and `ns/` has shipped the `wl:` vocabulary
   since; leaving the retired spelling in it would be the worst outcome of
   the fold. All 70 span drops are pre-authorised in `fold.yaml`'s
   `allow_dropped_ids`, so `--check --ids` cannot catch a term the rewrite
   loses — **the per-document review carries that check instead** (see the
   task steps).
6. **"Candidate spec 020" is de-numbered.** 014 §0, §14 (twice) and §15 name
   the unwritten onboarding spec as "candidate spec 020". That number now
   belongs to `020-inbox-import.md`, an accepted, unrelated document, so
   `refmap.py` would repoint the phrase into a confidently wrong pointer.
   The folded text names it as an unwritten candidate spec of its own,
   without a number. This is the one place in part 4 where a reference is
   deleted rather than repointed, and it is a planning-tier ruling, not a
   rewriter's call.
7. **References to `000` are dropped, not repointed.**
   `000-umbrella-architecture.md` has never existed in this corpus, so
   025 §2's "The authority sentence in 000 §1 updates accordingly" and
   006 §0 / 015 §1's "binding conventions (umbrella)" point at a document
   nobody can open. The claim each carries survives as the folded document's
   own statement; the pointer goes. Part 1's folded 005 set this precedent
   for the same dangling number.

8. **006's `covers` mention is respelled `wl:cutFrom`, not `wl:covers`.**
   006 §4's v1 caveat names `ls:covers` (Artifact→Commit, the delivery
   frontier). 033 §4.1 renamed that edge `wl:cutFrom` — because `wl:covers`
   is now Plan→Section and two domains on one property intersect to nothing
   — and left 006's mention as written on the grounds that it predates the
   prefix rename and sits in an accepted document. Ruling 5 retires that
   reasoning: the folded corpus enacts the rename, so a literal respell would
   have folded 006 contradict its own §3.1, which declares `wl:cutFrom`.
   **Consequence for task 4:** 033 §4.1's sentence about leaving 006's
   spelling alone is spent — state the rename of the term, not the note about
   006's historical spelling.
9. **025 §8's Workstream→Project change applies document-wide in folded
   006**, not only in the three sections carrying its inline notes. 025 §8
   deletes `wl:Workstream`, `wl:OngoingMaintenance` and `wl:inWorkstream`,
   redefines `wl:Project` as the unbounded umbrella and makes `wl:inProject`
   functional; 006 states consequences of the old model in its SHACL sketch,
   Layer 2 table, projection section, open questions and acceptance criteria.
   Leaving those would make the document contradict its own §1–§3. This
   carries one normative change that is 025's and not the rewriter's: with
   `wl:inProject` functional, "a Task in several Workstreams appears in
   several graphs" is gone, and each Task's quads live in the named graph of
   its one Project.

## Cross-target obligations

An amendment whose *content* one task must absorb while its *anchors* live in
another task's document. All four are internal to part 4, so no other part
inherits anything:

- **014 amends 006** §1.1–§1.5, §3.2 and §11, and **009** doc-wide (its
  item-3 note). Task 1 absorbs all of it; every 014 anchor lives in folded
  025, so the citations stay in refmap-visible old-corpus form.
- **014 amends 007** §2.1, §3, §4, §4.4 and §6. Task 2 absorbs it, same
  citation treatment.
- **015 amends 007** §3.4. Task 2 absorbs it; 015's anchors live in folded
  006, so this citation also stays.
- **025 amends 006** §1.1, §1.2, §1.3 and §1.5, and **033 amends 006** §1.3.
  Task 1 absorbs both; 025's and 033's anchors live in folded 025 and folded
  026 respectively.

Within a target, an amendment whose amender folds into the same document
dissolves uncited: 025's, 027's, 028's and 034's amendments of 014 and of
each other (task 3), 033's of 026 §2.1 (task 4), and 015's of 006 (task 1).

## Residuals for part 5

Additions to the list parts 2 and 3 already record.

- **Every `D<n>` design-record label in the corpus now cites a retired
  document.** Part 3 left the scheme half-retired and gave part 5 the choice
  of sweeping or keeping it. Part 4 settles the fact that decides it: 003 is
  retired *by this part*, so `(D4)`, `(D11)`, `(D2/D3)` and the fifteen
  others in folded 006, 007, 004, 008 and 016 point at nothing a reader can
  open. Part 4 deliberately does not strip them — doing it in two of the five
  documents would widen the divergence part 3 named — so part 5 strips them
  corpus-wide or states why they stay.
- **Two more `requires:` lists need deduplication after `refmap.py` runs**,
  the same defect part 3 records for folded 008. Folded 026's computed
  `requires:` names both 014 and 025, which both repoint to folded 025;
  folded 025's names both 018 and 004, which both repoint to folded 004.
- **`--needs-planning` output in folded 026 §2.1 cites `007 §3.4` and
  `007 §5` as an example gap.** Both anchors move in this part (to §2.4 and
  §4), and the example is prose inside a fenced sample line, so `refmap.py`
  will not see it. Task 4 repoints it by hand; the residual is recorded so
  part 5's review does not read the hand-repoint as a stale reference.
- **009's implementation-status qualifiers stay as written** — "done in dev",
  "prod remains blocked on item 1", "the override is not yet implemented in
  rdf-registry". Rule 4 leaves implementation status alone, so folded 006 §13
  ships a status report that was verified in 2026-07. Refreshing it is a spec
  amendment, not a fold or a cutover edit.

## Global Constraints

Inherited from part 1, restated so a task reading only this plan misses none:

- `mapping.yaml` is generated from `fold.yaml` alone. Never hand-edit it, and
  never derive it by reading `docs/specs2/*.md`. It is already regenerated for
  part 4; no task runs `fold.py --mapping`.
- `fold.py --scaffold` refuses to overwrite an existing `docs/specs2/*.md`.
  Regenerating a document after the rewrite has started is a data-loss bug.
- Every anchor of the twelve sources appears exactly once across `from:` and
  `dropped:` — already satisfied, and now corpus-wide: `--check` must stay
  clean **without** `--partial` after every task. Do not pass `--partial`; it
  would silently scope the check to the documents already folded.
- Do not renumber by hand. `new:` numbers come from `fold.yaml`; `secfmt.py`
  owns the `{#sec-N}` anchors in the written files.
- Introduce no new Python dependency beyond PyYAML.
- Part 4 creates the four `docs/specs2/` documents and nothing else. It does
  not modify `docs/specs/`, `scripts/fold.py` or `scripts/refmap.py`, and it
  repoints no references outside `docs/specs2/` — the corpus-wide reference
  rewrite is part 5.
- `docs/plans/` gets its spec references repointed at cutover (part 5) and
  nothing else.

## Tasks

Each task is the part-1 plan's frozen per-document task template with the
document substituted, preceded by a context recording what `fold.yaml` decided
and the rulings the rewrite applies. Two template notes hold for all four: the
pre-commit hooks cover `docs/specs2/` for `secfmt.py`/`secmeta.py` but nothing
runs `secindex.py` for you, so run the steps and see them clean before
reporting; and `--check` reporting `undeclared` on a heading you added is
rewrite rule 10 working — delete the heading or escalate, never extend
`fold.yaml`. The tasks are independent.

### Task 1 — Fold `006-knowledge-graph.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/006-knowledge-graph.md`.

**Context:** 33 placed refs from four sources into 28 sections (17 top-level);
12 drops. 015's sections **interleave** with 006's own structure rather than
trailing it — runtime classes at §2.1 under Classes, runtime properties at
§3.1, runtime schemes at §6 beside the other two, the kind-first IRI grammar
at §10.1 under the IRI scheme, the runtime projection table at §11.1 under
Projection — so the rewrite must make each pair read as one subject, not as a
006 half followed by a 015 half. 009 folds whole: its preamble opens §13 (the
scope sentence — the data-platform hosts the knowledge half, the backbone
keeps the execution half) with its four sections as §13.1–§13.4 and its
acceptance criterion joining §16. 003 contributes nothing but its retirement:
preamble and all six anchors are `dropped:`, and it is a `sources:` entry so
every whole-document and `WL-SPEC-3` reference repoints here.

Five drops are 006's own retired anchors and each `dropped:` reason names what
survives where — §1 (the `ls:` prefix block; §10 states the post-rename
bindings), §1.6 (`Spec ⊃ Plan ⊃ Task`; the surviving projection claims are in
§3 and §11), §3.3 (Layer 3; replaced by 015 §2 and §6, both placed *here* as
§2.1 and §11.1, so §8's entity model cross-references them instead of carrying
a table), §7 (partial supersession; replaced by 014 §3, which folds into 025),
§11 (acceptance criteria; replaced by 014 §16, likewise). **006 therefore has
no acceptance criteria of its own**: §16 is 009 §4 plus 015 §10, and that is a
`fold.yaml` decision, not a loss to report.

Rulings the rewrite applies:

- **The respell (ruling 5) is the largest mechanical change in this
  document.** Every `ls:`/`lsc:`/`lsid:` occurrence becomes `wl:`/`wlc:`/
  `wlid:`; `rdf/ls/ontology.ttl`, `rdf/ls/ontology.1-2.ttl`,
  `rdf/ls/concept.ttl` and `rdf/shapes/ls-shapes.ttl` become their `rdf/wl/`
  and `wl-shapes.ttl` forms; §10's namespace table rows lose the `/ls/`
  segment, which is exactly what 006 §5's own amendment note instructs. The
  `@prefix` block itself is not reinstated — it lived in dropped §1.
- **Amendment notes to absorb (rule 3), with the citation rule of part-3
  ruling 1 applied per amender.** §1 carries four (014, 025 §8, 015, 016 §1);
  §2 four (014 §2, 016 §1, 025 §8 and §4); §3 four (025 §8, 033 §4, the long
  `implements` note from 014 §6 and 015, and 033 §4.2); §4 one (014 §5); §5
  two (014 §8, then 025 §6, which **wins** — the scheme is `feature, bug,
  chore, design, review, spike`, `epic` removed and `spec` renamed `design`);
  §8.2 two (014 §9, 015 §7); §9 two (015 §7, and the internal
  implementation-statement note); §10 two (015 §5, 014 §1); §11 one (014 §6
  on the `implements` projection row). **015's notes dissolve uncited** — 015
  is this same document. 014's, 016's, 025's and 033's citations **stay**,
  spelled as ordinary old-corpus references for `refmap.py`, never as bare
  numbers and never hand-repointed to new-corpus numbering.
- **§7 merges 006 §2 with 015 §7 (ruling 4).** Keep the three-tier reasoning
  table and the OWL-classifies/SHACL-enforces split; extend the node-shape
  sketch list with 015's four shapes, under the respelled `wl-shapes.ttl`
  filename. 015 §7's amendment table dissolves: every row but one names a
  section of this document, and the 007 row is task 2's to absorb.
- **§0 merges two scope sections.** State one scope for the folded document —
  vocabulary, entity model across three layers, runtime layer, IRI scheme,
  projection, and what the data-platform must host. 015 §0's "lands with 014
  or after it, never before" is a document-status qualifier and resolves
  (rule 4's first axis); 015 §0's account of *why* the runtime layer was
  missing survives as motivation. Drop the umbrella pointer per ruling 7 while
  keeping the binding conventions themselves (standards-first, mint sparingly,
  no gtio).
- **Rule 4's second axis binds §13.** 009's "done in dev", "open", "prod
  remains blocked on item 1" are implementation status and stay verbatim,
  including the runbook filename.
- **The bare-number grep will be busy here.** Hits naming 009 and 015 are now
  *internal* references and repoint to sections of this document via
  `mapping.yaml`; hits naming 011 or 018 repoint into folded 004; hits naming
  004, 005, 007, 008, 013 and 016 keep their numbers. `(D…)` labels are **not**
  in scope for this task — see **Residuals for part 5**.

- [ ] `./scripts/fold.py --scaffold --only 006-knowledge-graph.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/006-knowledge-graph.md` — repoint each hit by hand against
      `mapping.yaml` (rewrite rule 5's manual half). `refmap.py` cannot see a
      bare number, so these are the references nothing else will catch.
- [ ] `grep -n 'ls:\|lsc:\|lsid:\|rdf/ls/\|ls-shapes' docs/specs2/006-knowledge-graph.md`
      — **must be empty.** `allow_dropped_ids` authorises every one of these
      span drops, so `--check --ids` cannot catch a half-finished respell;
      this grep is what does.
- [ ] `./scripts/fold.py --check --ids` — clean. Do **not** pass `--partial`:
      part 4 completes the corpus-side placement, and `--partial` would scope
      the check to the documents already folded.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`, so the folded corpus ships with the map every consumer
      reads and `--check` stays meaningful.

### Task 2 — Fold `007-drift-and-overview.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/007-drift-and-overview.md`.

**Context:** One source, 18 live anchors placed 1:1 into 18 sections, two
drops. It sits in part 4 rather than part 2 because its two-layer model,
deriver contract and standing queries only read correctly against the
vocabulary 006 states and the coverage claim 014 §6 adds — both of which move
in this part. Numbering shifts by one throughout: Purpose & scope becomes §0
as everywhere else in the new corpus, so the derivers become §2.1–§2.4 and the
standing queries §3.1–§3.4, and the parallel `3.N`/`4.N` split the source's
own preamble records having introduced disappears. Do not carry that history
forward — the preamble is dropped.

Both drops carry their reasons in `fold.yaml`: the preamble (three notes, all
absorbed — the dependency note is restated by §0 and §6, the prefix-rename
note is enacted by the respell, the numbering note is spent under this fold's
own numbering) and §4.3 (superseded by 014 §6's stale-claim query, which folds
into 025).

Rulings the rewrite applies:

- **The respell (ruling 5)** covers prose, the CLI table and the SPARQL
  sketches: `ls:AcceptedDeviation`, `ls:status`, `ls:layer`, `ls:mirrors`,
  `ls:sanctionedBy`, `<task> ls:affects <component>` and the bare `ls` token
  in the fenced queries. Eight entries in `allow_dropped_ids` authorise the
  drops; the grep step below is what proves the `wl:` counterparts landed.
- **Five amendment notes to absorb (rule 3), every citation kept** — 014 and
  015 both fold into other targets. §1.1 ← 014 §6 (the table gains
  `…/graph/observed/repo-implements`); §2 ← 014 §6 (the fifth deriver, under
  the same contract); §2.4 ← 015 (output vocabulary, IRI grammar and per-node
  v1/v2 status are 015 §2–§6, now folded 006 §2.1/§3.1/§6/§10.1/§11.1);
  §3 ← 014 §6 (two standing queries arrive; the stale-claim query replaces
  4.3 rather than joining it); §5 ← 014 §10 (`lode drift --docs`, the
  `lode doc …` family, per-section coverage badges).
- **§3.3 needs care.** Its amendment re-points the Task→DesignDoc join at the
  Component→Section edge and ends the status enum at `superseded`. State the
  amended rule and keep the SPARQL sketch, updating the sketch's join and the
  parenthetical status-scheme line to match what the section now says — a
  sketch contradicting its own prose is the fragmentation this migration
  removes. If the re-pointed join cannot be written without inventing a
  predicate, stop and report it rather than guessing.
- **§3.1's `xsd:date(NOW())` clause, the deviation-suppression arithmetic and
  §3.4's authority caveat are normative and survive intact** (rule 6). The
  `(D5)`, `(D12)`, `(D14)` and `(006)` marks in body text stay — see
  **Residuals for part 5**.

- [ ] `./scripts/fold.py --scaffold --only 007-drift-and-overview.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/007-drift-and-overview.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half).
- [ ] `grep -n 'ls:\|lsc:\|lsid:' docs/specs2/007-drift-and-overview.md` —
      **must be empty**, for the reason task 1's equivalent step gives.
- [ ] `./scripts/fold.py --check --ids` — clean, without `--partial`.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`.

### Task 3 — Fold `025-documents-in-the-backbone.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/025-documents-in-the-backbone.md`.

**Context:** The largest fold in the corpus — 89 placed refs from five sources
into 70 sections (25 top-level), four drops, a ~2,150-line scaffold. The
document's spine is the one 025 and 014 share: what a design document is
(§2–§4), where it lives (§5), what constrains it (§6), how it moves through
review and amendment (§7–§8), what a plan is and what accepting one does
(§9–§10), how implementation coverage is claimed (§11), how documents are
addressed and referenced (§12–§14), the event log the lifecycle rides on
(§15), the git on-ramp that populates the store today (§16), and the schema,
surfaces and scaffolding trio (§17–§24).

Renumbering is total and every source amends at least one other, so **rule 3
does more work here than anywhere else in the migration**. Absorptions whose
amender folds into this same document dissolve uncited: 025 §2 ← 034 §7,
025 §3 ← 027 §5, 014 §0/§4/§10 ← 025 §2, 014 §2 ← 025 §4, 014 §5 ← 025 §3.
Absorptions whose amender folds elsewhere keep a refmap-visible citation:
014 §6 ← 033 §4.2, 025 §4 ← 033 §1, 025 §7 ← 033 §2 — all three now name
folded 026.

Four drops, each with its reason in `fold.yaml`: 014 §8 (superseded by
025 §6, placed at §10), and the three "Amendments to existing specs" sections
(014 §12, 025 §11, 027 §11) whose rows are recorded in their documents'
`amends:` frontmatter and absorbed by the amended documents' own rewrites.

Rulings the rewrite applies:

- **§9 merges the demotion with its reversal.** 014 §2.1 ("plans are
  demoted", `wl:Plan` dropped) and 025 §4 ("plans are documents", `wl:Plan`
  returns as a sibling of `wl:DesignDoc`) become one statement of the end
  state: a plan is a document, mutable, anchor-free, accept-gated, and the
  argument 014 made — the section lock must never bind a plan — is what
  *keeps* it a sibling rather than a subclass. Do not write it as a history
  of two positions.
- **§7 merges 025 §3 with 014 §5.** The scheme is `draft → accepted →
  superseded`; `proposed` and `implemented` are both gone, a document under
  review is a draft with an open review task, and 014 §5's revision flow
  keeps its shape with the candidate carrying `draft`. 027's amendment —
  minting a task that *asks for* review or planning is not the act it asks
  for — is absorbed here as ordinary text, uncited.
- **The lifecycle extensions of 028 are stated, not harmonised.** §8.6 adds
  `stale` on plans, §7.3 adds `patched` on a section and `in_review` as a
  reviewer-set fact, §8.7 adds `withdrawn` to the status scheme. These extend
  what §7 states; 028 is a draft that says so. State each where its source
  states it and cross-reference. **Do not** rewrite §7's scheme to pre-merge
  them, and do not invent a reconciliation — that would be a design change
  (rule 6), and if the two readings look irreconcilable, stop and report it.
- **§17 carries 014 §1's rename as settled vocabulary identity**, not as a
  migration instruction: the prefixes, the three namespaces, the published
  base without an ontology-name segment, and the `rdf/ls/` → `rdf/wl/` source
  move. "It must happen before spec 006 ships" is a document-status qualifier
  and resolves (rule 4). The section is otherwise 025 §9's `ns/`-is-the-source
  rule plus 034 §8's `wl:Plan` gap-closing, which the same section now
  answers.
- **Ruling 6 lands in §22 and §23** (and once in §2's scope prose): the
  onboarding spec is named without the number 020.
- **Ruling 7 lands in §5**: state the authority split — the backbone owns
  execution facts and design-document artifacts, the graph owns the derived
  queryable view — as this document's own claim, without the `000 §1` pointer.
- **§18 merges four surface sections** (025 §10, 014 §10, 027 §6, 034 §9) into
  one command table. Every verb survives; a verb listed twice is listed once.
  034 §9's `--body-file` is a real surface addition and stays.
- **§24 consolidates four acceptance-criteria sections by claim, not by
  concatenation** (rule 11) — roughly forty items where a straight
  concatenation would be unreadable. Merge criteria stating the same
  requirement; keep every distinct one. §22 and §23 get the same treatment.
- **Rule 4's second axis binds §5 and §16.** "until this spec is implemented
  and the corpus is imported", "None of it is built", "Nothing emits any of
  this yet" are implementation status and stay.
- **The bare-number grep is heavy here.** Hits naming 014, 027, 028 and 034
  are now *internal*; hits naming 018, 010 or 011 repoint into folded 004;
  hits naming 004, 005, 006, 012, 013, 016, 019, 022, 026 and 029 keep their
  numbers; `000` and `020` are rulings 7 and 6, not repoints.

- [ ] `./scripts/fold.py --scaffold --only 025-documents-in-the-backbone.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/025-documents-in-the-backbone.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half).
- [ ] `./scripts/fold.py --check --ids` — clean, without `--partial`. This
      document has no `allow_dropped_ids` entries, so `--ids` is a real gate
      here: every backticked span and fenced-block token of five sources must
      survive. Rule 9 applies throughout — never break a span across a line,
      and reflow a source-side wrapped span onto one line rather than treating
      it as a loss.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`.

### Task 4 — Fold `026-design-doc-queries.md`

```yaml
kind: chore
priority: medium
blockedBy: [ ]
```

**Files:** Create `docs/specs2/026-design-doc-queries.md`.

**Context:** 30 placed refs from two sources into 27 sections (13 top-level),
no drops — every anchor of both sources is live and placed. The fold's point
is one merge: **§2.1 states the coverage resolution where the query is
defined.** 026 §2.1 was already rewritten once to cite 033, so the two texts
overlap heavily; keep 033 §2's four-outcome table as the rule (fully planned /
partially planned / bound only / unplanned, with `fullCoverageWith` checked
rather than trusted) and keep 026 §2.1's query-surface specifics that 033 does
not state — the whole-document-claim exclusion, the sample output line, the
`NO-SPEC` case, the `--status` and `--needs-execution` conflicts, the
acceptance gate on both ends, and the argument from `.worklode/implements.yaml`
being section-scoped. De-duplicate restatements; drop nothing that only one
source says.

The other structural change is 033's material finding its place: §5 (a plan
covers sections; it implements none) and §5.1 (frontmatter shape) sit beside
026 §5 (plans carry `status` and `task`) as §5.2, and 033 §4's ontology
becomes §6–§6.3 with its checks at §7–§7.1. 026 §4.2a becomes **§4.3**: the
letter suffix records an insert into a published document, and this fold
assigns fresh numbers to a document nobody has pinned a claim against yet.
`NO-SPEC` itself is unaffected — it is a sentinel value, not an anchor.

Rulings the rewrite applies:

- **033's amendment of 026 §2.1 dissolves uncited** (same document). Its
  amendments of 025 §4 and §7 are not this task's to state; folded 025 carries
  them and cites this document.
- **Citations into 014 stay, and become citations of folded 025.** 026 and 033
  cite `014 §11.3` (the shorthand), `014 §6` (the implements manifest),
  `014 §3`/`§4` (anchors, versioning), `014 §11` (frontmatter keys) and
  `014 §5` (revision). Leave every one in old-corpus form for `refmap.py`;
  never hand-repoint to new-corpus numbering (part-3 ruling 5).
- **§7.1 is largely spent but is not dropped.** 033 §5.1's table lists the
  documents the `covers` rename reaches; the rename has since landed in
  `CLAUDE.md`, `docs/authoring-design-docs.md` and `ns/`. Rule 4's second axis
  keeps implementation status, so state the table as the source does. Its one
  document-status qualifier — "014 §11's key table row is corrected in place
  (014 is draft)" — resolves.
- **§1's metrics exception is normative and survives verbatim in substance**
  (rule 6): this spec adds no server-side surface, so it carries no
  `worklode_*` metric, and the exception is recorded so a later reviewer does
  not read the absence as an oversight.
- **The example gap line in §2.1 names `007 §3.4` and `007 §5`.** Both anchors
  move in this part (§2.4 and §4 of folded 007) and the line sits inside a
  fenced sample that `refmap.py` will not touch, so repoint it by hand against
  `mapping.yaml`. It is recorded under **Residuals for part 5** so the review
  reads it as deliberate.
- **Bare-number grep:** hits naming 033 are now internal; 014 and 025 repoint
  into folded 025; 019, 022, 029 and 032 keep their numbers.

- [ ] `./scripts/fold.py --scaffold --only 026-design-doc-queries.md`
- [ ] Rewrite the scaffold following **Rewrite rules** 1–11 of the part-1 plan
      (`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`).
      Do not edit `fold.yaml`; if a rule cannot be followed without a judgment
      call, stop and report it as a `fold.yaml` defect.
- [ ] `grep -nE '\(0[0-9]{2}\)|\*\*0[0-9]{2}\*\*|(per|spec|see) 0[0-9]{2}'
      docs/specs2/026-design-doc-queries.md` — repoint each hit by hand
      against `mapping.yaml` (rewrite rule 5's manual half).
- [ ] `./scripts/fold.py --check --ids` — clean, without `--partial`. No
      `allow_dropped_ids` entries here either, so `--ids` gates every span of
      both sources.
- [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
      docs/specs2` — clean.
- [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
      `index.yaml`.

## What part 5 inherits

With these four documents written, `docs/specs2/` holds all eighteen and the
migration's judgment is spent. Part 5 is mechanical: run `refmap.py` over the
repo's inbound references, resolve the reference decisions parts 2–4 recorded
as residuals, `git rm -r docs/specs`, move `docs/specs2` into its place, set
each document's `status`/`issued` per part 1's accepted-if-every-source-was
rule, delete `fold.py` and `refmap.py`, and keep `mapping.yaml` as the durable
record.
