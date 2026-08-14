---
status: draft
covers:
  - docs/specs/026-design-doc-queries.md#sec-1
  - docs/specs/026-design-doc-queries.md#sec-2
  - docs/specs/026-design-doc-queries.md#sec-2.1
  - docs/specs/026-design-doc-queries.md#sec-2.2
  - docs/specs/026-design-doc-queries.md#sec-2.3
  - docs/specs/026-design-doc-queries.md#sec-4
  - docs/specs/026-design-doc-queries.md#sec-5.2
  - docs/specs/026-design-doc-queries.md#sec-8
  - docs/specs/026-design-doc-queries.md#sec-9
  - docs/specs/026-design-doc-queries.md#sec-10
requires:
  - 2026-08-03-spec-shorthand-references.md
---
# Design doc queries 1/3: corpus loading and `lode doc list`

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `internal/designdoc` learns to load the whole corpus, resolve every
reference form, and report defects; `lode doc list` (with `--kind`, `--status`,
`--needs-planning`, `--needs-execution`, `--json`) and `lode doc sections` ship
on top of it, and `scripts/currentspec.py` retires into the latter.

**Architecture:** Everything is offline file reading through `designdoc.Parse`
(which already round-trips the corpus byte for byte). `corpus.go` loads and
indexes; `check.go` reports §4 defects; `query.go` computes answers from a
`*Corpus` plus caller-supplied task states — no HTTP, no store import — and
`internal/cmd/doc.go` does the one API call (`--needs-execution`) and all
rendering. Spec 026 §1: this adds no endpoint, no background loop, no store
operation, and therefore **no `worklode_*` metrics** — do not add any.

**Series and boundaries.** This is part 1 of 3 concurrent plans for spec 026:

- **This plan** — §1, §2–§2.3, §4 (not §4.1/§4.2), §5, §6, and the §7 units
  `corpus.go`, `query.go`, `check.go`, `doc.go`.
- `2026-08-03-design-doc-queries-2-consolidated-show.md` — §3/§3.1/§3.2,
  `internal/designdoc/resolved.go`, the `lode doc show` subcommand. It consumes
  `LoadCorpus`, `Corpus.Resolve`, and `Defect` exactly as defined below — **do
  not redefine or rename them**, and do not touch `resolved.go` here.
- `2026-08-03-design-doc-queries-3-permanence-gate.md` — §4.1,
  `scripts/secfrozen.py`, its `.pre-commit-config.yaml` entry, and §6's
  paragraph about that entry. Not planned here.
- `2026-08-03-spec-shorthand-references.md` (already written) — §4.2,
  `internal/designdoc/shorthand.go` and `resolve.go` (`ParseShorthand`,
  `ErrNotShorthand`, `ResolveShorthand`, `Outcome`), `secfmt.py`'s Python half,
  the `project_key` config line. **This plan depends on its Tasks 1–3 and 6**
  (the Go grammar, tier resolution, `Frontmatter.Kind`, and
  `project_key = "WL"` in `.worklode/config.toml`). If they are not merged when
  execution starts, stop and execute that plan first.

**Tech stack:** Go 1.26, cobra, `gopkg.in/yaml.v3` (already a dependency).
Module path `github.com/sunstoneinstitute/worklode`; the binary is
`./cmd/lode`.

**Read first:**

- `docs/specs/026-design-doc-queries.md` §1, §2–§2.3, §4, §5–§8, §10
- `docs/authoring-design-docs.md` — frontmatter keys, reference forms
- `internal/designdoc/designdoc.go`, `frontmatter.go` — `Parse`, `Document`,
  `Section`, `Frontmatter`, `RefList`, `AnchorMap`
- `scripts/currentspec.py` — the output contract `CurrentSections` reproduces
- `internal/cmd/root.go` — `jsonOut`, `newAPIClientWithConfig`, `SilenceUsage`
- `internal/cli/client.go` — `findRepoConfig` (the spec-019 walk), `ListTasks`
- `internal/store/tasks.go:662-670` — `closedStates` / `closedStateSet`

**Conventions:** TDD per task; commit after every task, imperative mood, no
trailers. `go test ./internal/designdoc ./internal/cmd ./internal/cli` after
each task; `./scripts/secfmt.py -l` must stay clean. Two other agents are
working adjacent slices of this spec in this repo — re-read any shared file
(`ns/ontology.ttl`, `docs/authoring-design-docs.md`, `CLAUDE.md`,
`internal/cmd/doc.go`) immediately before editing it, and skip edits another
plan has already landed.

## File structure

| File | Responsibility |
|---|---|
| `internal/designdoc/corpus.go` (new) | `LoadCorpus`, `Corpus`, `Doc`, `Ref`, `Corpus.Resolve`, `ErrUnresolvedForeign`, project-key reading |
| `internal/designdoc/corpus_test.go` (new) | load + resolution tests over the fixture corpora |
| `internal/designdoc/check.go` (new) | `Defect`, `Corpus.Check`: reference integrity + mirror edges |
| `internal/designdoc/check_test.go` (new) | fixture defect tests + the real-`docs/` integrity test |
| `internal/designdoc/query.go` (new) | `NeedsPlanning`, `NeedsExecution`, `CurrentSections` |
| `internal/designdoc/query_test.go` (new) | the §8 query bullets; golden test for `CurrentSections` |
| `internal/designdoc/testdata/corpus/` (new) | clean golden fixture corpus |
| `internal/designdoc/testdata/badcorpus/` (new) | fixture corpus with a dangling ref, a dangling fragment, a missing mirror edge |
| `internal/designdoc/testdata/sections.golden` (new) | `CurrentSections` output for the clean fixture |
| `internal/cmd/doc.go` (new) | `lode doc`, `doc list`, `doc sections`; corpus-root discovery; table and `--json` output; defect reporting and exit codes |
| `internal/cmd/doc_test.go` (new) | command tests |
| `internal/cli/client.go` | export `FindRepoRoot` over the existing walk |
| `internal/store/tasks.go` | export `IsClosedState` |
| `scripts/currentspec.py` | **deleted** (task 7) — and only this script |
| `ns/ontology.ttl` | `wl:status` domain widens to include `wl:Plan` (task 8) |
| `docs/authoring-design-docs.md`, `CLAUDE.md` | §6's documentation edits (tasks 7 and 8) |

## The fixed contract

Plans 2 and 3 build against these signatures. They are the deliverable of
tasks 1–3 and must land exactly as written (doc comments may grow, shapes may
not change):

```go
// corpus.go
func LoadCorpus(root string) (*Corpus, error)

type Corpus struct {
	Root       string          // absolute corpus root
	ProjectKey string          // project_key from root's .worklode/config.toml; "" when absent
	Docs       map[string]*Doc // keyed by root-relative slash path, e.g. "docs/specs/025-documents-in-the-backbone.md"
	Defects    []Defect        // parse failures found during load
}

type Doc struct {
	Path string // root-relative slash path
	*designdoc.Document
}
func (d *Doc) Kind() string   // "plan" | "adr" | "spec"
func (d *Doc) Status() string // frontmatter status, "" when the document carries none
func (d *Doc) Title() string  // H1 text, "" when there is none
func (d *Doc) Anchors() []string // anchors of anchored sections, document order
func (d *Doc) HasAnchor(frag string) bool

type Ref struct {
	Path string // root-relative target path
	Frag string // "sec-2.1", "" when the whole document is meant
}
// Resolve resolves ref as written in the document at from (root-relative),
// per 026 §4 and §4.2. An error matching ErrUnresolvedForeign is §4.2 tier 3
// (reported, never fatal); any other non-nil error is a §4 defect.
func (c *Corpus) Resolve(from, ref string) (Ref, error)

var ErrUnresolvedForeign = errors.New("unresolved: project not known here")

// check.go
type Defect struct {
	File  string // referring file, root-relative
	Key   string // frontmatter key, e.g. `implements`, `amends["#sec-2"]`
	Ref   string // the reference as written
	Msg   string
	Fatal bool // false only for tier-3 unresolved foreign references
}
func (d Defect) String() string // "<file>: <key>: <ref>: <msg>"
func (c *Corpus) Check() []Defect

// query.go
type PlanningGap struct {
	Path      string   // the spec
	Total     int      // its anchored sections
	Unplanned []string // anchors in no plan's implements union
}
func NeedsPlanning(c *Corpus) []PlanningGap

type ExecutionGap struct {
	Path      string
	Task      string // frontmatter task id, "" when absent
	TaskState string // "" when Task is ""; "unknown" when absent from taskStates
}
func NeedsExecution(c *Corpus, taskStates map[string]string, closed func(string) bool) []ExecutionGap

// Effective is the §3.1 gate, exported because plan 2's renderer applies
// the same rule: a claim from srcPath is in force when the source is
// accepted or superseded, carries no status, or lies outside the corpus;
// a draft's claim counts only under withDrafts.
func (c *Corpus) Effective(srcPath string, withDrafts bool) bool

func CurrentSections(c *Corpus, withDrafts, showDropped bool) string
```

## Semantics decisions (read before implementing)

Points the spec leaves open, decided here so implementation never guesses:

1. **Corpus root.** §1: the root is the directory holding
   `.worklode/config.toml` (found with the walk `internal/cli.findRepoConfig`
   already implements), with the corpus at `<root>/docs/specs` and
   `<root>/docs/plans`. `--docs <dir>` overrides the **root** itself — the
   layout under it stays `docs/specs` + `docs/plans` (§1: no repo needs
   anything else today).
2. **`implements` is not status-gated.** §2.1 says "any plan's `implements`"
   with no status qualifier, and a draft plan is planning activity, not a
   planning gap. The §3.1 effectiveness gate applies to `amends`/`replaces`
   (in `CurrentSections` here, and in plan 2's renderer) — not to
   `implements`.
3. **`NeedsPlanning` covers `kind == "spec"` only.** §2.1 says "spec"
   throughout; ADRs record decisions and mint no plans, and plans claim no
   sections. There is no ADR in the corpus today, so nothing changes either
   way.
4. **014's `rdf-registry:ADR-0006` is tier-3 unresolved, not fatal.** §4.2
   calls it "a reported defect", but §10's criterion 1 requires exit 0 on the
   current tree, and criterion 8 establishes that an unresolvable foreign
   reference costs stderr, never the exit code. Rule: a reference whose first
   path segment contains `:` is foreign-corpus-shaped → non-fatal, message
   `legacy colon form; unresolved: project <name> not known here`. Flagged for
   spec review; if 026's author disagrees, only the `Fatal` bit changes.
5. **A `task` the server does not know** is listed by `--needs-execution` with
   state `unknown` plus a stderr note — an unknown state is exactly what the
   caller is asking about, and hiding the row would understate the answer.
6. **Composing filters** is AND. Exactly two combinations are errors:
   `--needs-planning` or `--needs-execution` with a conflicting `--status`
   (§2.1: an empty result would read as "nothing to plan"), and
   `--needs-planning --needs-execution` together (they select disjoint kinds,
   so AND is always empty — the same silent-lie trap).
7. **`lode doc sections` drops `currentspec.py`'s positional argument.** §2.3
   defines only `[--with-drafts] [--show-dropped]`; the single-document view
   is §3's `lode doc show` (plan 2). The generator comment line in the output
   names `lode doc sections` instead of the deleted script; everything else is
   byte-identical (task 7 proves it).

## Tasks

### Task 1 — Fixture corpora and `LoadCorpus`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:** create `internal/designdoc/testdata/corpus/**`,
`internal/designdoc/testdata/badcorpus/**`, `internal/designdoc/corpus.go`,
`internal/designdoc/corpus_test.go`.

- [ ] **Step 1: Write the clean fixture corpus** (§8 bullet 1: all four
  reference forms, section and whole-document `implements`, an amendment, a
  supersession). Create exactly these files:

`internal/designdoc/testdata/corpus/.worklode/config.toml`:

```toml
current_project = "fixture"
project_key = "WL"
```

`.../corpus/docs/specs/001-alpha.md`:

```markdown
---
status: accepted
issued: 2026-01-01
amendedBy:
  "#sec-2":
    - 002-beta.md#sec-1
isReplacedBy:
  "#sec-3":
    - 003-gamma.md#sec-1
---
# Spec 001 — Alpha

## 1. Scope {#sec-1}

Alpha scope.

## 2. Model {#sec-2}

Alpha model.

## 3. Legacy path {#sec-3}

Alpha legacy.

## 3a. Added later {#sec-3a}

Added after acceptance.
```

`.../corpus/docs/specs/002-beta.md` — bare-filename reference form:

```markdown
---
status: accepted
issued: 2026-01-02
requires:
  - 001-alpha.md
amends:
  "#sec-1":
    - 001-alpha.md#sec-2
---
# Spec 002 — Beta

## 1. Amendment {#sec-1}

Beta's amendment of alpha §2.
```

`.../corpus/docs/specs/003-gamma.md` — `./` reference form; a **draft**
whose `replaces` must stay pending:

```markdown
---
status: draft
issued: 2026-01-03
requires:
  - ./002-beta.md
replaces:
  "#sec-1":
    - 001-alpha.md#sec-3
---
# Spec 003 — Gamma

## 1. Replacement {#sec-1}

Gamma's replacement of alpha §3.
```

`.../corpus/docs/specs/004-delta.md` — whole-document supersession:

```markdown
---
status: accepted
issued: 2026-01-04
replaces:
  ".":
    - 005-epsilon.md
---
# Spec 004 — Delta

## 1. First {#sec-1}

Delta first.

## 2. Second {#sec-2}

Delta second.
```

`.../corpus/docs/specs/005-epsilon.md`:

```markdown
---
status: superseded
issued: 2026-01-05
isReplacedBy:
  ".":
    - 004-delta.md
---
# Spec 005 — Epsilon

## 1. Old {#sec-1}

Epsilon old.
```

`.../corpus/docs/specs/006-zeta.md` — shorthand, leading-`/`, and foreign
(tier-3) forms:

```markdown
---
status: accepted
issued: 2026-01-06
requires:
  - WL-SPEC-1
  - /docs/specs/002-beta.md
  - CMS-SPEC-4
---
# Spec 006 — Zeta

## 1. Zeta {#sec-1}

Zeta body.
```

`.../corpus/docs/specs/007-notes.md` — the one ADR (`Frontmatter.Kind` comes
from the shorthand plan's Task 3):

```markdown
---
kind: adr
status: accepted
issued: 2026-01-07
---
# ADR 007 — Notes

## 1. Decision {#sec-1}

We decided.
```

`.../corpus/docs/plans/2026-01-10-alpha-plan.md` — whole-document claim,
scalar `implements`, no `status` (legacy shape):

```markdown
---
implements: docs/specs/001-alpha.md
task: WL-1
---
# Alpha plan

Covers alpha wholesale.
```

`.../corpus/docs/plans/2026-01-11-beta-plan.md` — section-scoped claim via
`../`:

```markdown
---
status: accepted
covers:
  - ../specs/002-beta.md#sec-1
task: WL-2
---
# Beta plan

Covers beta §1 only.
```

`.../corpus/docs/plans/2026-01-12-gamma-plan.md` — accepted, no `task`:

```markdown
---
status: accepted
covers:
  - /docs/specs/003-gamma.md
---
# Gamma plan

No task yet.
```

- [ ] **Step 2: Write the defect fixture corpus.**

`.../badcorpus/docs/specs/001-broken.md`:

```markdown
---
status: accepted
issued: 2026-01-01
requires:
  - 999-missing.md
  - 002-target.md#sec-9
amends:
  "#sec-1":
    - 002-target.md#sec-1
---
# Spec 001 — Broken

## 1. One {#sec-1}

Body.
```

`.../badcorpus/docs/specs/002-target.md` — deliberately **no** `amendedBy`
mirror:

```markdown
---
status: accepted
issued: 2026-01-02
---
# Spec 002 — Target

## 1. One {#sec-1}

Body.
```

Also create empty-but-present `.../badcorpus/docs/plans/` (a `.gitkeep` is
fine) so the loader's walk sees both directories.

- [ ] **Step 3: Write the failing tests** in `corpus_test.go` (fixture root:
  `filepath.Join("testdata", "corpus")`):

```go
func TestLoadCorpus(t *testing.T) {
	c, err := LoadCorpus(filepath.Join("testdata", "corpus"))
	if err != nil { t.Fatal(err) }
	if c.ProjectKey != "WL" { t.Fatalf("ProjectKey = %q", c.ProjectKey) }
	if len(c.Defects) != 0 { t.Fatalf("load defects: %v", c.Defects) }
	if got := len(c.Docs); got != 10 { t.Fatalf("docs = %d", got) }
	d := c.Docs["docs/specs/001-alpha.md"]
	if d == nil || d.Kind() != "spec" || d.Status() != "accepted" ||
		d.Title() != "Spec 001 — Alpha" {
		t.Fatalf("alpha: %+v", d)
	}
	if got := d.Anchors(); !reflect.DeepEqual(got, []string{"sec-1", "sec-2", "sec-3", "sec-3a"}) {
		t.Fatalf("anchors: %v", got)
	}
	if k := c.Docs["docs/specs/007-notes.md"].Kind(); k != "adr" { t.Fatalf("kind = %q", k) }
	if k := c.Docs["docs/plans/2026-01-11-beta-plan.md"].Kind(); k != "plan" { t.Fatalf("kind = %q", k) }
}
```

Plus: `TestLoadCorpusRealTree` — `LoadCorpus("../..")` succeeds with zero
`Defects` (every real document parses); `TestLoadCorpusSkipsIndexYaml` — no
`Docs` key ends in `index.yaml`; `TestLoadCorpusNoProjectKey` — loading
`testdata/badcorpus` (which has no `.worklode/`) yields `ProjectKey == ""`
and still works.

- [ ] **Step 4: Run tests, confirm they fail to compile** (red).

- [ ] **Step 5: Implement `corpus.go`**: the `Corpus`/`Doc` types and
  `LoadCorpus` from the contract section. Implementation notes:

  - Walk `<root>/docs/specs` and `<root>/docs/plans` with `os.ReadDir`
    (both flat), taking only `*.md`. A missing `docs/plans` is fine (the
    badcorpus case needs it present only because git can't track empty dirs);
    a root with **neither** directory is an error naming the root — that is
    a wrong `--docs` value, not an empty corpus.
  - Keys are always slash paths relative to root (`filepath.ToSlash`).
  - A file `designdoc.Parse` rejects becomes
    `Defect{File: path, Msg: err.Error(), Fatal: true}` in `c.Defects` and is
    excluded from `Docs` — a query never quietly lies (§0), but one broken
    file must not take every query down.
  - `ProjectKey`: read `<root>/.worklode/config.toml` (then
    `<root>/.lode/config.toml`) with the same line-matching style as
    `cli.parseConfig` — match `project_key = "..."`, ignore everything else;
    do **not** import `internal/cli` (designdoc stays dependency-light) and do
    not add a TOML parser.
  - `Doc.Kind()`: `"plan"` when `Path` starts `docs/plans/`; else
    `Frontmatter.Kind` when `"adr"`; else `"spec"` (026 §4.2's rule).
  - `Doc.Status()`: `Frontmatter.Status`, `""` when `Frontmatter` is nil.
  - `Doc.Title()`: first line of `Preamble` that starts `# `, prefix
    stripped.
  - `Defect` and its `String()` live in `check.go` (created in this task with
    just the type; `Check` follows in task 3).

- [ ] **Step 6: `go test ./internal/designdoc/ -count=1`** — new tests green,
  and the pre-existing `TestRoundTripCorpus` still green.

- [ ] **Step 7: Commit** — `feat(designdoc): load the document corpus`.

### Task 2 — `Corpus.Resolve`: the four reference forms

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:** modify `internal/designdoc/corpus.go`,
`internal/designdoc/corpus_test.go`.

- [ ] **Step 1: Write the failing tests.** Table-driven over the clean fixture
  (`from` is the referring document, per §4 the resolution base):

| from | ref | want |
|---|---|---|
| `docs/specs/002-beta.md` | `001-alpha.md` | `{docs/specs/001-alpha.md, ""}` — no `/`: referrer's directory |
| `docs/specs/003-gamma.md` | `./002-beta.md` | `{docs/specs/002-beta.md, ""}` |
| `docs/plans/2026-01-11-beta-plan.md` | `../specs/002-beta.md#sec-1` | `{docs/specs/002-beta.md, "sec-1"}` |
| `docs/plans/2026-01-10-alpha-plan.md` | `docs/specs/001-alpha.md` | `{docs/specs/001-alpha.md, ""}` — `/` present: repo-relative |
| `docs/specs/006-zeta.md` | `/docs/specs/002-beta.md` | `{docs/specs/002-beta.md, ""}` — leading `/` optional |
| `docs/specs/006-zeta.md` | `WL-SPEC-1` | `{docs/specs/001-alpha.md, ""}` — shorthand tier 1 |
| `docs/specs/006-zeta.md` | `CMS-SPEC-4` | error matching `ErrUnresolvedForeign` |
| `docs/specs/006-zeta.md` | `WL-SPEC-999` | error, **not** `ErrUnresolvedForeign` (tier-1 miss is a defect) |
| `docs/specs/002-beta.md` | `999-missing.md` | error: no such document |
| `docs/specs/002-beta.md` | `001-alpha.md#sec-99` | error: no such anchor |
| `docs/specs/002-beta.md` | `rdf-registry:ADR-0006` | error matching `ErrUnresolvedForeign` (decision 4) |

Include the §8 bullet verbatim: from `docs/plans/2026-01-11-beta-plan.md`,
both `docs/specs/002-beta.md#sec-1` (repo-relative) and
`../specs/002-beta.md#sec-1` resolve to the same `Ref` — same target, same
referring file, two forms.

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement `Resolve`** in `corpus.go`:

  1. Try `ParseShorthand(ref')` where `ref'` is `ref` with any `#frag` still
     attached (the shorthand grammar carries its own fragment). On success,
     call `ResolveShorthand(c.Root, c.ProjectKey, sh)`; map `Resolved` to a
     `Ref`, `Unresolved` to an error wrapping `ErrUnresolvedForeign`, `Defect`
     to a plain error carrying the resolver's message. On `ErrNotShorthand`,
     fall through to path resolution.
  2. Split `ref` at `#` into path and fragment.
  3. If the first path segment contains `:` → `ErrUnresolvedForeign` with the
     legacy-colon-form message (decision 4).
  4. Resolve the path per §4's table: no `/` → `path.Join(path.Dir(from), p)`;
     leading `./` or `../` → the same join (`path.Join` cleans both); any
     other `/`-containing path → root-relative, with a leading `/` stripped.
     Use the `path` package throughout — corpus paths are slash paths.
  5. The result must be a key in `c.Docs`, else "no such document". A
     non-empty fragment must satisfy `HasAnchor`, else "no such anchor"
     naming the target (§4: a fragment must exist in the target's source).

- [ ] **Step 4: `go test ./internal/designdoc/ -run 'Resolve|Corpus' -v`** —
  green.

- [ ] **Step 5: Commit** — `feat(designdoc): resolve document references`.

### Task 3 — `Corpus.Check` and the real-corpus integrity test

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:** modify `internal/designdoc/check.go`; create
`internal/designdoc/check_test.go`; possibly repair `docs/**` frontmatter
(mirror edges only — see step 5).

- [ ] **Step 1: Write the failing tests.**

Over `testdata/badcorpus`, `Check` returns exactly three fatal defects:

- `001-broken.md` / `requires` / `999-missing.md` — no such document;
- `001-broken.md` / `requires` / `002-target.md#sec-9` — no such anchor;
- `001-broken.md` / `amends["#sec-1"]` / `002-target.md#sec-1` — mirror edge
  missing: target carries no matching `amendedBy` (§4: reported, and it must
  **not** change any query's answer).

Over `testdata/corpus`, `Check` returns exactly one defect —
`006-zeta.md` / `requires` / `CMS-SPEC-4`, with `Fatal == false` — and
nothing else.

Then the §8 bullet that is the point of the task:

```go
// TestRealCorpusIntegrity fails the build when anyone lands a dangling
// reference or a missing mirror edge in docs/ — the check secfmt.py never
// had (026 §4, §8).
func TestRealCorpusIntegrity(t *testing.T) {
	c, err := LoadCorpus("../..")
	if err != nil { t.Fatal(err) }
	// The one tolerated non-fatal defect: 014's legacy colon-form
	// cross-project reference (026 §4.2). It becomes a real shorthand when
	// rdf-registry gets a project key.
	allowed := "rdf-registry:ADR-0006"
	for _, d := range c.Check() {
		if !d.Fatal && d.Ref == allowed { continue }
		t.Errorf("corpus defect: %s", d)
	}
}
```

- [ ] **Step 2: Run, confirm red** (Check does not exist).

- [ ] **Step 3: Implement `Check`.** For every doc, resolve every reference in
  every reference-bearing frontmatter field — `Implements`, `Requires`,
  `IsRequiredBy`, `WasDerivedFrom`, and every value of `Amends`, `AmendedBy`,
  `Replaces`, `IsReplacedBy` — via `Corpus.Resolve`, recording a `Defect` per
  failure (`Fatal` from `errors.Is(err, ErrUnresolvedForeign)`; the `Key` for
  map fields is `amends["#sec-2"]`-style). Also validate each `AnchorMap`
  subject key: `"."` or `#<anchor>` where the anchor exists in the *own*
  document. Then mirror edges: for each resolvable `amends` entry A→B, some
  `amendedBy` entry in B must resolve back to A's document (fragment-level
  match on the acting side is not required — §3.1's union works from either
  side; the defect is the edge being entirely absent from the mirror).
  Same for `replaces`/`isReplacedBy`, both directions. Deterministic output
  order: sort by (File, Key, Ref). Do not report a mirror-edge defect twice
  for one logical edge.

- [ ] **Step 4: Run the tests.** `TestRealCorpusIntegrity` may now fail on
  genuine corpus defects nobody has ever checked for.

- [ ] **Step 5: Repair the real corpus, narrowly.** For each real defect
  found: a missing mirror edge gets the mechanical mirror entry added to the
  target document's frontmatter (three-line YAML edit, per
  `docs/authoring-design-docs.md`); run `./scripts/secfmt.py -l` after. A
  dangling reference or anchor is **not** mechanically repairable — if one
  exists, stop and escalate to the human rather than guessing the intended
  target. Do not touch section bodies, and never renumber anything.

- [ ] **Step 6: `go test ./internal/designdoc/ -count=1`** — all green,
  including the real-corpus test.

- [ ] **Step 7: Commit** — `feat(designdoc): corpus integrity checking` (the
  corpus repairs ride in the same commit; the test pins them).

### Task 4 — `NeedsPlanning` and `NeedsExecution`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:** create `internal/designdoc/query.go`,
`internal/designdoc/query_test.go`.

- [ ] **Step 1: Write the failing tests**, §8's bullets exactly, over the
  clean fixture:

`NeedsPlanning`:

- returns exactly `[{docs/specs/004-delta.md, 2, [sec-1 sec-2]}, {docs/specs/006-zeta.md, 1, [sec-1]}]`, sorted by path. This proves in one shot: a
  whole-document claim covers a later-added section (001's `sec-3a` postdates
  the alpha plan yet 001 is absent); a section claim covers only its own
  (beta's `#sec-1` claim covers 002 fully because 002 has one section); a
  spec with no plan lists every section (004, 006); a draft spec never
  appears (003, despite gamma-plan claiming it); a superseded spec never
  appears (005); an ADR never appears (007, unclaimed).
- a plan with no `status` still covers (alpha-plan is status-less; 001 is
  absent) — decision 2.

`NeedsExecution` with `closed := func(s string) bool { return s == "merged" }`:

- states `{"WL-1": "in_progress", "WL-2": "in_progress"}` → exactly
  `[{…beta-plan.md, WL-2, in_progress}, {…gamma-plan.md, "", ""}]` sorted by
  path: no `task` → listed; open task → listed; no `status` → absent
  regardless of `task` (alpha-plan).
- states `{"WL-2": "merged"}` → only gamma-plan: closed task → absent.
- states `{}` → beta-plan listed with `TaskState == "unknown"` (decision 5).

- [ ] **Step 2: Run, confirm red.**

- [ ] **Step 3: Implement.** `NeedsPlanning`: build the union of claimed
  `(path, anchor)` pairs from every plan's `Implements` (resolving each via
  `Corpus.Resolve`; an unresolvable claim is skipped here — `Check` already
  reports it, and §4 wants it *reported*, not silently repaired into
  coverage). A whole-document `Ref` (`Frag == ""`) covers every anchor the
  target has, present and future, so record it as a document-level cover.
  Then for each doc with `Kind() == "spec"` and `Status() == "accepted"`,
  `Unplanned` = its `Anchors()` minus the union; include the doc when
  `len(Unplanned) > 0`. `NeedsExecution`: docs with `Kind() == "plan"` and
  `Status() == "accepted"`; gap when `Frontmatter.Task == ""`, or the state
  (`taskStates[task]`, `"unknown"` when absent) is not `closed`. Both sorted
  by path. Neither function performs I/O beyond the corpus it is handed.

- [ ] **Step 4: `go test ./internal/designdoc/ -run Needs -v`** — green.

- [ ] **Step 5: Commit** — `feat(designdoc): needs-planning and
  needs-execution queries`.

### Task 5 — `CurrentSections`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

**Files:** modify `internal/designdoc/query.go`, `query_test.go`; create
`internal/designdoc/testdata/sections.golden`.

- [ ] **Step 1: Write the golden file** — `currentspec.py`'s exact output
  contract over the clean fixture (decision 7: only the generator line
  differs). Format rules, ported from the script: docs sorted by path; each
  block is a blank line, `<path>  (<status>[; <doc-level notes>])`, then per
  anchored section two spaces + anchor left-justified to 12 columns + one
  space + heading text (number rendered `N.` at H2, `N.M` deeper, then the
  title; no anchor braces) + optional `   [note; note]`. Content of
  `testdata/sections.golden`, byte for byte:

```
# Current spec sections -- 10 sections across 6 documents
# generated by lode doc sections; superseded material is listed at the end

docs/specs/001-alpha.md  (accepted)
  sec-1        1. Scope
  sec-2        2. Model   [amended by 002-beta.md#sec-1]
  sec-3        3. Legacy path   [pending 003-gamma.md#sec-1]
  sec-3a       3a. Added later

docs/specs/002-beta.md  (accepted)
  sec-1        1. Amendment

docs/specs/003-gamma.md  (draft)
  sec-1        1. Replacement

docs/specs/004-delta.md  (accepted)
  sec-1        1. First
  sec-2        2. Second

docs/specs/006-zeta.md  (accepted)
  sec-1        1. Zeta

docs/specs/007-notes.md  (accepted)
  sec-1        1. Decision

# Superseded documents
  005-epsilon.md  ->  004-delta.md
```

(File ends with a trailing newline. If the implementation and this golden
disagree on whitespace, the authority is `scripts/currentspec.py`'s format
strings — `f"  {anchor:<12} {text}{note}"`, notes prefixed with three spaces
— and the golden is corrected to match them, because task 7's real-corpus
diff against the script is the binding check.)

- [ ] **Step 2: Write the failing tests.**

- `CurrentSections(c, false, false)` over the clean fixture equals the golden
  byte for byte (on mismatch, print a unified diff or both strings — this
  test is the format contract).
- `--with-drafts` semantics (§3.1 gate, §8 bullet): with `withDrafts ==
  true`, 001's `sec-3` line is gone, and the output ends with a
  `# Replaced sections (1), omitted above` footer; with `showDropped` also
  true, the footer is followed by
  `  001-alpha.md#sec-3  ->  003-gamma.md#sec-1`. With `withDrafts == false`
  the section is present and marked `pending` (already pinned by the golden).
- Effectiveness gate corners: 004→005 whole-document replacement is effective
  (004 is accepted), so 005 is in the superseded footer, not the body —
  pinned by the golden; a claim from a status-less document is effective
  (assert by flipping: parse a copy of the corpus where 004's frontmatter has
  no `status` — build it under `t.TempDir()` by copying the fixture and
  deleting the line — and 005 still lands in the footer).

- [ ] **Step 3: Run, confirm red.**

- [ ] **Step 4: Implement `CurrentSections`** as a port of
  `currentspec.py:main` (read it side by side):

  - Only docs with `Kind() != "plan"` participate (the script reads
    `docs/specs/index.yaml`; the corpus is the index now).
  - Build the two edge maps exactly as `edges()` does — union `replaces` with
    `isReplacedBy` and `amends` with `amendedBy`, resolving each ref through
    `Corpus.Resolve` (an unresolvable ref contributes no edge; `Check`
    reports it) — keyed by `(target path, anchor-or-empty)`, values the set
    of `(source path, source anchor)`.
  - The gate is the exported `Corpus.Effective(srcPath, withDrafts)` from
    the contract section — source outside the corpus → true (trusted,
    §3.1); status `accepted`/`superseded` → true; `draft` → `withDrafts`;
    no status → true. Plan 2's renderer calls the same function; keep it a
    pure corpus lookup.
  - Whole-document drop: own status `superseded`, or an effective
    doc-level replacement → the superseded footer with its successors.
  - Section drop: an effective replacement targeting its anchor → counted,
    listed only under `showDropped`.
  - Notes: `amended by <name>` for every amend source (effective or not —
    the script annotates amendments unconditionally), `pending <name>` for
    each non-effective replacement source. `<name>` is basename plus
    `#anchor` when the source is section-scoped. Sorted as the script sorts
    (`(path, anchor-or-"")`).
  - Header text as in the golden; counts are kept sections and kept
    documents. Heading text: `Number + ". " + Title` at level 2,
    `Number + " " + Title` deeper, `Title` alone when unnumbered; skip
    sections with no anchor.

- [ ] **Step 5: `go test ./internal/designdoc/ -count=1`** — green.

- [ ] **Step 6: Commit** — `feat(designdoc): current-sections corpus view`.

### Task 6 — `lode doc list`

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3, 4]
```

**Files:** create `internal/cmd/doc.go`, `internal/cmd/doc_test.go`; modify
`internal/cli/client.go` (export `FindRepoRoot`), `internal/store/tasks.go`
(export `IsClosedState`).

- [ ] **Step 1: The two tiny exports, with tests.**

`internal/cli/client.go` (test in `internal/cli/client_test.go`, next to the
existing `findRepoConfig` tests):

```go
// FindRepoRoot walks up from startDir for a repo-local .worklode (or .lode)
// config and returns the directory holding it — the corpus root of spec
// 026 §1. Same walk, same $HOME stop, as findRepoConfig.
func FindRepoRoot(startDir string) (string, bool) {
	p, ok := findRepoConfig(startDir)
	if !ok {
		return "", false
	}
	return filepath.Dir(filepath.Dir(p)), true
}
```

`internal/store/tasks.go` (beside `closedStateSet`; the existing
`TestClosedStateSetMirrorsSQL` keeps both lists honest, so the new function
needs only a one-line test that `IsClosedState("merged") &&
!IsClosedState("in_progress")`):

```go
// IsClosedState reports whether state no longer blocks dependents —
// closedStates as a predicate, for callers outside the store (026 §2.2).
func IsClosedState(state string) bool { return closedStateSet[state] }
```

- [ ] **Step 2: Write the failing command tests** in `doc_test.go`. Drive the
  commands the way the existing `internal/cmd` tests do (build the command,
  `SetArgs`, `SetOut`/`SetErr` to buffers, `Execute`), always passing
  `--docs ../designdoc/testdata/corpus` (and `badcorpus` where named):

- `doc list`: 10 rows sorted by path; the alpha-plan row (the one status-less
  document) shows `—` in the status column, every other row its status (§2:
  how a pre-§5 plan stays visible without a separate selector).
- `doc list --kind adr`: exactly the 007 row. `--status draft`: exactly 003.
  `--kind plan --status accepted`: beta-plan and gamma-plan.
- `doc list --needs-planning`: two rows,
  `docs/specs/004-delta.md … 2/2 unplanned sec-1 sec-2` and
  `docs/specs/006-zeta.md … 1/1 unplanned sec-1`.
- `doc list --needs-planning --status draft`: error mentioning the conflict;
  `--needs-planning --status accepted`: not an error. `--needs-planning
  --needs-execution`: error (decision 6).
- `doc list --needs-execution` against an `httptest.NewServer` faking
  `GET /api/v1/tasks` with `{"tasks":[{"id":"WL-2","state":"in_progress"}]}`
  (point the client at it with `t.Setenv("LODE_SERVER", srv.URL)` and
  `t.Setenv("LODE_TOKEN", "wl_"+40 hex chars)` — `LODE_TOKEN` short-circuits
  the keychain): beta-plan (`in_progress`) and gamma-plan (`—`) rows. With
  the server closed: the command fails before printing rows (§2.2: fail, not
  degrade).
- `doc list --json`: output unmarshals into `[]map[string]any`; rows carry
  `kind`, `path`, `status`, `title`; under `--needs-planning` also `total`
  and `unplanned`; under `--needs-execution` also `task`, `task_state`.
- Defect reporting: `doc list --docs ../designdoc/testdata/badcorpus` prints
  its rows to stdout, three defect lines to **stderr**, and returns an error
  (non-zero exit) naming the defect count; over the clean corpus the
  `CMS-SPEC-4` line goes to stderr as `unresolved` and the command still
  succeeds (§4, §4.2 tier 3).
- No `--docs` and no `.worklode` above the cwd: error telling the user about
  both the walk and the flag.

- [ ] **Step 3: Run, confirm red.**

- [ ] **Step 4: Implement `doc.go`.** Shape:

```go
func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "Query the design-document corpus (offline; reads docs/ via the repo's .worklode root)",
	}
	cmd.PersistentFlags().String("docs", "",
		"corpus root: the directory holding docs/specs and docs/plans (default: found via .worklode/config.toml)")
	cmd.AddCommand(newDocListCmd()) // task 7 appends newDocSectionsCmd(); plan 2 appends newDocShowCmd()
	return cmd
}

func init() { rootCmd.AddCommand(newDocCmd()) }
```

- `loadDocCorpus(cmd)` helper: `--docs` value, else
  `cli.FindRepoRoot(os.Getwd())`, else the two-option error; then
  `designdoc.LoadCorpus`. No API client, no server config — every subcommand
  except `--needs-execution` must work with no server configured at all
  (§10 criterion 6).
- `reportDefects(cmd, defects []designdoc.Defect) error`: print every defect
  to `cmd.ErrOrStderr()` (prefix `unresolved: ` when `!Fatal`); return
  `fmt.Errorf("%d corpus defect(s)", n)` when any are fatal, nil otherwise.
  **Always called after the results are printed** — §4: report after
  printing what could be computed. Defects are `append(c.Defects,
  c.Check()...)`.
- `doc list` flag handling per decision 6; validate `--kind` against
  `spec|adr|plan` and `--status` against `draft|accepted|superseded`,
  rejecting anything else.
- Row assembly: default rows from `c.Docs` sorted by path (kind, path,
  status-or-`—`, title); `--needs-planning` rows from
  `designdoc.NeedsPlanning(c)` (path, status, `K/N unplanned`, anchors
  space-joined — §2.1's example line); `--needs-execution` rows from
  `designdoc.NeedsExecution(c, states, store.IsClosedState)` where `states`
  comes from one `ListTasks` call with a zero `TaskListFilter` (all
  projects, all states — one request, §2.2), id→state. `--kind`/`--status`
  filter whichever row set is active. Tables via `cli.newTabwriter`-style
  `text/tabwriter` with a header row, like the other list commands.
- `--json` (the root persistent flag, read with `jsonOut(cmd)`): encode the
  active row set as a JSON array with `json.Encoder` on stdout —
  lower_snake keys as in the tests. Defect reporting behaves identically in
  both modes.

- [ ] **Step 5: `go test ./internal/cmd/ ./internal/cli/ ./internal/store/
  -count=1`** — green (store tests skip without Postgres; the
  `IsClosedState` unit test must not need Postgres — keep it a pure function
  test).

- [ ] **Step 6: Smoke it on the real tree** and check §10 criterion 1:

```bash
go run ./cmd/lode doc list | head
go run ./cmd/lode doc list --needs-planning
```

`--needs-planning` must exit 0 and list exactly those accepted specs no plan's
`implements` union covers, naming the unplanned anchors (the stderr
`unresolved` line for 014's colon ref is expected). Do not hardcode the
expected document — the earlier version of this step named
`000-umbrella-architecture.md`, which has since been deleted. Derive the
expectation from the corpus, then verify it by hand. If it disagrees, stop:
either the corpus gained an accepted spec nobody planned (fine — adjust the
expectation) or the coverage arithmetic is wrong. Do not "fix" it by editing
docs.

- [ ] **Step 7: Commit** — `feat(cmd): lode doc list`.

### Task 7 — `lode doc sections`, and `currentspec.py` retires

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [5, 6]
```

**Files:** modify `internal/cmd/doc.go`, `internal/cmd/doc_test.go`,
`docs/authoring-design-docs.md`; delete `scripts/currentspec.py`.

- [ ] **Step 1: Write the failing command test**: `doc sections --docs
  ../designdoc/testdata/corpus` prints exactly the golden from task 5 (read
  the same file), plus one `unresolved` stderr line (CMS-SPEC-4), exit 0;
  `--with-drafts --show-dropped` shows the dropped-section footer; over
  `badcorpus` it prints its output and then fails with the defect count.

- [ ] **Step 2: Implement `newDocSectionsCmd`** — flags `--with-drafts`,
  `--show-dropped`; body is `loadDocCorpus`, print
  `designdoc.CurrentSections(c, withDrafts, showDropped)`, then
  `reportDefects`. `--json` is not defined for this verb (§2 defines it for
  `list` rows); it is ignored here, matching the spec's silence.

- [ ] **Step 3: The parity check** (§8: `lode doc sections` reproduces
  `scripts/currentspec.py` on the real corpus — the one-time act that lets
  the script be deleted rather than drift):

```bash
./scripts/secindex.py                      # the script reads index.yaml; make it current
./scripts/currentspec.py        > /tmp/currentspec.out
go run ./cmd/lode doc sections  > /tmp/docsections.out 2>/dev/null
diff <(sed '2d' /tmp/currentspec.out) <(sed '2d' /tmp/docsections.out)
```

The only permitted difference is line 2 (the generator comment — decision 7),
hence the `sed '2d'`. Any other diff is a bug in the Go port: fix
`CurrentSections` (and the task-5 golden if the format understanding was
wrong), never the script. Repeat with `--with-drafts` and with
`--show-dropped` on both sides. If `secindex.py` changed
`docs/specs/index.yaml`, commit that separately as the generated file it is.

- [ ] **Step 4: Delete `scripts/currentspec.py`** — and nothing else in
  `scripts/` (§10: `secfmt.py` keeps its hook, `secindex.py` stays manual).
  Then update the two places that name it:

  - `docs/authoring-design-docs.md`, section "The section index and the
    current view": the `currentspec.py` usage lines and its explanatory
    paragraph are replaced by the equivalent `lode doc sections
    [--with-drafts] [--show-dropped]` description (same semantics — pending
    claims, `--with-drafts`, supersession resolved corpus-wide) with a note
    that the single-document view moved to `lode doc show <ref>` (spec 026
    §3, shipping separately). `secindex.py`'s paragraph stays.
  - `grep -rn currentspec` across the repo must then return nothing but
    `docs/specs/026-design-doc-queries.md` and historical plans.

- [ ] **Step 5: Verify** — `go test ./internal/... -count=1`,
  `./scripts/secfmt.py -l`, and `git grep -l currentspec -- ':!docs/specs'
  ':!docs/plans'` is empty.

- [ ] **Step 6: Commit** — `feat(cmd): lode doc sections; retire
  currentspec.py`.

### Task 8 — `wl:status` widening and the §6 documentation edits

```yaml
kind: chore
priority: medium
skills: [ ]
blockedBy: [7]
```

**Files:** modify `ns/ontology.ttl`, `docs/authoring-design-docs.md`,
`CLAUDE.md`.

**Concurrency note:** the accepted plan
`2026-08-03-documents-in-the-backbone-1-kinds-and-containers.md` (Task 6)
also edits `ns/ontology.ttl` — it adds `wl:Plan` and widens `wl:status`'s
domain as part of a larger 025 edit. Re-read the file first; whichever plan
executes second skips what is already done.

- [ ] **Step 1: `ns/ontology.ttl`** (spec 026 §5.2 is the governing amendment;
  per CLAUDE.md the `ns/` mirror follows it):

  - If `wl:status`'s domain (currently
    `owl:unionOf ( wl:DesignDoc wl:Section )`, around line 252) already
    includes `wl:Plan`: nothing to do.
  - Else if `wl:Plan` is already declared (backbone plan 1 landed): add
    `wl:Plan` to the union and extend the `rdfs:comment` with one clause —
    plans carry the same value set, no per-section status (026 §5.2).
  - Else (neither landed): add the `wl:Plan` class with spec 025 §9's Turtle
    verbatim (sibling of `wl:DesignDoc`, `wl:layer wlc:execution`), update
    the "Deliberately absent — wl:Plan dropped (025 §2)" trailer comment to
    record the 025 §9 return, and widen the domain as above.
  - Validate: `riot --validate ns/ontology.ttl ns/concept.ttl ns/shapes.ttl`.

- [ ] **Step 2: `docs/authoring-design-docs.md`** (§6's list; re-read the
  file first — the shorthand plan and plan 3 also edit it):

  - Frontmatter table: `status` row's "On" column gains **plans**, with §5's
    meaning appended to the shape cell or a following sentence: a plan is
    `draft` while written and reviewed, `accepted` from the moment its
    execution is authorised; plans take no per-section status. `task` row's
    "On" becomes "plans (transitional)" and its note says: on a plan it
    names the task the plan's execution hangs off, the stand-in for 025 §9.2's
    `plan_doc` reference; neither key is backfilled (026 §5.2).
  - References section: replace the two-sentence bare-filename/repo-relative
    rule with §4's table — no `/` → the referring document's directory;
    leading `./`/`../` → the same; any other `/`-containing path →
    repo-relative, leading `/` optional — and the sentence that the rule is
    exhaustive with no legacy branch. (The shorthand subsection and its
    distance-decides-canonical-form rule already exist — leave them.)
  - Same section, one new line: **new plans must carry section-scoped
    `implements`** — a whole-document claim is a coverage assertion that can
    never go stale, and it is what makes `--needs-planning` vacuous today
    (026 §2.1).
  - "Amending a section": one added line — write an amendment as a
    self-contained, section-shaped payload, not a diff against text the
    reader cannot see, so it consolidates cleanly (026 §3.2).
  - New short section (before "Checks") titled "Querying the corpus": `lode
    doc list` (+ `--needs-planning`, `--needs-execution`) for coverage
    questions, `lode doc sections` for orientation, raw files as the
    deliberate act; note that the commands report corpus defects on stderr
    and exit non-zero on dangling references (026 §4). Do not describe
    `doc show` here — plan 2 adds it with §3. Do not touch the "Checks"
    section's `secfmt.py` content (plan 3 owns the `secfrozen.py` addition).

- [ ] **Step 3: `CLAUDE.md`** — the "Specs, plans, tasks" section gains one
  line (§6), e.g.: "`lode doc list --needs-planning|--needs-execution` and
  `lode doc sections` answer coverage and orientation questions from the
  corpus — use them instead of reading 26 specs."

- [ ] **Step 4: Verify** — `riot --validate ns/*.ttl`,
  `./scripts/secfmt.py -l`, `go test ./internal/designdoc -count=1` (the
  real-corpus test re-checks the edited docs).

- [ ] **Step 5: Commit** — `docs: plan status/task keys, reference forms,
  lode doc pointers; widen wl:status to plans`.

## Done when

Maps to spec 026 §12 (this plan's share):

1. `lode doc list --needs-planning` exits 0 and lists every accepted spec with a
   section no plan's `implements` union names, with the unplanned anchors and
   no hardcoded document; a plan carrying `implements: NO-SPEC` is absent
   from the output and adds coverage to nothing (criterion 1, 026 §4.2a).
2. `lode doc list --needs-execution` lists every `status: accepted` plan whose
   `task` is absent or open, and no status-less plan (criterion 2).
3. A dangling reference anywhere in `docs/` fails
   `go test ./internal/designdoc` (criterion 4).
4. Every query runs with no server configured except `--needs-execution`,
   which fails rather than degrades when the server is unreachable
   (criterion 6, §2.2).
5. `lode doc sections` matched `scripts/currentspec.py` on the real corpus
   (modulo the generator line) before that script — and only that script —
   was deleted (criterion 7).
6. `LoadCorpus`, `Corpus.Resolve`, `Defect`, and `Corpus.Check` exist exactly
   as the contract section states, ready for plans 2 and 3 (criterion 9's
   "the migration is swapping the loader").
7. No `worklode_*` metric was added anywhere (§1).
