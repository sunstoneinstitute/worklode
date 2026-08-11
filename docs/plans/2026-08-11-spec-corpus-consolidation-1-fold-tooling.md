---
status: draft
covers: NO-SPEC
---
# Spec corpus consolidation — part 1: fold tooling

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Build the tooling that folds `docs/specs/` (33 documents, 458 live
sections, dense amendment chains) into a defragmented `docs/specs2/` of 18
documents, and that emits the old-anchor → new-anchor map every other consumer
of the corpus needs. Part 1 delivers the tools and validates them end-to-end on
one cheap document; parts 2–5 do the folding.

**Architecture:** One hand-written placement file, `docs/specs2/fold.yaml`,
is the only place a folding decision is recorded. `scripts/fold.py` reads it and
derives everything else: the skeleton documents, `docs/specs2/mapping.yaml`, and
the checks. Crucially `mapping.yaml` derives from `fold.yaml` **alone**, never
from the written prose — so the consolidating rewrite in parts 2–4 can be as
heavy as it needs to be without the mapping drifting, and `--check` catches a
rewrite that drops or invents an anchor. `scripts/refmap.py` consumes
`mapping.yaml` at cutover to rewrite the repo's ~1,700 inbound references.

**Why this is cheap to execute.** All the judgment lives in `fold.yaml`, written
at the planning tier. Once it exists, each document's rewrite is a
fully-specified task — the scaffold already holds the right source text in the
right order under fixed headings and numbers, the rules below say what to change,
and `fold.py --check` is an objective pass/fail gate. Per `MODEL_SELECTION.md`
that is the Sonnet row: ~650 lines per document, no design decisions, a
mechanical diff. `fold.yaml` authoring and the whole-branch review stay at the
top tier.

**Tech Stack:** Python 3 (stdlib + PyYAML, already required by `secmeta.py` and
`currentspec.py`), `unittest` following `scripts/secmeta_test.py`, and the
existing `secfmt.py` / `secmeta.py` / `secindex.py` / `currentspec.py` as the
corpus oracles rather than re-parsing markdown.

## Decisions this plan implements

Settled during design; recorded here because no spec governs a corpus migration.

- **18 documents, each keeping the number of its dominant source**, so no
  `WL-SPEC-<N>` ever changes meaning. Survivors: 001, 004, 005, 006, 007, 008,
  012, 013, 016, 017, 019, 020, 021, 022, 025, 026, 029, 032. Retired and never
  reused: 002, 003, 009, 010, 011, 014, 015, 018, 023, 024, 027, 028, 030, 031,
  033, 034.
- **Drafts count as current.** The fold takes the `--with-drafts` view, so a
  draft's amendments are folded in as in force.
- **Everything in `docs/specs2/` starts `status: draft`.** At cutover a document
  becomes `accepted` (with `issued:` = cutover date) only if every source it
  folds was accepted; otherwise it stays draft.
- **Scaffolding sections are kept, consolidated per document** — one
  `Dependencies` / `Open questions` / `Acceptance criteria` trio folded from the
  sources. The five `Amendments to existing specs` sections (014 §12, 015 §7,
  025 §11, 027 §11, 029 §9) are the exception: once their amendments are folded
  in there is nothing left to state, so they are recorded as `absorbed`.
- **`docs/specs2/` replaces `docs/specs/` at cutover** (part 5), in the same
  commit as the reference rewrite. `.worktrees/` and `.claude/worktrees/` are
  excluded from that rewrite.
- **`fold.py` and `refmap.py` are one-shot migration code** — they get tests,
  not permanent-tooling polish, and they are deleted in the cutover commit.
  `mapping.yaml` is the durable artifact.

## The fold.yaml schema

```yaml
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - to: 005-prioritization-and-pickup.md
    title: Prioritization and pickup
    sources: [005-prioritization-and-pickup.md]
    sections:
      - {new: "0", heading: "Purpose & scope", from: ["005-prioritization-and-pickup.md#sec-0"]}
      - {new: "1", heading: "`concern` and `priority`", from: ["005-prioritization-and-pickup.md#sec-1"]}
      - {new: "2", heading: "Ranking", from: ["005-prioritization-and-pickup.md#sec-3",
                                              "005-prioritization-and-pickup.md#sec-3.1"]}
    dropped:
      - {ref: "005-prioritization-and-pickup.md#sec-7", reason: "absorbed: dependency list is frontmatter"}
```

`from` is a list so N old sections may merge into one new section; the mapping
then points all N at the same new anchor. `dropped` carries a reason string
whose first token is a category — `absorbed`, `spent`, `superseded`, `dead`.

## Derived mapping.yaml

```yaml
version: 1
corpus: {from: docs/specs, to: docs/specs2}
documents:
  - {from: 002-github-app-auth.md, to: 001-identity-and-authentication.md,
     from_id: WL-SPEC-2, to_id: WL-SPEC-1}
sections:
  "002-github-app-auth.md#sec-3.5": "001-identity-and-authentication.md#sec-6.2"
dropped:
  - {ref: "002-github-app-auth.md#sec-2", reason: "spent: current-state narrative, retired by 023"}
```

Document-level refs and the `WL-SPEC-<n>#sec-<a>` shorthand are derived by the
consumer from `documents` + `sections`; the file carries no second table.

## Rewrite rules

The contract every per-document task in parts 2–4 follows verbatim. It is
deliberately mechanical: the aim is a document that reads as one statement, not
a better spec. Nothing here licenses a design change.

1. **Delete the provenance markers** the scaffold emitted.
2. **Merged sections state shared material once.** Where `from:` listed several
   sources for one section, keep every distinct claim and de-duplicate only
   restatements of the same claim.
3. **Absorb amendment notes.** A `> **Amended by spec NNN.** …` block is replaced
   by the amended text stating the post-amendment rule directly. The note goes
   away; what it said does not.
4. **Resolve dead qualifiers.** "until 025 lands", "in v1", "today", "currently"
   and similar are rewritten to the state the corpus now describes, since drafts
   count as current here. If resolving one requires a judgment call, stop and
   escalate rather than guess — that is a `fold.yaml` defect.
5. **Repoint cross-references** using `mapping.yaml`. A reference to material
   listed under `dropped:` is an escalation, not a deletion.
6. **Never add a normative claim, never drop one.** No new MUST/never/only
   sentences, no removed ones. Trimming a redundant restatement is allowed under
   rule 2; trimming the last statement of a rule is not.
7. **Preserve every backticked identifier** — schema columns, CLI flags,
   ontology terms, file paths, env vars. `fold.py --check --ids` enforces this
   mechanically.
8. **Leave numbering alone.** Headings and numbers come from `fold.yaml`;
   `secfmt.py` owns the `{#sec-N}` anchors.
9. **Never break a backtick span across a line.** Reflowing a paragraph so that
   `` `lode task brief` `` wraps mid-span makes `--check --ids` report it as
   dropped. Keep the span whole and wrap around it.
10. **Do not add structure.** The drift check reports any heading `fold.yaml`
    does not declare — including a sub-heading carried over from a source spec,
    and an unnumbered `## Appendix`. New structure is a `fold.yaml` change,
    which is a planning-tier decision, not a rewrite.

## Global Constraints

- `mapping.yaml` is generated from `fold.yaml` alone. Never hand-edit it, and
  never derive it by reading `docs/specs2/*.md`.
- `fold.py --scaffold` refuses to overwrite an existing `docs/specs2/*.md`.
  Regenerating a document after the rewrite has started is a data-loss bug.
- Every anchor in the `--with-drafts` current view must appear exactly once
  across all `from:` and `dropped:` entries. No anchor twice, none missing.
- Do not renumber by hand. Author `new:` numbers in `fold.yaml`, let
  `secfmt.py` own the `{#sec-N}` anchors in the written files.
- Introduce no new Python dependency beyond PyYAML.
- Part 1 touches no document other than the one validation fold (005). It does
  not start the consolidating rewrite and does not modify `docs/specs/`.
- **`docs/plans/` gets its spec references repointed and nothing else.**
  `refmap.py` rewrites the reference strings — including `covers:` frontmatter,
  so coverage queries keep resolving — but no plan is restructured, no `covers:`
  semantics are re-derived, and the 49 pre-existing `secmeta.py` findings on
  plans are out of scope. A plan whose covered section was dropped surfaces
  through task 5's unmapped-reference report.

## File Structure

- Create: `scripts/fold.py` — `--mapping`, `--scaffold`, `--check`.
- Create: `scripts/fold_test.py` — hermetic corpus fixtures, per `secmeta_test.py`.
- Create: `scripts/refmap.py` — `--dry-run` / `-w` reference rewriter.
- Create: `scripts/refmap_test.py`.
- Create: `docs/specs2/fold.yaml` — validation slice only in part 1 (005).
- Create: `docs/specs2/mapping.yaml` — generated.
- Create: `docs/specs2/005-prioritization-and-pickup.md` — the validation fold.

## Tasks

### Task 1 — Parse fold.yaml and emit mapping.yaml

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:** Create `scripts/fold.py` and `scripts/fold_test.py`.

**Interfaces:** `load_fold(path) -> Fold`, `derive_mapping(fold) -> dict`, and a
`--mapping` CLI mode writing `docs/specs2/mapping.yaml`. `Fold` exposes
`documents`, and per document `to`, `title`, `sources`, `sections`, `dropped`.

- [ ] **Step 1: Write failing mapping tests.** Build a hermetic two-document
      fixture corpus in a temp dir. Assert a 1:1 section maps to its new anchor;
      that a `from:` list of three maps all three onto the same new anchor; that
      `dropped` entries land in `dropped:` and never in `sections:`; that
      `from_id`/`to_id` render as `WL-SPEC-<n>` from the filename ordinal; and
      that emitting twice is byte-identical.
- [ ] **Step 2: Implement the parser and deriver** until the tests pass.
- [ ] **Step 3: Reject malformed folds** — a `new:` number whose parent is not
      declared, a duplicate `new:` within a document, a `from:` ref with no
      `#sec-` fragment, and an unknown top-level key. Each is an error with the
      offending ref in the message.

### Task 2 — Corpus completeness check

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** Modify `scripts/fold.py` and `scripts/fold_test.py`.

**Interfaces:** `--check` mode. Reads the live corpus via `currentspec.py`'s
`--with-drafts` view (import it, do not shell out) and compares against
`fold.yaml`.

- [ ] **Step 1: Write failing coverage tests.** Fixtures where one live anchor
      is unplaced, where one is placed twice, and where a `from:` names an
      anchor that does not exist in `docs/specs/`. Each must exit non-zero and
      name the anchor.
- [ ] **Step 2: Implement.** Report as three grouped lists — unplaced, placed
      twice, dangling — never a single opaque count. Exit 0 only when all three
      are empty.
- [ ] **Step 3: Partial-fold mode.** `--check --partial` restricts the
      completeness requirement to the documents `fold.yaml` currently declares,
      so parts 2–4 can run the check while the fold is incomplete.

### Task 3 — Scaffold assembly

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** Modify `scripts/fold.py` and `scripts/fold_test.py`.

**Interfaces:** `--scaffold [--only <file>]` writes `docs/specs2/<to>` for each
declared document.

- [ ] **Step 1: Write failing scaffold tests.** Assert frontmatter is emitted
      with `status: draft` and a `requires:` that is the union of the sources'
      `requires` minus references that resolve inside this fold; that headings
      come from `fold.yaml`'s `heading`, numbered per `new:`; that each section
      body is the source section's text verbatim, in `from:` order, separated by
      an HTML-comment provenance marker naming the source anchor; and that a
      second `--scaffold` over an existing file exits non-zero without writing.
- [ ] **Step 2: Implement** section slicing against `secindex.py`'s anchor
      table rather than re-parsing markdown.
- [ ] **Step 3: Verify the output passes the house checks** — run `secfmt.py -d`
      and `secmeta.py` over the scaffolded fixture and assert both are clean.

### Task 4 — Anchor-drift check against written prose

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

**Files:** Modify `scripts/fold.py` and `scripts/fold_test.py`.

**Interfaces:** extend `--check` to compare `fold.yaml`'s declared `new:`
anchors against the anchors actually present in the written `docs/specs2/*.md`.

This is the guard that makes the consolidating rewrite safe to do incrementally:
it fails when a rewrite drops a section the mapping still promises, or adds one
the mapping does not know about.

- [ ] **Step 1: Write failing drift tests.** A written file missing a declared
      anchor; a written file carrying an anchor `fold.yaml` never declared.
- [ ] **Step 2: Implement**, reporting the two directions separately. Skip
      documents `fold.yaml` declares but that have not been written yet.

### Task 5 — Reference rewriter

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** Create `scripts/refmap.py` and `scripts/refmap_test.py`.

**Interfaces:** `refmap.py [--dry-run|-w] [--root .]`, consuming
`docs/specs2/mapping.yaml`.

- [ ] **Step 1: Write failing rewrite tests** over a temp tree covering all
      three reference spellings — repo-relative path with and without
      `#sec-N`, bare filename within the corpus, and the `WL-SPEC-<n>` /
      `WL-SPEC-<n>#sec-<a>` shorthand — plus the prose form `spec 014 §7.2`
      found in `ns/shapes.ttl` and Go comments.
- [ ] **Step 2: Implement.** Exclude `.worktrees/` and `.claude/worktrees/`
      unconditionally. Include `docs/plans/`, `ns/`, Go sources and comments,
      and the repo's markdown. Default to `--dry-run`; `-w` writes.
- [ ] **Step 3: Report unmapped references** that point into `docs/specs/` but
      have no `sections:` entry — these are refs to dropped material and need a
      human decision, so list them and exit non-zero rather than silently
      leaving them.

### Task 6 — Identifier-preservation guard

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

**Files:** Modify `scripts/fold.py` and `scripts/fold_test.py`.

**Interfaces:** `--check --ids`. For each document, collect every backticked
span in the source sections `fold.yaml` places there, and assert each still
appears somewhere in the written `docs/specs2/` file.

This is what makes a Sonnet rewrite safe to accept. The realistic failure mode
in a spec consolidation is not bad prose, it is a summarised-away schema column,
CLI flag or ontology term — and that failure is mechanically detectable.

- [ ] **Step 1: Write failing identifier tests.** A rewrite that drops
      `` `task_edges` `` must fail and name the identifier and its source
      anchor. A rewrite that merely reorders or rewords around it must pass.
- [ ] **Step 2: Implement.** Compare on exact backtick-span text; do not
      normalise case or punctuation.
- [ ] **Step 3: Add an escape hatch.** A per-document
      `allow_dropped_ids: ["`old_column`"]` key in `fold.yaml`, each entry
      requiring a `# reason:` comment. Assert an unlisted drop still fails.

### Task 7 — Validate end-to-end on spec 005

```yaml
kind: chore
priority: high
skills:
  - superpowers:verification-before-completion
blockedBy: [3, 4, 6]
```

**Files:** Create `docs/specs2/fold.yaml`, `docs/specs2/mapping.yaml`,
`docs/specs2/005-prioritization-and-pickup.md`.

Spec 005 is the cheapest real fold — 11 live sections, one source, one
whole-document amendment from 018 to absorb — so it proves the format before
parts 2–5 commit to it.

- [ ] **Step 1: Write the 005 entry in `fold.yaml`**, placing all 11 live
      anchors and recording the `Dependencies`/`Open questions` handling.
- [ ] **Step 2: `fold.py --check --partial`** and confirm clean.
- [ ] **Step 3: `fold.py --scaffold --only 005-prioritization-and-pickup.md`,
      then `--mapping`.**
- [ ] **Step 4: Do the consolidating rewrite of 005 by hand**, absorbing 018's
      amendment into the ranking and decomposition sections.
- [ ] **Step 5: Verify.** `fold.py --check --ids`, `secfmt.py -l`, `secmeta.py`,
      `secindex.py --check`, and `refmap.py --dry-run` reporting a plausible
      count. Record in the plan review whether the format survived contact.
- [ ] **Step 6: Freeze the per-document task template** below against what 005
      actually needed, so parts 2–4 are uniform.

## The per-document task template

Every task in parts 2–4 is this, with the document substituted. It is the same
task 18 times, which is the point. **Frozen against what folding 005 actually
took** (task 7) — every path below is explicit because the checkers default to
`docs/specs`/`docs/plans` and otherwise report clean while checking the corpus
this migration is replacing.

> **Task N — Fold `<NNN-slug>.md`**
> `kind: chore`, `priority: medium`, `blockedBy: [ ]`
>
> **Files:** Create `docs/specs2/<NNN-slug>.md`.
>
> - [ ] `./scripts/fold.py --scaffold --only <NNN-slug>.md`
> - [ ] Rewrite the scaffold following **Rewrite rules** 1–10 above. Do not edit
>       `fold.yaml`; if a rule cannot be followed without a judgment call, stop
>       and report it as a `fold.yaml` defect.
> - [ ] `./scripts/fold.py --check --partial --ids` — clean. **`--partial` is
>       required** until the last document is folded; without it the check
>       reports every anchor of the unfolded corpus as unplaced.
> - [ ] `./scripts/secfmt.py -l docs/specs2` and `./scripts/secmeta.py
>       docs/specs2` — clean.
> - [ ] `./scripts/secindex.py docs/specs2` and commit the regenerated
>       `index.yaml`, so the folded corpus ships with the map every consumer
>       reads and `--check` stays meaningful.

`fold.py --mapping` is not part of this task — `mapping.yaml` derives from
`fold.yaml` alone, so it is regenerated once per part, after that part's
planning-tier `fold.yaml` authoring and before its first rewrite.

**Those steps are the only gate.** The `secfmt.py` pre-commit hook matches
`^docs/specs/.*\.md$` and the `secmeta.py` hook `^docs/(specs|plans)/.*\.md$`,
so neither fires on a commit that touches only `docs/specs2/` — and docs-only
PRs skip CI. Until cutover renames the directory, nothing automatic checks the
folded corpus. Run the steps.

Model per `MODEL_SELECTION.md`: implementation `sonnet` (fully specified,
mechanical diff, objective gate); per-document review `sonnet` for the near-1:1
folds in part 2, `opus` for the multi-source folds in parts 3–4 and for the
whole-branch review before cutover.

## What folding 005 established

The validation fold (task 7) came out clean on every check with zero findings.
Three things it settled, and two the schema cannot yet express.

**Settled.** The scaffolding trio is kept per document, not dropped — the
schema block above still shows `005#sec-7` under `dropped:` as a `dropped:`
*syntax* illustration; the decision in "Decisions this plan implements" governs.
A `Dependencies` section earns its place by saying what each neighbouring spec
supplies, which frontmatter `requires:` cannot; it opens by naming the hard
edge `requires:` already carries. And a section with exactly one subsection
folds into its parent (005's §3 + §3.1), which is the fragmentation this
migration exists to remove.

**The two gaps, both in parts 3–4 territory.** First, `fold.yaml` records
*placement* but not *authority*. Where one `new:` section merges sources
written years apart, the later source often supersedes rather than supplements
the earlier — and a whole-document supersession leaves no inline note in the
sliced body, so rewrite rules 2 and 3 both miss it. Rule 2 read literally
("keep every distinct claim") makes the rewriter state the retired rule
*and* the current one. `sections:` needs a way to name which `from:` ref is
authoritative, or at minimum a free-text `note:` the scaffold passes through to
the rewriter. `SECTION_KEYS` is closed, so this is a `scripts/fold.py` change
and therefore a planning-tier decision to take **before part 3 starts**, not
inside a rewrite task.

Second, `from:` is many-old → one-new only, and the "exactly once" constraint
forbids the reverse. A source section whose substance legitimately belongs in
two new sections has no expression: placing it under its dominant new section
makes `mapping.yaml` point half its inbound references at the wrong place.
Expect this in 006, 025 and 026. Splitting one such section costs a
`sections:` schema change plus a carve-out in `run_check`'s placed-twice group.

Two conventions to keep, neither enforced by code: **list the dominant source
first in `sources:`** (it drives `requires:` union order and `mapping.yaml`'s
row order), and give every `allow_dropped_ids` entry a reason that says whether
the drop is a source-side artifact or a real, accepted loss — the key is the
raw identifier, so one entry silently covers every source contributing that
same string.

## Series

| Part | Scope | Tier |
|---|---|---|
| 1 (this) | `fold.py`, `refmap.py`, `fold.yaml` format, validated on 005 | sonnet impl, opus review |
| 2 | Near-1:1 folds: 013, 016, 017, 019, 020, 021, 022, 029, 032 | sonnet |
| 3 | Multi-source folds: 001, 004, 008, 012 | sonnet impl, opus review |
| 4 | The dense clusters: 006, 007, 025, 026 | sonnet impl, opus review |
| 5 | Cutover (see order below) | opus |

**Part 5 runs in this order, in one commit.** The obvious sequence —
`refmap.py -w` then `git mv` — is wrong: it rewrites ~2,000 references to
`docs/specs2/…` and then renames that directory out from under them.

1. `refmap.py -w --corpus-to docs/specs` — rewrite references to their
   *post-move* paths, while `docs/specs2/` is still where the fold lives.
2. `git rm -r docs/specs && git mv docs/specs2 docs/specs`.
3. Flip `status`/`issued` on the documents whose sources were all accepted.
4. `spec_corpus = "docs/specs"` in `.worklode/config.toml`; `lode doc sync --dry-run`.
5. `git rm docs/specs/.refmap-applied` — `refmap.py -w` drops this marker in
   `docs/specs2/`, and step 2 carries it into the shipped corpus.
6. Delete `scripts/fold.py`, `scripts/refmap.py` and their tests; keep
   `mapping.yaml`.

**If the mapping is amended mid-cutover, revert the rewrite before re-running.**
The marker records `corpus_to` and counts but no identity of `mapping.yaml`, so
it cannot tell "the mapping changed" from "this is a double-rewrite". `--force`
re-applies the *full* rewrite over already-rewritten text — straight into the
id collisions the marker exists to prevent. `git checkout` the previous run
first, then re-run.

Reversing 1 and 2 does not work: after the move `corpus.from` collides with the
new corpus, `refmap.py`'s `MAPPING_PATH` is hardcoded to
`docs/specs2/mapping.yaml`, and the bare-filename gate would read new-corpus
siblings as old references. Changing `corpus.to` in `fold.yaml` instead does not
work either — it also drives where `fold.py --scaffold` writes.

`fold.yaml` authoring for parts 2–4 is planning-tier work and happens before
that part's tasks, not inside them. Part 1 is the exception: its single 005
entry is written inside task 7, because the format it validates does not exist
until then.

**Before part 2 starts, pre-seed `allow_dropped_ids` with the corpus's 22
source-side span artifacts.** `SPAN_RE`'s single-backtick branch is `[^`\n]+`,
so where a source spec wraps an inline span across a line the backticks re-pair
and capture the prose between — yielding garbage "identifiers" like `` `, binds
` `` and `` `'; on **first** sight call '` ``. There are 22 across 11 files. They
are a property of the source, computable before any rewrite begins, and no
rewrite can make them go away. Seeding them in a planning-tier pass keeps the
"rewrite tasks never edit `fold.yaml`" rule intact; leaving them unseeded spends
22 human escalations on noise.

Measured cost of the guard on a real fold: a faithful rewrite of spec 005 — all
markers deleted, all ten headings reworded, every paragraph reflowed, two
sections merged, 018's amendment absorbed — cost exactly **one**
`allow_dropped_ids` entry, for the one source-side artifact in the document,
and came out at zero findings. The only rewrite-side risk the guard caught in
rehearsal was a reflow wrapping `` `lode task brief` `` across a line, which
rule 9 exists to prevent rather than exempt.
