---
status: draft
covers: NO-SPEC
---
# Spec corpus consolidation — part 5: cutover

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (- [ ]) syntax for tracking.

**Goal:** Retire `docs/specs/` and put the folded corpus in its place, repoint
every inbound reference in the repository, and settle the residuals parts 2–4
recorded. After this part there is one corpus, `fold.py` and `refmap.py` are
gone, and `mapping.yaml` is the durable record of where each old section
landed.

The contract is part 1,
`docs/plans/2026-08-11-spec-corpus-consolidation-1-fold-tooling.md`: its
cutover order (**Series**, "Part 5 runs in this order") binds section 3 below.
Its **Rewrite rules** do not — they govern folding, and no document is folded
here.

## The gate is already discharged

`./scripts/fold.py --check` passes clean, without `--partial`, at this
branch's merge-base. Every anchor in the eighteen source documents is placed
or dropped with a reason. That is the whole job `fold.py` existed to do, and
it is done; nothing below re-earns it.

This matters because it decides the order. **Rule 7 and `--check --ids` pin
every backticked span in the folded corpus**, so the content repairs in
section 4 — which retire spans on purpose — cannot run while `fold.py` still
gates the tree. They therefore run *after* the move, against the new
`docs/specs/`, with `fold.py` already deleted.

Part 2 warned that `git rm -r docs/specs` is "the last moment the surviving
material can be recovered from the source". It is not: git keeps the old
corpus at `e4e2920:docs/specs/…` forever, and task 4.1 reads it from there.
The warning was about attention, not about availability, and this plan pays
that attention explicitly.

## Rulings

Continuing part 4's numbering; 17–26 are part 5's.

17. **The four consolidation plans are excluded from the reference rewrite.**
    `docs/plans/*spec-corpus-consolidation*` is the migration's own record and
    its subject *is* the old numbering — repointing it turns "fold 026 + 033
    into 026" into "fold 026 + 026 into 026". Every other plan is repointed,
    so a reader following `014 §6` lands on `025 §11`. Human ruling,
    2026-08-14.

18. **The `D<n>` / `Q<n>.<n>` design-record labels are stripped corpus-wide.**
    Part 3 left the scheme half-retired and part 4 established the fact that
    settles it: 003 is retired, so every one of these labels points at a
    document no reader can open. Strip the label, keep the claim. The
    per-document open-question scheme (`Q008.2`, `Q018.1`, `Q024.4` — three
    digits, keyed by source document) is a different scheme and stays. Human
    ruling, 2026-08-14.

19. **The `ls:`→`wl:` respell is completed, and criterion 19 is amended
    rather than satisfied.** Folded 025 §24's criterion 19 reads "No `ls:`,
    `lsc:` or `lsid:` occurrence remains in `docs/`", which no corpus can
    satisfy: 025 §14 *is* the record of the rename and must name both
    spellings to state it. The respell completes everywhere the prefix is
    *used*; criterion 19 gains an exception for the sections that document
    the rename. Human ruling, 2026-08-14; the exception is this plan's.

20. **Reconciling a spelling against `ns/` is transcription, not design.**
    Part 4 recorded this for folded 007's path-style concept IRIs
    (`wl:status/accepted` where `ns/concept.ttl` ships `wlc:accepted`) and it
    generalises: where the schema already ships the term, copying it in is
    the same act as ruling 16's disjointness fix. Where `ns/` *lags* the spec
    — `wlc:epic`, the `wl:Workstream` disjointness member — part 4's residual
    stands and nothing here touches `ns/`.

21. **A reference that resolves to nothing is deleted, not repointed.**
    Ruling 7's treatment of the dangling `000` pointers, applied to the rest:
    `000-umbrella-architecture.md` (never existed in this corpus), `034
    §12.1`–`12.5` (only `sec-12` ever existed), `014 §7.2`, `006 §4.2` and
    `023 §3.3.4`. The sentence keeps its claim and loses the pointer. Part 1
    called this out at hand-off and required the decision be made rather than
    silenced; this is the decision.

22. **A fixture identifier is data and is excluded, never rewritten.**
    `001-alpha.md`, `002-beta.md`, `003-gamma.md`, `004-delta.md`,
    `006-zeta.md`, `007-notes.md`, `001-fixture.md`, `WL-SPEC-900` and
    `WL-SPEC-999` name a hermetic test corpus. `refmap.py` already prunes the
    Go and Python fixtures; the plans quoting them need `--ignore-glob`, and
    `scripts/refmap.py` needs it for its own comment. `WL-SPEC-999` inside
    folded 026 is a worked "not found" example and stays as written.

23. **Folded 026's acceptance criteria 3, 9 and 11 are retired, not
    re-aimed.** They test that consolidation renders a *specific* pre-fold
    amendment, and the folded corpus has no amendment edges — part-3 ruling 3
    keeps only `status` and `requires`. Re-aiming them at a hypothetical
    post-cutover amendment would assert a state no one can check on day one.
    The machinery they tested (§3, §3.1, §3.2) stays correct and stays
    documented; what goes is three criteria whose subject the fold dissolved.

24. **A reference resolved by hand before the rewrite is written in
    *old*-corpus form.** Part 2's invariant — hand-repointing inside the
    folded corpus is only ever done for references `refmap.py` cannot see —
    generalises to every file the rewriter scans. The nine unmapped
    references below are all visible to it, so each is repaired to a *live
    old anchor* and left for `-w` to convert. Writing the new anchor by hand
    double-maps it.

25. **`WL-SPEC-999` in folded 026 §12 is neutralised for the rewrite and
    restored after it.** It is a fixture literal (ruling 22) naming a
    deliberately absent document, `refmap.py` has no inline escape, and
    `--ignore-glob` is file-level — excluding folded 026 would forfeit its
    real substitutions. So the token is replaced with a placeholder before
    `-w` and restored after, leaving the line byte-identical in the commit.
    The claim it makes stays true: 999 is absent from the new corpus too.

26. **`docs/specs/` is not rewritten before it is deleted.** Part 1's step 1
    runs `refmap.py -w` over the whole repo, which includes 595 sites in a
    directory step 2 removes. Harmless but noisy in the diff, and it makes
    the rename read as 595 edits plus a delete. `--ignore-glob 'docs/specs/*'`
    keeps the cutover commit legible. The new corpus at `docs/specs2/` is
    still rewritten, which is the point — its prose carries old-corpus
    numbering on purpose.

## Tasks

### Task 1 — The mechanical cutover

One commit's worth of scripted steps, in part 1's order. Nothing here is a
judgment call; if a step surprises you, stop rather than improvise.

- [x] Confirm the gate: `./scripts/fold.py --check` clean, no `--partial`.
- [x] **Resolve the nine unmapped references first** — `-w` refuses to write
      while any remain, and each is a ruling-21 or ruling-22 case. All are
      written in old-corpus form (ruling 24) for `-w` to convert:
      - `ns/shapes.ttl:89,97` — `014 §7.2` never existed; §7 is the section
        that says a superseded section explains itself. Write `014 §7`.
      - `docs/plans/2026-08-02-keycloak-primary-auth-2-link-and-tokens.md:1096`
        — `023 §3.3.4` never existed. Write `023 §3.3`.
      - `docs/specs2/026-design-doc-queries.md:706` — `spec 006 §4.2` in an
        illustrative edge sentence; §4.2 never existed. Write `006 §4`.
      - `000-umbrella-architecture.md` ×4, in
        `docs/plans/2026-07-30-data-platform-kg-requirements.md:84` and
        `docs/plans/2026-07-30-design-documents-as-graph-objects.md:51,255,274`
        — a document that never existed in this repo. Ruling 21: drop the
        pointer, keep the claim; where it sits in a `file:line` inventory,
        drop that entry.
      - `docs/specs2/026-design-doc-queries.md:960`'s `WL-SPEC-999` —
        ruling 25's neutralise/restore, not a repair.
- [x] `./scripts/refmap.py --dry-run --corpus-to docs/specs --allow-dropped
      --ignore-glob 'docs/specs/*'
      --ignore-glob 'docs/plans/*spec-corpus-consolidation*'
      --ignore-glob 'docs/plans/2026-08-03-design-doc-queries-*'
      --ignore-glob 'docs/plans/2026-08-03-spec-shorthand-references.md'
      --ignore-glob 'docs/plans/2026-08-09-design-doc-sync-*'
      --ignore-glob 'scripts/refmap.py'` — read the substitution count and
      the unmapped list. Expect **zero unmapped**. Every glob above is
      ruling 17 or 22; adding one for any other reason is a plan defect,
      because `--ignore-glob` hides a stale reference and says nothing when
      it does (part 1, hand-off).
- [x] Re-run with `-w` instead of `--dry-run`. If it must be re-run after an
      amendment, `git checkout` the previous run first — `--force` re-applies
      the full rewrite over rewritten text, straight into id collisions.
- [x] `git rm -r docs/specs && git mv docs/specs2 docs/specs`.
- [x] `git rm docs/specs/.refmap-applied` — the marker `-w` dropped in
      `docs/specs2/`, carried in by the move.
- [x] `git rm scripts/fold.py scripts/refmap.py` and their tests. Keep
      `docs/specs/mapping.yaml` and `docs/specs/fold.yaml`.
- [x] Deduplicate `requires:` where the rewrite collapsed two entries onto
      one target: folded 008 (011 and 004 → 004), folded 026 (014 and 025 →
      025), folded 025 (018 and 004 → 004).
- [x] Flip `status`/`issued` per part 1's rule — accepted where every source
      was accepted, and `issued` set to the newest source's date. A document
      with any draft source stays `draft`.
- [x] `spec_corpus = "docs/specs"` in `.worklode/config.toml`, then
      `lode doc sync --dry-run`.
- [x] `./scripts/secindex.py docs/specs` and commit the regenerated
      `index.yaml`.
- [x] `./scripts/secfmt.py -l docs/specs` and `./scripts/secmeta.py
      docs/specs` — clean.

### Task 2 — The references `refmap.py` cannot see

Runs after task 1, against the new `docs/specs/`. Every item is a reference
the rewriter is structurally blind to, which is why it is hand work rather
than a wider regex — part 1's rule 5 records that widening the prose regex
has already caused two critical bugs.

- [x] **Bare document numbers.** Part 2's hand-off lists 25 across folded
      016, 017, 019, 020, 021, 029 and 032, in shapes like `rides spec 014`,
      `since spec 018`, `027 mints`, `014's document IRI`. Part 4 adds folded
      026 §3.2 and §10's two bare `014`s. Repoint each against
      `mapping.yaml`; where the number names a document the fold retired and
      no successor claim exists, apply ruling 21.
- [x] **Letter-suffix references.** `\d+(?:\.\d+)*` cannot match `019 §4.3a`,
      so these are neither substituted nor reported. Known sites: folded 029's
      `(019 §4.3a)`, folded 026 §4.2a's, `internal/cmd/show_test.go:774`,
      `CLAUDE.md`, `AGENTS.md`, `docs/authoring-design-docs.md`, a 2026-08-03
      plan, and twice in `internal/designdoc/resolve.go`. Sweep for
      `§\d+(\.\d+)*[a-z]` repo-wide rather than trusting that list.
- [x] **Folded 026's four reference-form worked examples**, which `-w`
      rewrote silently because they are *mapped*: §3's `014` / `WL-SPEC-14`
      pairing, §3's `--spec 15` / `WL-SPEC-15`, §10's `WL-SPEC-23` /
      `WL-SPEC-023` zero-padding pair, and §12 criterion 7's
      `WL-SPEC-14#sec-2.1` / `WL-SPEC-4`. Each example's whole point is the
      spelling being rewritten; restate all four against the new numbering so
      the claim each makes is true again.
- [x] **Self-citations.** Folded 008 cites `008` at three sites, folded 004
      cites `004` at one. All resolve correctly; strip the number so a
      document stops citing itself by name.

### Task 3 — Recover what 013's over-scoped supersession dropped

`docs/specs/013-reconciliation.md`'s frontmatter supersedes §3, §5 and §8
wholesale, but its inline notes scope the supersession far more narrowly.
`fold.yaml` had no legal alternative — `--check` rejects a retired anchor in
a `from:` — so the folded document lost material nothing else in the corpus
states. Read the source from git: `git show
e4e2920:docs/specs/013-reconciliation.md`.

- [x] Recover the `events.applied_at timestamptz` column declaration
      (nullable, set when an event's apply completes by either path) and its
      down-migration sentence. Folded §0 refers to the marker and §2.1 relies
      on its behaviour, with nothing declaring it.
- [x] Recover the Testing section: the replay test, the poll test, "Both
      doctors: table-driven over broken-setup fixtures", the
      ephemeral-Postgres note.
- [x] Recover the Acceptance criteria section: the replay, poll,
      `lode project doctor` and `lode doctor` criteria, and "Every command
      emits deterministic `--json`". 013 is otherwise the only document in
      the corpus with neither section.
- [x] Resolve the two scars whose referents left the corpus: folded §0's "the
      `task_docs` link and the spec-drift engine it supported are superseded"
      and folded §5's "the (now-superseded) engine 3's third finding". Keep
      the claim, drop the pointer at material no longer there (ruling 21).

### Task 4 — Complete the `ls:`→`wl:` respell

Ruling 19. The surviving sites are few and known.

- [x] Folded 016: `ls:Skill` (§1, §3), `ls:recommendsSkill` (§1, §7), and
      §0's note "Read every `ls:` below as `wl:`" — which the respell makes
      pointless, so it goes with them.
- [x] Folded 004: `ls:mirrors`.
- [x] Folded 025: `lsc:DesignDocStatus` (§9-ish) and `ls:Plan`/`ls:ADR`/
      `ls:Spec`/`ls:DesignDoc` (the §12 sentence about what 006 got wrong).
- [x] Leave §14's rename record alone — `ls:`/`lsc:`/`lsid:` are its subject,
      including the three-row prefix table and the sentence counting
      occurrences.
- [x] Amend criterion 19 to except that record, so the corpus can satisfy it.

### Task 5 — Strip the design-record labels

Ruling 18. Roughly fifty sites across folded 004, 006, 007, 008 and 016.

- [x] Strip every `D<n>` and `Q<n>.<n>` (one- or two-digit `n`). A trailing
      parenthetical `(D2)`, `(D8/D11/D14)`, `(Q14.3)` simply goes. A label
      carrying a sentence's subject — "This is D14 applied to skills",
      "D5 says …" — is reworded to state the claim directly; the claim is
      what survives, and it is stated in the folded corpus already.
- [x] Keep `Q008.2`, `Q018.1`, `Q024.4` and their siblings: three-digit,
      keyed by source document, a different scheme that still resolves.
- [x] Folded 006 §8 and §12 already shed theirs during part 4 — do not
      double-count them looking for a label that is gone.

### Task 6 — The remaining part-4 residuals

- [x] **Transcribe from `ns/` where it is ahead** (ruling 20): folded 007
      §3.3's SPARQL spells concept IRIs path-style (`wl:status
      wl:status/accepted`) where `ns/concept.ttl` ships `wlc:accepted`.
- [x] **Stale document slugs**, all illustrative Turtle naming retired
      documents: folded 006 §9's `wlid:doc/spec-worklode-009`, folded 025
      §4.1's `spec-worklode-014`, folded 025 §11.2's
      `wlid:section/spec-worklode-004/sec-4` and
      `wlid:section/spec-worklode-013/sec-3.1`.
- [x] **Folded 026's acceptance criteria 3, 9 and 11** — retire them
      (ruling 23), leaving §3/§3.1/§3.2's machinery documented and untested
      against a pre-fold amendment that no longer exists.
- [x] **Folded 025 §14.3's three falsified statements**: the "exactly one
      cross-project reference today — this document's own `amends:
      rdf-registry:ADR-0006`" claim, which no folded document can carry; the
      `023` / `WL-SPEC-23` normalisation arithmetic the rewrite mangled; and
      the `spec-worklode-014` doc slugs. Restate each against the new corpus.
- [x] **016 §1 says "Two mints" and adds a third** — the disjointness member
      is a third addition the Turtle block never declares, and §7's
      dependency bullet repeats the undercount. Correct both counts; do not
      fabricate Turtle.

### Task 7 — Close the books

- [x] `docs/follow-ups.md`: record what cutover deliberately did not fix —
      031 §2.3's stale single-instance claim, 024 §3.1's missing
      worktree-exit event, 009's 2026-07 implementation-status report, and
      `ns/`'s three pre-025 mirrors (part 4's residual: not part 5's to fix).
      Check the file first; several may already be there.
- [x] Update `CLAUDE.md` and `AGENTS.md` where they describe the corpus or
      the fold tooling. Both already point at `docs/specs/`; what changes is
      any mention of `docs/specs2/`, `fold.py` or `refmap.py`.
- [x] `go build ./... && go test ./internal/designdoc/ ./internal/cmd/` —
      the reference rewrite touched Go source and testdata.
- [x] `./scripts/secfmt.py -l docs/specs`, `./scripts/secmeta.py docs/specs`,
      `./scripts/secindex.py docs/specs` — all clean, index regenerated after
      tasks 2–6 have moved text.

## What this part does not do

Recorded so a reviewer does not read them as omissions:

- **`ns/` keeps its pre-025 vocabulary.** `wlc:epic` and the `wl:Workstream`
  disjointness member mirror a spec 025 that is unimplemented. CLAUDE.md's
  rule is amend the spec first, then mirror the term; the spec is amended and
  the mirror follows when 025 ships.
- **Spec-vs-code drift stays drift.** Folded 004 §1.3/§6.4's `closedStates`
  predicate and the shipped constant tuple disagree; `docs/follow-ups.md`
  owns it.
- **No section is renumbered.** Anchors are frozen; tasks 2–6 change prose
  inside sections and never their numbering.

## What execution found

Four things the plan did not anticipate, recorded because each is a property
of the tooling or the corpus rather than of this run.

- **`refmap.py` has two blind spots beyond the letter suffix its rule 5
  records.** `DEFAULT_IGNORE_GLOBS` excludes `*_test.go` outright, so every
  citation in a Go test — including ones carrying a real `§` — was never
  rewritten; and the shape `<filename>.md §N` is not one reference to the
  tool but a filename it maps plus a `§N` it cannot see, so it silently
  produced `025 §8` from `014 §8` where the correct target was `025 §10`.
  Three sites carried that shape with a rewritten filename: migration 0009's
  comment and two plans quoting it. The others (`029 §3`, `032 §2`, `032
  §10`) survived only because those documents map to themselves identically.
- **Ruling 22's `--ignore-glob` was too blunt.** Excluding a whole plan file
  for one fixture identifier also excluded its real references — seven files,
  237 substitutions, four of which `secmeta.py` then caught as `covers:`
  resolving to no file. Recovered by replaying the pre-move dry run's own
  substitution record. Part 1's hand-off warned exactly this ("prefer the
  narrowest glob that clears them"); the warning was right and the plan
  should have made the record-and-replay the primary method, not the repair.
- **025's prefix-rename record is §17, not §14.** §14 is document frontmatter
  and carries no `ls:` at all. Criterion 19 also had to except *itself*,
  since it names all three retired spellings in its own text.
- **The `Q<n>.<n>` labels split two ways, and the split is not cosmetic.**
  `Q14.x` and `Q15.x` belong to 003's design record (they hang off D14 and
  D15) and were stripped; `Q17.x` in folded 017 is that document's own
  open-question numbering, keyed by document number without zero-padding,
  and stays — as do the zero-padded `Q008.x`, `Q018.x`, `Q024.x`. The test
  is whether a `D<n>` with the same number existed, not the digit count.
