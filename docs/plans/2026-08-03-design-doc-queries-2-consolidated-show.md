---
status: draft
covers:
  - docs/specs/026-design-doc-queries.md#sec-3
  - docs/specs/026-design-doc-queries.md#sec-3.1
  - docs/specs/026-design-doc-queries.md#sec-3.2
  - docs/specs/026-design-doc-queries.md#sec-9
  - docs/specs/026-design-doc-queries.md#sec-10
requires:
  - 2026-08-03-design-doc-queries-1-corpus-and-list.md
---
# Design-doc queries 2/3: consolidated `lode doc show`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 3 of spec 026's plan series. Part 1
(`2026-08-03-design-doc-queries-1-corpus-and-list.md`) builds the corpus
loader, reference resolution, defect reporting and the `list`/`sections`
commands; it must be merged first — every task here consumes its surfaces.
Part 3 (`2026-08-03-design-doc-queries-3-permanence-gate.md`,
`scripts/secfrozen.py`) is independent of this part: the commit-time gate is
where a cycle is *prevented*; task 3's cycle marker is the renderer's
backstop for a corpus that got one through (`--no-verify`, a rebase). Spec
026 §4.1 keeps them independent — do not collapse them. Task numbers restart
at 1 in this file; the cross-part dependency is the `requires:` frontmatter
edge above, never a task number.

**Goal:** `lode doc show <ref> [--resolved] [--section <anchor>]
[--with-drafts]` — the consolidated read path of 026 §3: amendments and
supersessions folded inline under the sections they act on, transitively,
with attribution, as a labelled read-only view.

**Architecture:** One new unit, `internal/designdoc/resolved.go` (026 §9's
table is fixed — this plan owns that row only). It builds a normalized edge
index from both directions of every `amends`/`amendedBy` /
`replaces`/`isReplacedBy` map (§3.1's union), gates each edge on the
claiming document's status, and renders a document or section as §3.2's
fixpoint: recursive expansion whose termination measure is a
rendering-global *surfaced* set (body-once), with a per-path visited list
that turns a true cycle into a marker plus a defect instead of a hang. The
CLI is one subcommand's flags in `internal/cmd/doc.go` (created by part 1;
this plan only extends it). No endpoint, no background loop, no store
operation — therefore **no `worklode_*` metrics** (026 §1).

**Tech Stack:** Go 1.25+, stdlib plus what `internal/designdoc` already
imports (`gopkg.in/yaml.v3`). No server, no Postgres — every test here runs
without infrastructure.

**Spec:** `docs/specs/026-design-doc-queries.md` §3, §3.1, §3.2; §7's
`resolved.go` row; §8's consolidation bullets. Read those sections in full
before starting any task.

**Read first:**
- 026 §3, §3.1, §3.2 (the semantics), §4 (defect reporting the renderer
  feeds), §10 items 3 and 8 (acceptance shapes)
- `internal/designdoc/designdoc.go` — `Parse`, `Document`, `Section`
  (`Body` excludes subsections; `Children` holds them)
- `internal/designdoc/frontmatter.go` — `Frontmatter`, `RefList`,
  `AnchorMap` (the four edge maps this plan consumes)
- `internal/designdoc/corpus.go`, `query.go`, `check.go` **as landed by
  part 1** — the consumed surfaces below are assumed shapes; reconcile names
  against the real code at execution time
- `docs/specs/014-…md` and `docs/specs/025-…md` frontmatter — the live
  amendment/supersession data the real-corpus tests run over
- `docs/authoring-design-docs.md` — the mirror-edge convention the fixtures
  imitate

**Non-goals:** everything part 1 owns (§1, §2, §2.1, §2.2, §2.3, §4, §5,
§6 — `corpus.go`, `query.go`, `check.go`, `doc list`, `doc sections`, ref
forms, `--docs`); everything part 3 owns (§4.1, `scripts/secfrozen.py`);
§4.2 shorthand (`shorthand.go`, planned in
`2026-08-03-spec-shorthand-references.md`); the web view of consolidations
(026 §11); any write verb; any metric.

---

## Consumed surfaces (assumed — reconcile, don't redefine)

Part 1 is being planned and executed adjacently. The names below are this
plan's *assumptions* about its exports; the semantics are the spec's and are
not negotiable, but the identifiers may land differently. Before each task,
re-read `corpus.go`/`query.go`/`check.go` as they exist and substitute the
real names. If a listed surface does not exist at all when a task starts,
**stop and escalate to the coordinator** — 026 §9 places it in part 1's
files, so the fix belongs there, not as a private copy in `resolved.go`.

```go
// corpus.go (part 1)
func LoadCorpus(root string) (*Corpus, error)   // root holds specs/ and plans/
type Corpus struct{ /* docs indexed by repo-relative path */ }
// Per-document access to: repo-relative path, *designdoc.Document (and so
// .Frontmatter and .Sections). Called Doc below.
func (c *Corpus) Resolve(from *Doc, ref string) (*Doc, string, error)
//                       ^ target doc, anchor ("" = whole doc), or a defect

// query.go (part 1) — §3.1's effectiveness gate, some spelling of:
func (c *Corpus) Effective(claimant *Doc, withDrafts bool) bool
// true for status accepted/superseded, for no status at all, and for a
// claimant outside the corpus; false (pending) only for draft w/o the flag.

// check.go (part 1) — Defect and its printing/exit-code path, some spelling of:
type Defect struct{ File, Key, Ref, Msg string }
```

## Design decisions (pinned — implementers do not re-litigate these)

Numbered so review comments and deviations can cite them.

1. **Edge normalization.** All four maps of every corpus document are read
   and unioned into one edge set: `amends` and `replaces` contribute
   `(acting=this doc + map key, target=value)`, `amendedBy`/`isReplacedBy`
   contribute `(acting=value, target=this doc + map key)`. Duplicates
   (both mirrors present) collapse on
   `(kind, actingPath, actingAnchor, targetPath, targetAnchor)`. A
   half-maintained mirror still registers (§3.1); the *disagreement* is
   check.go's to report, and it never changes an answer here.
2. **Doc-scoped means either end lacks a section.** An acting side of `"."`
   (or a value with no fragment when read from a mirror map) or a target
   with no fragment makes the edge doc-scoped: it renders as a banner
   reference and is **never inlined** — no section-shaped payload (§3,
   §3.2). A doc-scoped edge targeting a *section* renders one reference
   line above that section's body; an edge targeting a *whole document*
   renders in the top banner block (026 §12 item 3: 025 §10's doc-wide
   amendment of 018 is a banner).
3. **Effectiveness (§3.1)** comes from part 1's gate: pending only when the
   acting document is in the corpus with `status: draft` and `--with-drafts`
   is off. Pending edges render as references marked `pending` and leave
   the target's rendering otherwise untouched.
4. **Deterministic order.** Multiple edges onto (or off) one section sort by
   the acting (resp. target) document's `issued` (lexicographic; the value
   is `YYYY-MM-DD`; a missing `issued` sorts first as the empty string),
   then repo-relative filename, then anchor. Amend and replace edges share
   one sorted list; the marker text distinguishes them.
5. **The fixpoint is body-once memoization, not a depth parameter.** The
   renderer keeps one rendering-global `surfaced` map (`"path#anchor"` →
   emitted | back-referenced). A section's body is emitted at its first
   occurrence; every later occurrence emits a back-reference marker. Each
   body emission strictly grows `surfaced`, which is bounded by the number
   of sections in the corpus — that is the termination proof, and no depth
   limit exists anywhere in this plan. This is what bounds a diamond.
6. **Cycle detection is per expansion path, forward edges only.** The
   recursion carries the ordered list of sections on the current chain of
   *forward* (acting-onto-target) inlines. Check order on entry:
   on-path → cycle marker + Defect + stop; surfaced → back-reference;
   otherwise emit and recurse. Acting edges point new→old, so a forward
   revisit is always a genuine `amends`/`replaces` cycle.
7. **Backfill: expansion is bidirectional.** Consolidating a section also
   inlines, after its incoming edges, the consolidation of each section it
   *itself* effectively acts on, as a marked context block — skipped when
   that target is on the current path (it is already visible directly
   above), back-referenced when surfaced elsewhere. This is the decision
   that discharges §3.2's root-independence paragraph ("start from the
   newest document touching a subject and backfill") and §8's
   root-independence test: without it, entering the newer document of an
   amendment pair never surfaces the still-live amended text, and the §8
   test cannot pass. It goes beyond §3.2's one-directional definition
   sentence; the deviation is deliberate and is called out in this plan's
   header for spec review.
8. **An inlined section is its subtree.** The block is the attribution
   line, the section's `Body`, then each child section — heading line
   included — consolidated recursively (registered in `surfaced`, its own
   edges expanded). `--section` prints the same subtree shape. Root-level
   sections of the shown document render their own heading (rebuilt from
   the exported `Level`/`Number`/`Title`/`Anchor` fields, anchor kept — it
   is the citation) and no attribution line.
9. **`--section` forward-resolution is a `--resolved` behaviour.** Without
   `--resolved`, `--section` is a raw slice (cat semantics) even on a
   replaced anchor. With `--resolved`, an anchor with at least one
   *effective* replacement prints, for each replacement in decision-4
   order, a forward-resolution note line and then that replacement's
   consolidation — a split forward-resolves to all its replacements. A
   *pending* replacement does not trigger forward-resolution.
10. **Display refs.** A section is cited as `<num>#<anchor>` where `<num>`
    is the filename's leading digits when the basename matches `^\d+-`
    (`014#sec-6`), else the basename without `.md`
    (`2026-08-03-foo#sec-2` never occurs today — plans have no anchors —
    but the rule is total). A whole document is cited by the same rule
    without the fragment.
11. **Sources.** The header banner lists, sorted by path, every document
    that contributed at least one emitted body. Banner-only and
    pending-only documents are visible in their own reference lines and are
    not listed as sources.
12. **The output is a view.** Nothing writes it back; no flag writes it;
    every rendering opens with the consolidated-view banner. Defects the
    renderer discovers (cycles, an acting ref `Corpus.Resolve` cannot
    resolve) are returned on the `Rendering` and funnelled into part 1's
    stderr/exit-code path by the command — printed after the results, exit
    non-zero (§4).

## Marker grammar (exact strings — tests assert them byte for byte)

`<ref>` is decision 10's display form. The two attribution forms are 026
§3's fenced example verbatim; the rest are minted here and frozen by this
table.

| Situation | Line emitted |
|---|---|
| Inline, acting section amends the one above | `**[amending <ref>]:**<br>` |
| Inline, acting section replaces the one above | `**[superseding <ref>]:**<br>` |
| Backfill context: the block below is the target this section amends | `**[amends <ref>]:**<br>` |
| Backfill context: the block below is the target this section replaces | `**[replaces <ref>]:**<br>` |
| Body-once back-reference | `**[see above: <ref>]**` |
| Cycle | `[cycle: <ref> → <ref> → … → <ref>]` (from first occurrence back to the repeat) |
| Pending amendment (draft claimant) | `**[pending: amended by <ref> (draft)]**` |
| Pending supersession | `**[pending: superseded by <ref> (draft)]**` |
| Doc-scoped actor onto this section | `**[amended by <ref> (document-wide)]**` / `**[superseded by <ref> (document-wide)]**` |
| Acting ref that does not resolve | `**[unresolved: <raw ref>]**` |
| `--section` forward-resolution note | `**[forward-resolved: <ref> → <ref>]**` |

Header banner, first lines of every `--resolved` rendering (sources comma
separated; edges targeting the whole document, and pending doc-wide claims
suffixed ` (pending, draft)`, follow as further `> ` lines):

```markdown
> **Consolidated view — not a source document.** Drawn from: 013-reconciliation.md, 014-design-documents-as-graph-objects.md.
> Computed by `lode doc show --resolved`; never write this text back.
```

## File structure

| File | Change | Responsibility |
|---|---|---|
| `internal/designdoc/testdata/consolidate/base/specs/*.md` | new | fixture corpus: chain of three, diamond, split, pending draft, no-status, doc-scoped, one-sided mirrors |
| `internal/designdoc/testdata/consolidate/cycle/specs/*.md` | new | a constructed two-document `amends` cycle |
| `internal/designdoc/testdata/consolidate/dangling/specs/*.md` | new | an acting ref that resolves to nothing |
| `internal/designdoc/resolved.go` | new | everything §3/§3.1/§3.2: edge index, gate application, fixpoint, markers, `Resolved()` |
| `internal/designdoc/resolved_test.go` | new | fixture-driven tests, tasks 2–6 |
| `internal/cmd/doc.go` | modify (part 1 creates it) | `show` flags: `--resolved`, `--section`, `--with-drafts`, fragment sugar |

The core algorithm, written out so every task implements the same one:

```go
// consolidate renders sec and everything acting on it. path is the chain
// of forward inlines above it; r.surfaced is rendering-global.
func (r *renderer) consolidate(sec secRef, path []secRef) {
	if onPath(path, sec) {                       // decision 6
		r.cycleMarker(append(path[indexOf(path, sec):], sec))
		r.defect(cycleDefect(path, sec))
		return
	}
	if _, seen := r.surfaced[sec.id()]; seen {   // decision 5
		r.backRef(sec)
		return
	}
	r.surfaced[sec.id()] = OccurrenceEmitted
	r.emitDocWideBanners(sec)                    // decision 2
	r.emitBodyAndChildren(sec, path)             // decision 8
	for _, e := range r.incoming(sec) {          // decision 4 order
		if e.pending { r.pendingRef(e); continue }
		if e.actingDoc == nil { r.unresolvedRef(e); continue }
		r.attribution(e)                         // **[amending …]:** / **[superseding …]:**
		r.consolidate(e.actingSection(), append(path, sec))
	}
	for _, e := range r.outgoing(sec) {          // decision 7 backfill
		if e.pending || e.docScoped() || e.targetDoc == nil { continue }
		t := e.targetSection()
		if onPath(path, t) { continue }
		if _, seen := r.surfaced[t.id()]; seen { r.backRef(t); continue }
		r.context(e)                             // **[amends …]:** / **[replaces …]:**
		r.consolidate(t, append(path, sec))
	}
}
```

---

## Tasks

### Task 1 — Fixture corpora for consolidation

```yaml
kind: chore
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Create three miniature corpora under
`internal/designdoc/testdata/consolidate/`, laid out the way part 1's
`LoadCorpus` expects (a root containing `specs/`; add `plans/.gitkeep` only
if the landed loader requires the directory). They are the shared input of
tasks 2–6, so they are authored once, first, and later tasks may extend but
never reshape them. Every status is deliberate: `base` exercises effective
and pending claims; `cycle` and `dangling` are defective corpora kept apart
so their defects don't pollute `base`'s assertions.

The proof for this task is that every fixture parses: a
`TestConsolidateFixturesParse` in `resolved_test.go` that walks the three
directories, runs `designdoc.Parse` on each file, and asserts no error and
byte-exact `Bytes()` round-trip.

- [ ] **Step 1: `base` corpus — seven files under `base/specs/`.**

`100-alpha.md` — every target shape. Note the one-sided **target-side**
mirror for 140 (no `amends` in 140), which is what proves §3.1's union:

```markdown
---
status: accepted
issued: 2026-01-01
amendedBy:
  "#sec-1":
    - 140-nostatus.md#sec-1
---
# Spec 100 — Alpha

Alpha preamble.

## 1. Chain base {#sec-1}

Alpha body one.

## 2. Split base {#sec-2}

Alpha body two.

## 3. Diamond left {#sec-3}

Alpha body three.

## 4. Doc-wide target {#sec-4}

Alpha body four.

## 5. Pending target {#sec-5}

Alpha body five.

## 6. Diamond right {#sec-6}

Alpha body six.
```

`110-beta.md` — its own claims are acting-side only (no mirrors in 100),
it includes the diamond actor (one section amending two), and its
`amendedBy` mirrors 120's `amends` so exactly one edge in the corpus is
recorded on both sides (the dedupe case):

```markdown
---
status: accepted
issued: 2026-02-01
amends:
  "#sec-1":
    - 100-alpha.md#sec-1
  "#sec-3":
    - 100-alpha.md#sec-3
    - 100-alpha.md#sec-6
amendedBy:
  "#sec-1":
    - 120-gamma.md#sec-1
replaces:
  "#sec-2":
    - 100-alpha.md#sec-2
---
# Spec 110 — Beta

## 1. Chain link {#sec-1}

Beta body one.

## 2. Split first {#sec-2}

Beta body two.

## 3. Diamond actor {#sec-3}

Beta body three.
```

`120-gamma.md` — second link of the chain, second member of the split:

```markdown
---
status: accepted
issued: 2026-03-01
amends:
  "#sec-1":
    - 110-beta.md#sec-1
replaces:
  "#sec-2":
    - 100-alpha.md#sec-2
---
# Spec 120 — Gamma

## 1. Chain link two {#sec-1}

Gamma body one.

## 2. Split second {#sec-2}

Gamma body two.
```

`145-delta.md` — third link, so the chain of three section-scoped
amendments in 026 §12 item 3 exists verbatim:

```markdown
---
status: accepted
issued: 2026-03-15
amends:
  "#sec-1":
    - 120-gamma.md#sec-1
---
# Spec 145 — Delta

## 1. Chain head {#sec-1}

Delta body one.
```

`130-draft.md` — the pending claimant (§3.1):

```markdown
---
status: draft
issued: 2026-04-01
replaces:
  "#sec-1":
    - 100-alpha.md#sec-5
---
# Spec 130 — Draft

## 1. Pending replacement {#sec-1}

Draft body one.
```

`140-nostatus.md` — no `status` key at all (effective per §3.1), and no
`amends`: its edge exists only in 100's `amendedBy`:

```markdown
---
issued: 2026-05-01
---
# Spec 140 — No status

## 1. Statusless amendment {#sec-1}

Nostatus body one.
```

`150-docwide.md` — both doc-scoped shapes: a doc-wide actor onto a section
(`"."` key) and a section acting on a whole document (value without
fragment):

```markdown
---
status: accepted
issued: 2026-06-01
amends:
  ".":
    - 100-alpha.md#sec-4
  "#sec-1":
    - 100-alpha.md
---
# Spec 150 — Docwide

## 1. Whole-doc actor {#sec-1}

Docwide body one.
```

- [ ] **Step 2: `cycle` corpus — two files under `cycle/specs/`.**

```markdown
---
status: accepted
issued: 2026-01-01
amends:
  "#sec-1":
    - 210-cycb.md#sec-1
---
# Spec 200 — Cycle A

## 1. A one {#sec-1}

Cyca body one.
```

```markdown
---
status: accepted
issued: 2026-02-01
amends:
  "#sec-1":
    - 200-cyca.md#sec-1
---
# Spec 210 — Cycle B

## 1. B one {#sec-1}

Cycb body one.
```

(Files `200-cyca.md`, `210-cycb.md`. Part 3's `secfrozen.py` would refuse
this corpus at commit time; it lives in `testdata/` precisely because the
renderer must survive it anyway.)

- [ ] **Step 3: `dangling` corpus — one file under `dangling/specs/`.**

```markdown
---
status: accepted
issued: 2026-01-01
amendedBy:
  "#sec-1":
    - 999-nope.md#sec-1
---
# Spec 300 — Dangler

## 1. Dangling target {#sec-1}

Dangler body one.
```

- [ ] **Step 4: Write `TestConsolidateFixturesParse` and run it.**

In a new `internal/designdoc/resolved_test.go`: walk
`testdata/consolidate/`, `designdoc.Parse` every `.md`, assert nil error
and `bytes.Equal(doc.Bytes(), src)`.

Run: `go test ./internal/designdoc -run TestConsolidateFixturesParse -v` —
PASS, ten files parsed.

- [ ] **Step 5: Commit** — `git add internal/designdoc/testdata/consolidate internal/designdoc/resolved_test.go && git commit` (imperative subject, e.g. "Add consolidation fixture corpora for spec 026 §3").

### Task 2 — Edge index and the §3.1 gate applied

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Create `internal/designdoc/resolved.go` with the normalized edge model and
its index — decisions 1–4 — consuming part 1's `Corpus`, `Resolve` and
effectiveness gate (see Consumed surfaces; reconcile names first, escalate
if the gate does not exist). No rendering yet.

Types to define, exactly:

```go
type edgeKind int

const (
	edgeAmend edgeKind = iota
	edgeReplace
)

// edge is one normalized acting-onto-target claim, deduped across the
// mirror maps (decision 1).
type edge struct {
	kind         edgeKind
	actingDoc    *Doc   // nil when the acting ref did not resolve
	actingRaw    string // the ref as written, for the unresolved marker
	actingAnchor string // "" = document-scoped actor (".", or no fragment)
	targetDoc    *Doc   // nil when the target ref did not resolve
	targetAnchor string // "" = whole-document target
	pending      bool   // §3.1, already folded in for the withDrafts asked
}

type edgeIndex struct {
	incoming    map[string][]edge // "path#anchor" -> section-scoped edges onto it, sorted
	outgoing    map[string][]edge // "path#anchor" -> section-scoped edges it makes, sorted
	docWideOnto map[string][]edge // "path#anchor" -> doc-scoped actors onto the section
	ontoDoc     map[string][]edge // "path" -> edges targeting the whole document
}

func buildEdgeIndex(c *Corpus, withDrafts bool) (*edgeIndex, []Defect)
```

Sorting inside `buildEdgeIndex` is decision 4. An acting or target ref that
`Corpus.Resolve` rejects keeps the edge with the nil doc and contributes
the resolver's defect — reported, never dropped (§4).

The test, `TestBuildEdgeIndex` (table-driven over the `base` corpus),
proves each row:

| Assertion | Pins |
|---|---|
| `incoming["…100-alpha.md#sec-1"]` is exactly [110#sec-1, 140#sec-1] — 110 recorded acting-side only, 140 target-side only, both present, issued order | decision 1 union, decision 4 order |
| `incoming["…110-beta.md#sec-1"]` is exactly [120#sec-1] — the one edge in the fixtures recorded on **both** sides (120's `amends` and 110's `amendedBy`) collapses to a single entry | dedupe |
| edges from `130-draft.md` have `pending == true` with `withDrafts == false`, `false` with `true` | §3.1, `--with-drafts` |
| edges from `140-nostatus.md` are never pending | §3.1 no-status rule |
| `150-docwide.md`'s `"."` entry lands in `docWideOnto["…100-alpha.md#sec-4"]`; its `#sec-1 → 100-alpha.md` entry lands in `ontoDoc["…100-alpha.md"]`; neither is in `incoming` | decision 2 |
| `replaces` edges carry `edgeReplace`; the split (110#sec-2, 120#sec-2 onto 100#sec-2) sorts 110 before 120 | kinds, order |

- [ ] **Step 1:** Write `TestBuildEdgeIndex` against the assumed API; run `go test ./internal/designdoc -run TestBuildEdgeIndex` — fails to compile (red).
- [ ] **Step 2:** Implement `resolved.go` edge model + `buildEdgeIndex`.
- [ ] **Step 3:** `go test ./internal/designdoc -run TestBuildEdgeIndex -v` — PASS; then `go test ./internal/designdoc` to prove nothing else broke.
- [ ] **Step 4: Commit.**

### Task 3 — The consolidation fixpoint

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

Implement the renderer core in `resolved.go`: the `consolidate` function
from this plan's algorithm block, the `renderer` struct
(`strings.Builder`, `surfaced map[string]Occurrence`, `*edgeIndex`,
`[]Defect`, sources set), the marker constants from the grammar table, and
the public entry point:

```go
type ResolveOpts struct {
	WithDrafts bool
	Section    string // anchor; "" renders the whole document
}

type Occurrence int

const (
	OccurrenceEmitted Occurrence = iota + 1
	OccurrenceBackRef
)

type Rendering struct {
	Markdown string
	Sources  []string              // decision 11, sorted
	Surfaced map[string]Occurrence // "path#anchor" -> how it surfaced
	Defects  []Defect
}

func Resolved(c *Corpus, doc *Doc, opts ResolveOpts) (*Rendering, error)
```

Whole-document mode for this task: header banner (task 4 completes its
doc-wide lines), preamble, then each **top-level** section consolidated in
source order with `path` reset per section and `surfaced` shared across the
rendering; children are reached inside `consolidate` (decision 8), never by
the flat loop. A document's own later section already surfaced by an
earlier expansion renders its heading plus a back-reference.

Tests (each is a named subtest of `TestConsolidate`, over `base` unless
said otherwise), asserting on `Rendering.Markdown` with exact marker
strings and on `Surfaced`:

1. **Transitive chain** — `Resolved(100)`: under `sec-1`, in order:
   `Alpha body one.`, `**[amending 110#sec-1]:**<br>` + `Beta body one.`,
   `**[amending 120#sec-1]:**<br>` + `Gamma body one.`,
   `**[amending 145#sec-1]:**<br>` + `Delta body one.`, then
   `**[amending 140#sec-1]:**<br>` + `Nostatus body one.` — three levels
   expanded, nothing rendered as a bare reference (§3 transitivity; 026 §12
   item 3's "no rendering is depth-truncated").
2. **Diamond** — same rendering: `Beta body three.` appears exactly once
   (under `sec-3`); under `sec-6` the marker is
   `**[see above: 110#sec-3]**` and `Surfaced["…110-beta.md#sec-3"] ==
   OccurrenceEmitted` while the markdown contains exactly one copy of the
   body (§3.2 body-once).
3. **Split, ordered** — under `sec-2`: `Alpha body two.` retained, then
   `**[superseding 110#sec-2]:**<br>` + `Beta body two.`, then
   `**[superseding 120#sec-2]:**<br>` + `Gamma body two.` — both effective
   replacements render, issued order, replaced body kept beside them
   (§3.2's split rule).
4. **Cycle** — over the `cycle` corpus, `Resolved(200)`: terminates (run
   with `-timeout 30s`), markdown contains
   `[cycle: 200#sec-1 → 210#sec-1 → 200#sec-1]`, and `Defects` names both
   sections. Both bodies still appear once each — the marker stops the
   walk, not the rendering (§3.2 cycle marker; the gate in part 3 stays
   independent).
5. **Backfill** — `Resolved(110)`: under `sec-1`, `Beta body one.`, then
   incoming (`**[amending 120#sec-1]:**` …), then
   `**[amends 100#sec-1]:**<br>` + `Alpha body one.`, inside whose
   expansion 110#sec-1 comes back as `**[see above: 110#sec-1]**` (never a
   cycle marker — decision 7's path-skip and decision 6's forward-only
   cycle rule).

- [ ] **Step 1:** Write the five subtests; `go test ./internal/designdoc -run TestConsolidate` — red (undefined `Resolved`).
- [ ] **Step 2:** Implement renderer + `Resolved` whole-document mode.
- [ ] **Step 3:** `go test ./internal/designdoc -run TestConsolidate -timeout 30s -v` — PASS ×5; full package green.
- [ ] **Step 4: Commit.**

### Task 4 — Banners, pending references, sources, dangling refs

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

Complete the document rendering in `resolved.go`: the consolidated-view
header (exact two-line banner from the grammar section, sources per
decision 11), whole-document-target banner lines (from `ontoDoc`, pending
ones suffixed ` (pending, draft)`), doc-wide-actor reference lines above
the affected section's body (from `docWideOnto`), pending-edge reference
lines, and the `**[unresolved: …]**` line for a nil acting doc, with the
resolver's defect carried onto `Rendering.Defects`.

Tests — subtests of `TestResolvedRendering`:

1. **Header** — `Resolved(100)`: markdown begins with
   `> **Consolidated view — not a source document.** Drawn from: ` and the
   drawn-from list is exactly the base-corpus docs whose bodies were
   emitted (100, 110, 120, 140, 145 — not 130, not 150), sorted; the
   second line is the never-write-back line (§3's "labelled a consolidated
   view naming every source").
2. **Doc-scoped banner, never inlined** — `sec-4` carries
   `**[amended by 150-docwide.md (document-wide)]**` above `Alpha body
   four.` and `Docwide body one.` appears nowhere; the top banner block
   contains the line for 150#sec-1's whole-document claim (§3
   doc-scoped rule; §8's "doc-scoped banner").
3. **Pending (§3.1)** — with `WithDrafts: false`, `sec-5` shows
   `Alpha body five.` and `**[pending: superseded by 130#sec-1 (draft)]**`
   and no `Draft body one.`; with `WithDrafts: true` it shows
   `**[superseding 130#sec-1]:**<br>` + `Draft body one.` instead. The
   no-status claimant (140) is inlined in both runs (§8's §3.1-gate bullet,
   renderer half — part 1 owns the `doc sections` half).
4. **Attribution everywhere** — over the whole `Resolved(100)` markdown:
   every fixture body other than 100's own appears on a line preceded by an
   attribution, context, or pending marker naming its section — scan for
   each `body one/two/three` string and assert the marker precedes it
   (§8's "attribution on every borrowed block").
5. **Dangling acting ref** — over the `dangling` corpus, `Resolved(300)`:
   markdown contains `**[unresolved: 999-nope.md#sec-1]**`, `Defects` is
   non-empty, and the rest of the document still rendered (§4: report,
   never drop; print what could be computed).

- [ ] **Step 1:** Write the subtests — red.
- [ ] **Step 2:** Implement.
- [ ] **Step 3:** `go test ./internal/designdoc -run TestResolvedRendering -v` — PASS; full package green.
- [ ] **Step 4: Commit.**

### Task 5 — `--section` slice and forward-resolution

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

Implement `ResolveOpts.Section` in `resolved.go`, plus the raw
(non-`--resolved`) section slice the command needs:

```go
// SectionSlice returns the raw source of the section named by anchor and
// its subtree — cat semantics, no consolidation, no forward-resolution
// (decision 9). An unknown anchor is an error naming the document.
func SectionSlice(doc *Doc, anchor string) (string, error)
```

`Resolved` with `Section` set: header banner, then — decision 9 — if the
anchor has ≥1 *effective* replacement, for each in decision-4 order emit
`**[forward-resolved: <old> → <new>]**` and consolidate the replacement;
otherwise consolidate the named section itself. Pending-only replacement
means no forwarding (the anchor still states the design, §3.1).

Tests — subtests of `TestSectionMode` over `base`:

1. **Raw slice** — `SectionSlice(100, "sec-2")` returns heading + `Alpha
   body two.` and nothing of 110/120 even though the section is replaced;
   `SectionSlice(100, "sec-99")` errors.
2. **Resolved slice** — `Resolved(100, {Section: "sec-1"})` contains the
   full chain of task 3's test 1 and none of `sec-2`…`sec-6`'s bodies (§3:
   "an agent that needs 004 §5.4 should not pay for 011").
3. **Forward-resolution** — `Resolved(100, {Section: "sec-2"})`: contains
   `**[forward-resolved: 100#sec-2 → 110#sec-2]**` + 110#sec-2's
   consolidation and `**[forward-resolved: 100#sec-2 → 120#sec-2]**` +
   120#sec-2's, in that order (§8's forward-resolution bullet; a split
   forwards to both).
4. **Pending does not forward** — `Resolved(100, {Section: "sec-5",
   WithDrafts: false})` renders `Alpha body five.` with the pending marker,
   no forward-resolution note; with `WithDrafts: true` it forwards to
   130#sec-1.

- [ ] **Step 1:** Write the subtests — red.
- [ ] **Step 2:** Implement `SectionSlice` and the `Section` branch of `Resolved`.
- [ ] **Step 3:** `go test ./internal/designdoc -run TestSectionMode -v` — PASS; full package green.
- [ ] **Step 4: Commit.**

### Task 6 — Root-independence, fixture and real corpus

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [4]
```

§3.2's correctness property, made the test §8 demands. Read literally
("the consolidations of 006 and of 014 contain the same live bodies") it
is unimplementable — sections of each document untouched by any edge are
necessarily root-local — so this task pins the defensible reading, which
decision 7's backfill makes true: **restricted to the sections connected
to both documents by effective section-scoped edges, the two renderings
surface the same live sections**, where *surface* means present in
`Rendering.Surfaced` (emitted or back-referenced — body-once moves a body,
it never loses one; comparing markdown strings would wrongly fail on
narrative order, which is exactly what §3.2 says may differ).

Helpers to write in `resolved_test.go` (test-only, not exported):

```go
// liveIn reports whether sec has no effective replace edge onto it.
func liveIn(idx *edgeIndex, secID string) bool
// component returns every section reachable from any of doc's sections
// over effective section-scoped edges, either direction.
func component(idx *edgeIndex, doc *Doc) map[string]bool
```

Tests:

1. **Fixture** — `TestRootIndependenceFixture`: for each pair of roots
   drawn from {100, 110, 120, 145, 140}, render both; for every secID in
   `component(idx, a) ∩ component(idx, b)` with `liveIn(...)`, assert it
   is in both renderings' `Surfaced`. The chain, the split and the diamond
   all cross document boundaries, so this is not vacuous: e.g. `Gamma body
   two.`'s section must surface in `Resolved(110)` (via backfill of
   100#sec-2 and its other replacement) and 100#sec-1 must surface in
   `Resolved(145)`.
2. **Real corpus** — `TestRootIndependence006And014`: load the repo's own
   `docs/` tree (reuse part 1's real-corpus loading pattern from its
   zero-dangling-references test; reconcile the path helper), build the
   index with `withDrafts: true` (014 is `status: draft` — with default
   effectiveness its claims are all pending and the test would be
   vacuous), render 006 and 014 with `WithDrafts: true`, and run the same
   component-scoped assertion. This is live data: 014 replaces 006
   #sec-1, #sec-1.6, #sec-7, #sec-11 and amends half a dozen more, and 025
   acts on 014 in turn — three documents deep, both edge kinds, both
   mirror directions.

- [ ] **Step 1:** Write both tests — red where task-3/4 behaviour is incomplete, or green immediately if decision 7 was implemented faithfully; either way run them: `go test ./internal/designdoc -run TestRootIndependence -v`.
- [ ] **Step 2:** Fix any divergence **in the renderer, not the test** — the property is the spec's, the test is its statement.
- [ ] **Step 3:** Full package: `go test ./internal/designdoc -count=1` — green.
- [ ] **Step 4: Commit.**

### Task 7 — `lode doc show --resolved` in the command

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5, 6]
```

Modify `internal/cmd/doc.go` — created by part 1; re-read it immediately
before editing (two sibling plans and another session touch this repo). If
part 1 landed a plain `show` (cat with ref resolution), extend it; if not,
add the subcommand. Either way the flags and behaviour to deliver:

- `--resolved` — call `designdoc.Resolved` and print `Rendering.Markdown`.
- `--section <anchor>` — without `--resolved`, print
  `designdoc.SectionSlice`; with it, set `ResolveOpts.Section`.
- `--with-drafts` — sets `ResolveOpts.WithDrafts` (§3.1).
- Fragment sugar (§3): a `<ref>` carrying `#sec-N` strips the fragment and
  behaves as `--section sec-N`; giving both a fragment and a conflicting
  `--section` is an error.
- Plain `show` (no flags) stays byte-exact file output — never the
  consolidation.
- Defects (`Rendering.Defects` plus the corpus's own) go through part 1's
  reporting: stderr, after the rendering is printed, exit non-zero when any
  exist (§4). Reconcile with the exact mechanism `list`/`sections` landed.
- No metrics: this is a client-side command (026 §1).

Tests, following the package's existing cobra-test pattern (see
`internal/cmd/task_test.go` for how commands are executed in-process),
pointing the corpus at the fixture via part 1's `--docs` flag:

1. `doc show 100 --resolved --docs internal/designdoc/testdata/consolidate/base`
   → stdout begins with the consolidated-view banner; exit 0.
2. `doc show 100 --resolved --section sec-2 --docs …/base` → contains the
   forward-resolution notes; `doc show 100#sec-2 --resolved --docs …/base`
   → identical output (fragment sugar).
3. `doc show 100 --resolved --with-drafts --docs …/base` → contains
   `Draft body one.`; without the flag it contains the pending marker.
4. `doc show 200 --resolved --docs …/cycle` → stdout contains the cycle
   marker, stderr names the cycle, exit code non-zero.
5. Real-corpus smoke (026 §12 item 3): `doc show 018 --resolved` over the
   repo's `docs/` → output starts with the consolidated-view banner and
   contains a banner line naming 025's doc-wide claim marked
   `(pending, draft)` (025 is `status: draft` today; if 025 has been
   accepted by the time this runs, drop the pending suffix from the
   assertion, not the test).

- [ ] **Step 1:** Re-read `internal/cmd/doc.go` as landed; write the five tests — red.
- [ ] **Step 2:** Implement the flags.
- [ ] **Step 3:** `go test ./internal/cmd -run TestDocShow -v` and `go test ./internal/... -count=1` — green; then run `go run . doc show 018 --resolved | head -30` and eyeball the banner.
- [ ] **Step 4: Commit.**

---

## Done when

1. `lode doc show <spec> --resolved` renders §3's consolidation: chains
   fully expanded with no depth limit anywhere in the code, diamonds one
   body + back-references, splits both-shown-in-order, doc-scoped claims
   banners only, every borrowed block attributed with the grammar table's
   exact strings.
2. The `cycle` fixture renders in finite time with the `[cycle: …]` marker
   and a reported defect — while `scripts/secfrozen.py` (part 3) remains
   the place a cycle is *refused*; nothing here depends on it.
3. Draft claims are pending references by default and effective under
   `--with-drafts`; no-status and out-of-corpus claimants are effective;
   both edge directions register (one-sided fixtures prove it).
4. `--section` on a replaced anchor forward-resolves under `--resolved`,
   to every effective replacement in deterministic order.
5. `TestRootIndependence006And014` passes: 006-rooted and 014-rooted
   consolidations surface the same live sections on their shared lineages.
6. No `worklode_*` metric was added, no store or API code touched, and
   `internal/designdoc` still has no HTTP import.
