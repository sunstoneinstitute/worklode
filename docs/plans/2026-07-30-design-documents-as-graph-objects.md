# Design documents as graph objects (spec 014) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build every part of spec 014 that is implementable without a graph
server: the `ls:`→`wl:` prefix rename across `docs/`, the six-kind
`tasks.kind` migration, section/anchor parsing with the §3/§7 authoring
rules and the `lode doc anchors` lint, the §7 version-diff gate, and the
`.worklode/implements.yaml` claim model with component derivation and
`wl:implements` edge projection.

**Architecture:** Everything graph-shaped lands as pure Go under
`internal/kg/`, alongside the sibling plans' packages: a new
`internal/kg/section` package parses Markdown headings with Pandoc `{#sec-…}`
attributes and enforces the immutability constraints as set diffs, and a new
`internal/kg/implements` package parses the manifest, derives the claiming
component through `internal/kg/manifest` (implicit whole-repo component when
absent), and projects claims as `internal/graphproj` triples. Publication,
versioned named graphs, coverage queries, and the `lode doc` server surfaces
stay deferred — they need the graph server (spec 009, cross-repo) and the 006
vocabulary PR, neither of which exists.

**Tech Stack:** Go 1.x, cobra CLI, PostgreSQL via `database/sql` +
golang-migrate, `gopkg.in/yaml.v3`, standard-library testing.

**Spec:** `docs/specs/014-design-documents-as-graph-objects.md`

---

## Prerequisites (sibling plans, same day)

- `docs/plans/2026-07-30-platform-graph-design.md` owns `internal/kg/iri`
  and `internal/kg/manifest`. Tasks 3, 8–10 here extend/consume those
  packages and cannot start before that plan's Tasks 1–2 have landed.
- `docs/plans/2026-07-30-runtime-layer.md` owns `internal/graphproj`
  (`Triple`, `Render`). Task 10 here consumes it and cannot start before that
  plan's Task 6 has landed.
- Tasks 1, 2, 4–7 have no cross-plan dependency; Tasks 4–7 only need Task 3.

## Already implemented vs. what remains

**Already in place — do not redo:**

- **Every 014 amendment callout.** The "Amendments to existing specs" table
  in 014 is applied as `> **Amended/Superseded by 014 …**` blocks in
  `docs/specs/000-umbrella-architecture.md:51,94`,
  `docs/specs/006-knowledge-graph.md:42,65,109,164,269,297,328,505,576,589,711,722`,
  `docs/specs/007-drift-and-overview.md:12,49,73,146,203,221,293`,
  `docs/specs/008-worklode-plugin.md:7,114,144,151,232`, and
  `docs/specs/013-reconciliation.md:22,132,170,211,236,253`. No doc-amendment
  work remains except the mechanical prefix rename (Task 2), which is what
  makes several of those callouts redundant.
- **rdf-registry side of §1.** No `rdf/ls/` directory ever existed; the
  runtime-layer plan creates `rdf/wl/` directly under the `wl:` base. Nothing
  to move.

**Not implemented — this plan's build scope:**

| Spec section | State | Task |
|---|---|---|
| §1 prefix rename in `docs/` | body text still `ls:` — ~425 occurrences in 15 files (the spec's "187 across 11" predates the newer specs) | 2 |
| §8 task kinds | `deploy/base/migrations/0001_baseline.up.sql:53` still `('feature','bug','chore','spec')`; `internal/api/tasks.go:18-20` matches | 1 |
| §3 anchors + §7 authoring rules | no code anywhere | 3–6 |
| §7 version-diff gate (local form) | no code | 7 |
| §6 `implements.yaml`, component derivation, edge projection | no code | 8–10 |

**Not implementable yet — deferred with owners** (see the table after the
tasks): §4 versioned named graphs and atomic publication, §5 revision
lifecycle, §6 deriver graph writes and coverage/staleness queries, §9
authorship triples, §10 server surfaces and web view, all §2–§5 vocabulary
(TTL), and the ADR-0006 amendment.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `deploy/base/migrations/0006_task_kinds.up.sql` | widen `tasks.kind` CHECK to six kinds |
| `deploy/base/migrations/0006_task_kinds.down.sql` | remap `review`/`spike` to `chore`, restore the four-kind CHECK |
| `internal/kg/section/section.go` | parse Markdown headings + Pandoc `{#…}` anchors into `Section`/`Doc`; content hashing |
| `internal/kg/section/section_test.go` | parsing: fences, attributes, numbers, bodies |
| `internal/kg/section/lint.go` | §3/§7 authoring rules: anchor grammar, number agreement, duplicates, depth limit |
| `internal/kg/section/lint_test.go` | each rule, both severities |
| `internal/kg/section/diff.go` | `Compare`: append-only anchors, renumber rejection, changed-content set, lowered-limit orphans |
| `internal/kg/section/diff_test.go` | every §7 constraint expressible locally; AC3a, AC5, AC6c(depth) |
| `internal/cmd/doc.go` | `lode doc` + `lode doc anchors` (local lint, §10) |
| `internal/cmd/doc_test.go` | in-process command test |
| `internal/kg/implements/implements.go` | parse + validate `.worklode/implements.yaml`; `wlid:` CURIE expansion |
| `internal/kg/implements/implements_test.go` | the spec's own example; every rejection |
| `internal/kg/implements/resolve.go` | claims: derive component via manifest, implicit component, split, unmatched-path error |
| `internal/kg/implements/resolve_test.go` | AC6a, AC6b |
| `internal/kg/implements/triples.go` | claims → `wl:implements` triples for `observed/repo-implements` |
| `internal/kg/implements/triples_test.go` | rendered edge set; dedupe |

**Modified files**

| Path | Change |
|---|---|
| `internal/kg/iri/iri.go` (+ its test) | add `Section(docSlug, anchor)` and `DocVersion(slug, n)` |
| `internal/api/tasks.go:18-20,87` | six kinds in `validKinds` and the error message |
| `internal/api/admin.go:440` | same error message (inbox promote) |
| `internal/api/tasks_test.go` | all-kinds creation test |
| `internal/cmd/task.go:85`, `internal/cmd/inbox.go:104` | `--kind` help text lists six kinds |
| `docs/specs/*.md`, `docs/plans/*.md` | prefix rename `ls:`→`wl:`, `lsc:`→`wlc:`, `lsid:`→`wlid:`, `/ls/`→`/wl/`; redundant prefix callouts removed |

**Test commands**

- Pure packages (no Postgres): `go test ./internal/kg/... ./internal/cmd/ -run TestDocAnchors`
- Store/API suites need Postgres (`store.OpenTestStore`): `go test ./internal/store/... ./internal/api/...`
- Everything: `go test ./...`

---

## Task 1: Widen `tasks.kind` to six kinds (§8, AC11 backbone half)

**Files:**
- Create: `deploy/base/migrations/0006_task_kinds.up.sql`, `deploy/base/migrations/0006_task_kinds.down.sql`
- Modify: `internal/api/tasks.go:18-20` (`validKinds`), `:87` (message); `internal/api/admin.go:440` (message); `internal/cmd/task.go:85`, `internal/cmd/inbox.go:104` (flag help)
- Test: `internal/api/tasks_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/api/tasks_test.go` (package `api_test`; `createProject`
and `createTaskViaAPI` are defined at the top of that file):

```go
func TestCreateTaskAllKinds(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for _, kind := range []string{"feature", "bug", "chore", "spec", "review", "spike"} {
		got := createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": "kind " + kind, "priority": "low", "kind": kind,
		})
		if got["kind"] != kind {
			t.Fatalf("kind = %v, want %s", got["kind"], kind)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestCreateTaskAllKinds -v`
Expected: FAIL — 422 `invalid kind` on `review`.

- [ ] **Step 3: Write the migration**

`deploy/base/migrations/0006_task_kinds.up.sql`:

```sql
-- Spec 014 §8: reconcile tasks.kind with wlc:TaskKind by widening to the
-- union of the two enumerations. No rows change.
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec','review','spike'));
```

`deploy/base/migrations/0006_task_kinds.down.sql`:

```sql
-- Remap the widened kinds so the restored constraint validates.
UPDATE tasks SET kind = 'chore' WHERE kind IN ('review','spike');
ALTER TABLE tasks DROP CONSTRAINT tasks_kind_check;
ALTER TABLE tasks ADD CONSTRAINT tasks_kind_check
    CHECK (kind IN ('feature','bug','chore','spec'));
```

(`tasks_kind_check` is Postgres's generated name for the inline column CHECK
in `0001_baseline.up.sql:53`.)

- [ ] **Step 4: Update the Go validation**

In `internal/api/tasks.go`:

```go
var validKinds = map[string]bool{
	"feature": true, "bug": true, "chore": true, "spec": true,
	"review": true, "spike": true,
}
```

and change the message at `tasks.go:87` — and the identical one at
`admin.go:440` — to:

```go
"invalid kind: must be feature, bug, chore, spec, review, or spike"
```

Update the flag help at `internal/cmd/task.go:85` and
`internal/cmd/inbox.go:104` to `"kind: feature, bug, chore, spec, review, spike"`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/api/ -run 'TestCreateTask' -v && go test ./internal/store/ -run TestMigrate -v`
Expected: PASS — `TestMigrateRoundTrip` (`internal/store/store_test.go:64`)
already exercises the new pair down and up.

- [ ] **Step 6: Commit**

```bash
git add deploy/base/migrations/0006_task_kinds.up.sql deploy/base/migrations/0006_task_kinds.down.sql internal/api internal/cmd/task.go internal/cmd/inbox.go
git commit -m "Widen tasks.kind to the six-kind union of spec 014 §8"
```

---

## Task 2: Rename `ls:`/`lsc:`/`lsid:` to `wl:`/`wlc:`/`wlid:` across docs (§1, AC1 docs half)

**Files:**
- Modify: every Markdown file under `docs/` matching the old prefixes
  (currently: specs 000, 003, 004, 006, 007, 008, 009, 014, 015, 016 and
  plans `2026-07-19-worklode-v1.md`, `2026-07-20-hzdev-ci-deploy.md`,
  `2026-07-24-backbone-postgres.md`, `2026-07-24-worklode-plugin.md`,
  `2026-07-27-org-wide-skills.md`)

No code, so no TDD loop; the verification grep is the test.

- [ ] **Step 1: Mechanical rename**

```bash
cd /Users/stig/git/sunstone/worklode
grep -rlE '\b(ls|lsc|lsid):' docs/ | xargs perl -pi -e 's/\blsid:/wlid:/g; s/\blsc:/wlc:/g; s/\bls:/wl:/g'
grep -rl '/ls/' docs/ | xargs perl -pi -e 's{/ls/}{/wl/}g'
```

The word boundary keeps `urls:`, `details:` etc. untouched; the path pass
rewrites `…/ns/ls/ontology#`, `rdf/ls/`, `/rdf/ls/ontology.ttl` and friends.

- [ ] **Step 2: Hand-fix the passages that describe the rename itself**

The mechanical pass corrupts prose whose subject is the old name. Reword each
so the old prefix appears **without a colon** (AC1 greps for `ls:` — `` `ls`
`` is fine, `` `ls:` `` is not):

- `docs/specs/014-design-documents-as-graph-objects.md` §1 — the heading
  ("Prefix rename: `ls` → `wl`"), the opening paragraph ("`ls` predates the
  rename…"), and the Old column of the prefix table (`` `ls` ``, `` `lsc` ``,
  `` `lsid` ``). The mechanical pass will have turned these into `wl:`→`wl:`
  nonsense; restore them as historical mentions sans colon.
- `docs/specs/015-runtime-layer.md:21` — "Spec 014 renamed the `ls` prefix to
  `wl:`. This spec is written in `wl:` throughout…".
- `docs/specs/000-umbrella-architecture.md:128-129` — rewrite the resolved
  bullet as one statement of the final state: "**[006] RDF namespace →
  `wl:` under `rdf/wl/`** (originally resolved as the `ls` prefix under
  `rdf/ls`; superseded by 014 §1)." — spelling `rdf/ls` without a trailing
  slash so the `/ls/` grep stays clean.
- `docs/specs/009-data-platform-kg-requirements.md:31` — after the mechanical
  pass this reads "the `wl:` ontology stays in rdf-registry", which is correct;
  just confirm the sentence still parses.

- [ ] **Step 3: Delete the callouts the rename makes redundant**

These callouts exist only to map the old prefix to the new one; with the body
renamed they say nothing:

- `docs/specs/006-knowledge-graph.md:42` ("Superseded by 014 §1. The prefixes
  are `wl:`…")
- `docs/specs/007-drift-and-overview.md:12` ("Read every … as `wl:`…")
- `docs/specs/008-worklode-plugin.md:7` ("Read `ls:governs`… as
  `wl:governs`…")
- `docs/specs/000-umbrella-architecture.md:94-95` ("Amended by 014 §1. The
  prefix is `wl:`…")

Keep every other 014/015 amendment callout — they amend content, not spelling.
Where a surviving sentence names 014 §1 as the source of the now-inline
spelling (e.g. `006:188` "minted as `wl:deliveredBy`" after the pass), drop
the parenthetical rather than leaving a self-referential note.

- [ ] **Step 4: Verify**

```bash
grep -rnE '\b(ls|lsc|lsid):' docs/ ; grep -rn '/ls/' docs/
```

Expected: no output from either. Then read the diff of
`docs/specs/006-knowledge-graph.md` and `docs/specs/014-…md` end to end —
these two carry the rename-describing prose and the namespace tables, and are
where a mechanical slip would hide.

- [ ] **Step 5: Commit**

```bash
git add docs
git commit -m "Rename the ls prefixes to wl across the docs (spec 014 §1)"
```

---

## Task 3: Section and versioned-document IRIs

**Files:**
- Modify: `internal/kg/iri/iri.go`
- Test: `internal/kg/iri/iri_test.go` (append)

Depends on the platform-graph-design plan's Task 1 (`internal/kg/iri`).

- [ ] **Step 1: Write the failing test**

Append to `internal/kg/iri/iri_test.go`, inside `TestInstanceIRIs`'s `cases`
slice:

```go
		{"section (014 §3)", func() (string, error) {
			return iri.Section("spec-worklode-014", "sec-3")
		}, b + "section/spec-worklode-014/sec-3"},
		{"versioned doc (014 §4)", func() (string, error) {
			return iri.DocVersion("spec-worklode-014", 3)
		}, b + "doc/spec-worklode-014/v3"},
```

and inside `TestInstanceIRIRejects`'s `cases` slice:

```go
		{"section empty anchor", func() (string, error) { return iri.Section("spec-worklode-014", "") }},
		{"doc version zero", func() (string, error) { return iri.DocVersion("spec-worklode-014", 0) }},
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/iri/`
Expected: FAIL — `undefined: iri.Section`, `undefined: iri.DocVersion`

- [ ] **Step 3: Write the implementation**

Add to `internal/kg/iri/iri.go`, after `Doc`:

```go
// Section returns the IRI of an addressable design-document section
// (014 §3): id/section/<doc-slug>/<anchor>. The anchor is assigned at first
// publication and never changes; the IRI is therefore as durable as the
// document's.
func Section(docSlug, anchor string) (string, error) {
	return mint("section", docSlug, anchor)
}

// DocVersion returns the immutable versioned sibling IRI of a design
// document (014 §4): id/doc/<slug>/v<n>. Everything links to the canonical
// Doc IRI by default; versioned IRIs appear only in pinned claims.
func DocVersion(slug string, version int) (string, error) {
	if version <= 0 {
		return "", fmt.Errorf("iri: doc version %d is not positive", version)
	}
	return mint("doc", slug, "v"+strconv.Itoa(version))
}
```

(`fmt`, `strconv` are already imported.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/iri/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/iri
git commit -m "Mint section and versioned-document IRIs (spec 014)"
```

---

## Task 4: Parse Markdown sections and anchors

**Files:**
- Create: `internal/kg/section/section.go`
- Test: `internal/kg/section/section_test.go`

- [ ] **Step 1: Write the failing test**

```go
package section

import (
	"strings"
	"testing"
)

const sample = `# Spec X — title {#sec-spec-x}

Intro paragraph.

## 1. First {#sec-1}

Body of one.

` + "```" + `markdown
## not a heading (fenced)
` + "```" + `

## 2.1a Inserted section {#sec-2.1a}

Inserted body.

### Unanchored subsection

Sub body.

#### 2026-07-30 log entry

Not a section number.
`

func TestParse(t *testing.T) {
	d := Parse([]byte(sample))
	type want struct {
		anchor, number, title string
		depth, line           int
	}
	wants := []want{
		{"sec-spec-x", "", "Spec X — title", 1, 1},
		{"sec-1", "1", "First", 2, 5},
		{"sec-2.1a", "2.1a", "Inserted section", 2, 13},
		{"", "", "Unanchored subsection", 3, 17},
		{"", "", "2026-07-30 log entry", 4, 21},
	}
	if len(d.Sections) != len(wants) {
		t.Fatalf("parsed %d sections; want %d: %+v", len(d.Sections), len(wants), d.Sections)
	}
	for i, w := range wants {
		s := d.Sections[i]
		if s.Anchor != w.anchor || s.Number != w.number || s.Title != w.title ||
			s.Depth != w.depth || s.Line != w.line {
			t.Errorf("section %d = %+v; want %+v", i, s, w)
		}
	}
}

func TestParseBodies(t *testing.T) {
	d := Parse([]byte(sample))
	if !strings.Contains(d.Sections[1].body, "Body of one.") ||
		!strings.Contains(d.Sections[1].body, "not a heading (fenced)") {
		t.Fatalf("section 1 body = %q; want its prose and the fenced block", d.Sections[1].body)
	}
	if strings.Contains(d.Sections[1].body, "Inserted body.") {
		t.Fatal("section 1 body leaked into section 2.1a")
	}
}

func TestContentHash(t *testing.T) {
	a := Parse([]byte("## 1. T {#sec-1}\n\nsame body\n"))
	b := Parse([]byte("## 1. Reworded heading {#sec-1}\n\nsame body\n"))
	c := Parse([]byte("## 1. T {#sec-1}\n\ndifferent body\n"))
	if a.Sections[0].ContentHash() != b.Sections[0].ContentHash() {
		t.Fatal("rewording a heading changed the content hash (014 §3: rewording is free)")
	}
	if a.Sections[0].ContentHash() == c.Sections[0].ContentHash() {
		t.Fatal("different bodies hashed equal")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/section/`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package section models design-document sections per spec 014 §3: Markdown
// headings carrying Pandoc attribute anchors ({#sec-2.1}) become addressable
// nodes, and the §7 constraints on accepted documents are set operations
// over the parsed section lists (see lint.go, diff.go).
package section

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Section is one heading of a design document, addressable when anchored.
type Section struct {
	Anchor string // Pandoc attribute id ("sec-2.1"); "" when unanchored
	Number string // section number from the heading text ("2.1a"); "" when unnumbered
	Title  string // heading text without number and attribute
	Depth  int    // heading level (# == 1)
	Line   int    // 1-based source line of the heading

	body string // content up to the next heading, for change detection
}

// Addressable reports whether the section can carry claims: it has an anchor
// and sits within the addressability depth limit (014 §7.1).
func (s *Section) Addressable(depthLimit int) bool {
	return s.Anchor != "" && s.Depth <= depthLimit
}

// ContentHash digests the section body. The heading line is excluded:
// rewording a heading is explicitly free (014 §3), so it must not feed
// wl:lastRevisedIn.
func (s *Section) ContentHash() string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(s.body)))
	return hex.EncodeToString(sum[:])
}

// Doc is a parsed document: its headings in source order.
type Doc struct {
	Sections []Section
}

var (
	headingRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)
	attrRE    = regexp.MustCompile(`\s*\{#([^}\s]+)\}$`)
	// A section number: digits with dotted levels and an optional letter
	// suffix (the 2.1a insert convention), followed by an optional dot and
	// the title. "2026-07-30 …" does not match: the hyphen breaks it.
	numberRE = regexp.MustCompile(`^(\d+(?:\.\d+)*[a-z]*)\.?(?:\s+|$)`)
)

// Parse extracts the heading structure of a Markdown document. Headings
// inside fenced code blocks are content, not structure. Parsing is total —
// rule violations are Lint's business, not Parse's.
func Parse(src []byte) *Doc {
	var d Doc
	var body strings.Builder
	cur := -1
	fence := "" // the opening fence marker while inside a fenced block
	flush := func() {
		if cur >= 0 {
			d.Sections[cur].body = body.String()
		}
		body.Reset()
	}

	for i, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if marker := fenceMarker(trimmed); marker != "" {
			if fence == "" {
				fence = marker
			} else if marker == fence {
				fence = ""
			}
		} else if fence == "" {
			if m := headingRE.FindStringSubmatch(line); m != nil {
				flush()
				text := m[2]
				anchor := ""
				if loc := attrRE.FindStringSubmatchIndex(text); loc != nil {
					anchor = text[loc[2]:loc[3]]
					text = strings.TrimSpace(text[:loc[0]])
				}
				number := ""
				if nm := numberRE.FindStringSubmatch(text); nm != nil {
					number = nm[1]
					text = strings.TrimSpace(text[len(nm[0]):])
				}
				d.Sections = append(d.Sections, Section{
					Anchor: anchor, Number: number, Title: text,
					Depth: len(m[1]), Line: i + 1,
				})
				cur = len(d.Sections) - 1
				continue
			}
		}
		if cur >= 0 {
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	flush()
	return &d
}

// fenceMarker returns "```" or "~~~" when the line opens/closes a fenced
// block, else "".
func fenceMarker(trimmed string) string {
	for _, f := range []string{"```", "~~~"} {
		if strings.HasPrefix(trimmed, f) {
			return f
		}
	}
	return ""
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/section/ -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/kg/section
git commit -m "Parse design-document sections and Pandoc anchors"
```

---

## Task 5: Lint anchors against the authoring rules

**Files:**
- Create: `internal/kg/section/lint.go`
- Test: `internal/kg/section/lint_test.go`

- [ ] **Step 1: Write the failing test**

```go
package section

import (
	"strings"
	"testing"
)

func lintOf(t *testing.T, src string, depthLimit int) []Problem {
	t.Helper()
	return Lint(Parse([]byte(src)), depthLimit)
}

func onlyErrors(ps []Problem) []Problem {
	var out []Problem
	for _, p := range ps {
		if p.Severity == Error {
			out = append(out, p)
		}
	}
	return out
}

func TestLintCleanDoc(t *testing.T) {
	ps := lintOf(t, "# T {#sec-t}\n\n## 1. A {#sec-1}\n\n### 1.1 B {#sec-1.1}\n\n#### Deep, unanchored, fine\n", 3)
	if len(ps) != 0 {
		t.Fatalf("clean doc linted %+v; want none", ps)
	}
}

func TestLintRules(t *testing.T) {
	cases := []struct {
		name, src, wantMsg string
	}{
		{"bad anchor grammar", "## 1. A {#1}\n", "sec-"},
		{"uppercase anchor", "## 1. A {#sec-A}\n", "sec-"},
		{"number disagreement", "## 2. B {#sec-3}\n", "disagrees"},
		{"duplicate anchor", "## 1. A {#sec-1}\n\n## 2. B {#sec-1}\n", "already used"},
		{"anchor past depth limit", "#### 1.1.1.1 Deep {#sec-1.1.1.1}\n", "limit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := onlyErrors(lintOf(t, tc.src, 3))
			if len(errs) == 0 {
				t.Fatalf("linted no errors; want one mentioning %q", tc.wantMsg)
			}
			if !strings.Contains(errs[0].Msg, tc.wantMsg) {
				t.Fatalf("error %q does not mention %q", errs[0].Msg, tc.wantMsg)
			}
		})
	}
}

func TestLintUnanchoredWithinLimitWarns(t *testing.T) {
	ps := lintOf(t, "## 1. Anchorless\n", 3)
	if len(ps) != 1 || ps[0].Severity != Warning {
		t.Fatalf("lint = %+v; want one warning", ps)
	}
}

// AC6c, lint half: with the limit at 3 a ##### heading is not addressable
// and an anchor on it is an error; raising the limit to 5 clears it.
func TestLintDepthLimitIsConfigurable(t *testing.T) {
	src := "##### Deep {#sec-deep}\n"
	if errs := onlyErrors(lintOf(t, src, 3)); len(errs) != 1 {
		t.Fatalf("limit 3: %+v; want the depth error", errs)
	}
	if errs := onlyErrors(lintOf(t, src, 5)); len(errs) != 0 {
		t.Fatalf("limit 5: %+v; want none", errs)
	}
}

func TestValidAnchor(t *testing.T) {
	for a, want := range map[string]bool{
		"sec-2.1": true, "sec-2.1a": true, "sec-purpose": true,
		"2.1": false, "sec-": false, "sec-Ü": false, "SEC-1": false,
	} {
		if ValidAnchor(a) != want {
			t.Errorf("ValidAnchor(%q) = %v; want %v", a, !want, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/section/`
Expected: FAIL — `undefined: Lint`, `undefined: Problem`, `undefined: ValidAnchor`

- [ ] **Step 3: Write the implementation**

```go
package section

import (
	"fmt"
	"regexp"
)

// Severity of a lint problem. Errors block publication; warnings are
// authoring hints (an unanchored heading is legal — just not addressable).
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
)

// Problem is one lint finding, tied to a source line.
type Problem struct {
	Line     int      `json:"line"`
	Severity Severity `json:"severity"`
	Msg      string   `json:"msg"`
}

// anchorRE is the 014 §3 anchor grammar: the sec- prefix (an id like
// {#2.1} is legal HTML but a hostile CSS selector and URL fragment), then a
// section number (2.1a) or a lowercase slug (purpose).
var anchorRE = regexp.MustCompile(`^sec-[a-z0-9][a-z0-9.-]*$`)

// ValidAnchor reports whether a is a well-formed section anchor.
func ValidAnchor(a string) bool { return anchorRE.MatchString(a) }

// Lint checks a document against the 014 §3/§7 authoring rules. depthLimit
// is the addressability limit (server-configurable, default 3): deeper
// headings render normally but must not carry anchors, because an anchor
// there would be a promise nobody can pin to.
func Lint(d *Doc, depthLimit int) []Problem {
	var out []Problem
	seen := map[string]int{}
	for i := range d.Sections {
		s := &d.Sections[i]
		if s.Anchor == "" {
			if s.Depth <= depthLimit {
				out = append(out, Problem{s.Line, Warning,
					fmt.Sprintf("heading %q has no anchor and is not addressable", s.Title)})
			}
			continue
		}
		if !ValidAnchor(s.Anchor) {
			out = append(out, Problem{s.Line, Error,
				fmt.Sprintf("anchor %q does not match the sec-<number-or-slug> grammar", s.Anchor)})
		}
		if s.Number != "" && s.Anchor != "sec-"+s.Number {
			out = append(out, Problem{s.Line, Error,
				fmt.Sprintf("anchor %q disagrees with section number %s (want sec-%s)", s.Anchor, s.Number, s.Number)})
		}
		if s.Depth > depthLimit {
			out = append(out, Problem{s.Line, Error,
				fmt.Sprintf("anchor %q sits at depth %d, past the addressability limit %d", s.Anchor, s.Depth, depthLimit)})
		}
		if prev, dup := seen[s.Anchor]; dup {
			out = append(out, Problem{s.Line, Error,
				fmt.Sprintf("anchor %q already used at line %d", s.Anchor, prev)})
		} else {
			seen[s.Anchor] = s.Line
		}
	}
	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/section/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/section
git commit -m "Lint section anchors against the spec 014 authoring rules"
```

---

## Task 6: `lode doc anchors` (§10, the author's pre-publication lint)

**Files:**
- Create: `internal/cmd/doc.go`
- Test: `internal/cmd/doc_test.go`

Purely local — reads a file, no server. The depth limit is a flag defaulting
to 3 until the server-side setting exists (it belongs to the publication
surface, deferred).

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runDocAnchors(t *testing.T, src string, args ...string) (string, string, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spec.md")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := newDocAnchorsCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append(args, path))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestDocAnchorsClean(t *testing.T) {
	out, _, err := runDocAnchors(t, "# T {#sec-t}\n\n## 1. A {#sec-1}\n\n#### Deep content\n")
	if err != nil {
		t.Fatalf("clean doc: %v", err)
	}
	if !strings.Contains(out, "sec-1") || !strings.Contains(out, "true") {
		t.Fatalf("output missing the anchor table:\n%s", out)
	}
	// The depth-4 heading is listed but not addressable at the default limit.
	if !strings.Contains(out, "false") {
		t.Fatalf("deep heading not reported unaddressable:\n%s", out)
	}
}

func TestDocAnchorsErrors(t *testing.T) {
	_, errOut, err := runDocAnchors(t, "## 2. B {#sec-3}\n")
	if err == nil {
		t.Fatal("number/anchor disagreement did not fail the command")
	}
	if !strings.Contains(errOut, "sec-3") {
		t.Fatalf("stderr does not name the offending anchor:\n%s", errOut)
	}
}

func TestDocAnchorsDepthLimitFlag(t *testing.T) {
	src := "#### 1.1.1.1 Deep {#sec-1.1.1.1}\n"
	if _, _, err := runDocAnchors(t, src); err == nil {
		t.Fatal("depth-4 anchor passed at the default limit 3")
	}
	if _, _, err := runDocAnchors(t, src, "--depth-limit", "4"); err != nil {
		t.Fatalf("depth-4 anchor failed at limit 4: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestDocAnchors`
Expected: FAIL — `undefined: newDocAnchorsCmd`

- [ ] **Step 3: Write the implementation**

```go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/kg/section"
)

func newDocCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doc",
		Short: "work with design documents",
	}
	cmd.AddCommand(newDocAnchorsCmd())
	return cmd
}

// newDocAnchorsCmd is the pre-publication lint of spec 014 §10: list a
// document's anchors with depth and addressability, and fail on authoring-
// rule violations. Local-only; the depth limit becomes a server setting once
// the publication surface exists.
func newDocAnchorsCmd() *cobra.Command {
	var depthLimit int
	cmd := &cobra.Command{
		Use:   "anchors <file.md>",
		Short: "list section anchors and lint them before publishing",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			doc := section.Parse(src)
			problems := section.Lint(doc, depthLimit)

			if jsonOut(cmd) {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(anchorReport{
					DepthLimit: depthLimit,
					Sections:   anchorRows(doc, depthLimit),
					Problems:   problems,
				})
			}

			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%-6s %-28s %-6s %s\n", "LINE", "ANCHOR", "DEPTH", "ADDRESSABLE")
			for i := range doc.Sections {
				s := &doc.Sections[i]
				anchor := s.Anchor
				if anchor == "" {
					anchor = "-"
				}
				fmt.Fprintf(out, "%-6d %-28s %-6d %v\n", s.Line, anchor, s.Depth, s.Addressable(depthLimit))
			}
			errs := 0
			for _, p := range problems {
				fmt.Fprintf(cmd.ErrOrStderr(), "%s: line %d: %s\n", p.Severity, p.Line, p.Msg)
				if p.Severity == section.Error {
					errs++
				}
			}
			if errs > 0 {
				return fmt.Errorf("%d anchor error(s)", errs)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&depthLimit, "depth-limit", 3,
		"addressability depth limit (a server setting once publication exists)")
	return cmd
}

type anchorRow struct {
	Line        int    `json:"line"`
	Anchor      string `json:"anchor,omitempty"`
	Number      string `json:"number,omitempty"`
	Title       string `json:"title"`
	Depth       int    `json:"depth"`
	Addressable bool   `json:"addressable"`
}

type anchorReport struct {
	DepthLimit int               `json:"depth_limit"`
	Sections   []anchorRow       `json:"sections"`
	Problems   []section.Problem `json:"problems"`
}

func anchorRows(doc *section.Doc, depthLimit int) []anchorRow {
	rows := make([]anchorRow, 0, len(doc.Sections))
	for i := range doc.Sections {
		s := &doc.Sections[i]
		rows = append(rows, anchorRow{
			Line: s.Line, Anchor: s.Anchor, Number: s.Number, Title: s.Title,
			Depth: s.Depth, Addressable: s.Addressable(depthLimit),
		})
	}
	return rows
}

func init() {
	rootCmd.AddCommand(newDocCmd())
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cmd/ -run TestDocAnchors -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/doc.go internal/cmd/doc_test.go
git commit -m "Add lode doc anchors, the pre-publication anchor lint"
```

---

## Task 7: The version-diff gate (§7 as a set diff; AC3a, AC5, AC6c, AC8 input)

**Files:**
- Create: `internal/kg/section/diff.go`
- Test: `internal/kg/section/diff_test.go`

This is the local form of the publication gate: given the accepted version's
source and a candidate revision's source, compute exactly what §7 forbids.
The SHACL form over version graphs lands with the 006 vocabulary; this
function is what `lode doc publish` and CI will call, and is usable today as
a pre-merge check.

- [ ] **Step 1: Write the failing test**

```go
package section

import (
	"strings"
	"testing"
)

const acceptedV1 = `# Spec {#sec-spec}

## 1. One {#sec-1}

Alpha.

## 2. Two {#sec-2}

Beta.

### 2.1 Two-one {#sec-2.1}

Gamma.
`

func compareSrc(t *testing.T, accepted, candidate string, limit int) Diff {
	t.Helper()
	return Compare(Parse([]byte(accepted)), Parse([]byte(candidate)), limit)
}

func TestCompareIdentityIsEmpty(t *testing.T) {
	d := compareSrc(t, acceptedV1, acceptedV1, 3)
	if len(d.Removed)+len(d.Renumbered)+len(d.Changed)+len(d.Added)+len(d.TooDeep) != 0 {
		t.Fatalf("self-compare = %+v; want empty", d)
	}
	if v := d.Violations(); len(v) != 0 {
		t.Fatalf("violations = %v; want none", v)
	}
}

// §7.1 / AC5: deleting an accepted anchor is rejected.
func TestCompareRemovedAnchorIsViolation(t *testing.T) {
	candidate := strings.Replace(acceptedV1, "### 2.1 Two-one {#sec-2.1}\n\nGamma.\n", "", 1)
	d := compareSrc(t, acceptedV1, candidate, 3)
	if len(d.Removed) != 1 || d.Removed[0] != "sec-2.1" {
		t.Fatalf("Removed = %v; want [sec-2.1]", d.Removed)
	}
	if v := d.Violations(); len(v) != 1 || !strings.Contains(v[0], "sec-2.1") {
		t.Fatalf("violations = %v; want one naming sec-2.1", v)
	}
}

// §7.3 / AC3a: renumbering 2 → 3 under a kept anchor is rejected; inserting
// 1a renumbers nothing and passes.
func TestCompareRenumberIsViolationInsertIsNot(t *testing.T) {
	renumbered := strings.Replace(acceptedV1, "## 2. Two {#sec-2}", "## 3. Two {#sec-2}", 1)
	d := compareSrc(t, acceptedV1, renumbered, 3)
	if len(d.Renumbered) != 1 || !strings.Contains(d.Renumbered[0], "sec-2") {
		t.Fatalf("Renumbered = %v; want sec-2", d.Renumbered)
	}
	if len(d.Violations()) == 0 {
		t.Fatal("renumbering produced no violation")
	}

	inserted := strings.Replace(acceptedV1, "## 2. Two",
		"## 1a. Inserted {#sec-1a}\n\nNew.\n\n## 2. Two", 1)
	d = compareSrc(t, acceptedV1, inserted, 3)
	if len(d.Violations()) != 0 {
		t.Fatalf("letter-suffix insert violated: %v", d.Violations())
	}
	if len(d.Added) != 1 || d.Added[0] != "sec-1a" {
		t.Fatalf("Added = %v; want [sec-1a]", d.Added)
	}
}

// §7.5 / AC8 input: only sections whose content changed are in Changed —
// editing §2 must not touch §1.
func TestCompareChangedIsExact(t *testing.T) {
	candidate := strings.Replace(acceptedV1, "Beta.", "Beta, revised.", 1)
	d := compareSrc(t, acceptedV1, candidate, 3)
	if len(d.Changed) != 1 || d.Changed[0] != "sec-2" {
		t.Fatalf("Changed = %v; want [sec-2]", d.Changed)
	}
}

// Rewording a heading is not a content change (014 §3).
func TestCompareHeadingRewordIsNotChange(t *testing.T) {
	candidate := strings.Replace(acceptedV1, "## 2. Two {#sec-2}", "## 2. Two, better titled {#sec-2}", 1)
	d := compareSrc(t, acceptedV1, candidate, 3)
	if len(d.Changed) != 0 || len(d.Violations()) != 0 {
		t.Fatalf("heading reword: Changed=%v violations=%v; want none", d.Changed, d.Violations())
	}
}

// §7.6 / AC6c: lowering the limit below an accepted anchor's depth is a
// violation naming the anchor.
func TestCompareLoweredLimitNamesOrphans(t *testing.T) {
	d := compareSrc(t, acceptedV1, acceptedV1, 2)
	if len(d.TooDeep) != 1 || d.TooDeep[0] != "sec-2.1" {
		t.Fatalf("TooDeep = %v; want [sec-2.1]", d.TooDeep)
	}
	if v := d.Violations(); len(v) != 1 || !strings.Contains(v[0], "sec-2.1") {
		t.Fatalf("violations = %v; want one naming sec-2.1", v)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/section/`
Expected: FAIL — `undefined: Compare`, `undefined: Diff`

- [ ] **Step 3: Write the implementation**

```go
package section

import "fmt"

// Diff compares the accepted version of a document with a candidate
// revision (014 §7). Removed, Renumbered and TooDeep are violations;
// Changed is the wl:lastRevisedIn input (exactly the sections whose content
// changed — §7.5); Added is informational.
type Diff struct {
	Added      []string // candidate anchors not in accepted
	Removed    []string // accepted anchors missing from candidate — §7.1
	Renumbered []string // "sec-2: 2 -> 3" — §7.3
	Changed    []string // accepted anchors whose content differs — §7.5
	TooDeep    []string // accepted anchors past the depth limit — §7.6
}

// Compare evaluates the §7 constraints locally. depthLimit is the current
// addressability limit; accepted anchors deeper than it surface in TooDeep,
// which makes lowering the limit one-way-safe by construction.
func Compare(accepted, candidate *Doc, depthLimit int) Diff {
	var d Diff
	cand := byAnchor(candidate)
	acc := byAnchor(accepted)

	for i := range accepted.Sections {
		s := &accepted.Sections[i]
		if s.Anchor == "" {
			continue
		}
		if s.Depth > depthLimit {
			d.TooDeep = append(d.TooDeep, s.Anchor)
		}
		c, ok := cand[s.Anchor]
		if !ok {
			d.Removed = append(d.Removed, s.Anchor)
			continue
		}
		if c.Number != s.Number {
			d.Renumbered = append(d.Renumbered,
				fmt.Sprintf("%s: %s -> %s", s.Anchor, orNone(s.Number), orNone(c.Number)))
		}
		if c.ContentHash() != s.ContentHash() {
			d.Changed = append(d.Changed, s.Anchor)
		}
	}
	for i := range candidate.Sections {
		s := &candidate.Sections[i]
		if s.Anchor != "" {
			if _, ok := acc[s.Anchor]; !ok {
				d.Added = append(d.Added, s.Anchor)
			}
		}
	}
	return d
}

// Violations renders the blocking findings as messages; an empty result
// means the revision is publishable under §7.
func (d Diff) Violations() []string {
	var v []string
	for _, a := range d.Removed {
		v = append(v, fmt.Sprintf(
			"removed anchor %s: anchors are append-only — mark the section superseded instead (§7.1)", a))
	}
	for _, r := range d.Renumbered {
		v = append(v, fmt.Sprintf(
			"renumbered %s: accepted sections are never renumbered — insert with a letter suffix, e.g. 2.1a (§7.3)", r))
	}
	for _, a := range d.TooDeep {
		v = append(v, fmt.Sprintf(
			"anchor %s is deeper than the depth limit: lowering the limit may not orphan accepted anchors (§7.6)", a))
	}
	return v
}

// byAnchor indexes a document's anchored sections; on a duplicate anchor
// (a Lint error) the first occurrence wins.
func byAnchor(d *Doc) map[string]*Section {
	m := make(map[string]*Section, len(d.Sections))
	for i := range d.Sections {
		s := &d.Sections[i]
		if s.Anchor != "" {
			if _, dup := m[s.Anchor]; !dup {
				m[s.Anchor] = s
			}
		}
	}
	return m
}

func orNone(n string) string {
	if n == "" {
		return "(unnumbered)"
	}
	return n
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/section/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/section
git commit -m "Compare document versions against the spec 014 publication constraints"
```

---

## Task 8: Parse `.worklode/implements.yaml` (§6)

**Files:**
- Create: `internal/kg/implements/implements.go`
- Test: `internal/kg/implements/implements_test.go`

Depends on Task 3 (`iri.Section`) and Task 5 (`section.ValidAnchor`).

- [ ] **Step 1: Write the failing test**

The happy-path fixture is the spec's own §6 example, verbatim.

```go
package implements_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
)

const specExample = `
# .worklode/implements.yaml
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2     # version validated against
    by:      [internal/store/lease.go, internal/store/sweeper.go]
  - section: wlid:section/spec-worklode-013/sec-3.1
    pinned:  wlid:doc/spec-worklode-013/v1
    by:      [internal/hooks/apply.go]
`

func TestParseSpecExample(t *testing.T) {
	f, err := implements.Parse([]byte(specExample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(f.Implements) != 2 {
		t.Fatalf("entries = %d; want 2", len(f.Implements))
	}
	e := f.Implements[0]
	// The wlid: CURIE expands to the full instance IRI.
	if e.Section != "https://worklode.io/ns/wl/id/section/spec-worklode-004/sec-4" {
		t.Fatalf("section = %q", e.Section)
	}
	if e.Pinned != "https://worklode.io/ns/wl/id/doc/spec-worklode-004/v2" {
		t.Fatalf("pinned = %q", e.Pinned)
	}
	if len(e.By) != 2 || e.By[0] != "internal/store/lease.go" {
		t.Fatalf("by = %v", e.By)
	}
}

func TestParseAcceptsFullIRIs(t *testing.T) {
	f, err := implements.Parse([]byte(`
implements:
  - section: https://worklode.io/ns/wl/id/section/spec-worklode-014/sec-3
    pinned:  https://worklode.io/ns/wl/id/doc/spec-worklode-014/v1
    by:      [internal/kg/section/section.go]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if f.Implements[0].Section != "https://worklode.io/ns/wl/id/section/spec-worklode-014/sec-3" {
		t.Fatalf("section = %q", f.Implements[0].Section)
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"no entries", "implements: []"},
		{"not a section IRI", "implements: [{section: 'wlid:doc/x', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"bad anchor", "implements: [{section: 'wlid:section/x/NOT-AN-ANCHOR', pinned: 'wlid:doc/x/v1', by: [a.go]}]"},
		{"pinned not versioned", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x', by: [a.go]}]"},
		{"pinned version zero", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v0', by: [a.go]}]"},
		{"pin names another doc", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/y/v1', by: [a.go]}]"},
		{"no paths", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: []}]"},
		{"absolute path", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: [/etc/passwd]}]"},
		{"dotdot path", "implements: [{section: 'wlid:section/x/sec-1', pinned: 'wlid:doc/x/v1', by: ['../other/a.go']}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if f, err := implements.Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse accepted %+v; want an error", f)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := implements.Load(filepath.Join(t.TempDir(), "implements.yaml"))
	if !os.IsNotExist(err) {
		t.Fatalf("Load on a missing file: %v; want os.IsNotExist", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/implements/...`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Write the implementation**

```go
// Package implements reads .worklode/implements.yaml (spec 014 §6): the
// machine-readable claim that this repository's code satisfies specific
// design-document sections, pinned to the document version validated
// against. The manifest deliberately has no component field — the claiming
// component is derived from the by: paths (resolve.go), never declared.
package implements

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/sunstoneinstitute/worklode/internal/kg/section"
)

const (
	idPrefix      = "https://worklode.io/ns/wl/id/"
	sectionPrefix = idPrefix + "section/"
	docPrefix     = idPrefix + "doc/"
)

var versionRE = regexp.MustCompile(`^v[1-9][0-9]*$`)

// Entry is one claim: this repo's files in By satisfy Section, validated
// against the Pinned document version. Parse normalizes Section and Pinned
// to full IRIs.
type Entry struct {
	Section string   `yaml:"section"`
	Pinned  string   `yaml:"pinned"`
	By      []string `yaml:"by"`
}

// File is a parsed .worklode/implements.yaml.
type File struct {
	Implements []Entry `yaml:"implements"`
}

// Load reads and parses the manifest at p. A missing file surfaces as
// os.IsNotExist: a repo with no claims simply has no manifest.
func Load(p string) (*File, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	f, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return f, nil
}

// Parse parses and validates manifest YAML. The wlid: CURIE of the spec's
// examples expands to the full instance IRI; both forms are accepted.
func Parse(data []byte) (*File, error) {
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse implements manifest: %w", err)
	}
	if len(f.Implements) == 0 {
		return nil, fmt.Errorf("implements manifest: at least one entry is required")
	}
	for i := range f.Implements {
		e := &f.Implements[i]
		e.Section = expand(e.Section)
		docSlug, err := sectionDoc(e.Section)
		if err != nil {
			return nil, fmt.Errorf("implements entry %d: %w", i, err)
		}
		e.Pinned = expand(e.Pinned)
		pinSlug, err := pinnedDoc(e.Pinned)
		if err != nil {
			return nil, fmt.Errorf("implements entry %d: %w", i, err)
		}
		if pinSlug != docSlug {
			return nil, fmt.Errorf("implements entry %d: pinned %q names doc %q, but the section belongs to %q",
				i, e.Pinned, pinSlug, docSlug)
		}
		if len(e.By) == 0 {
			return nil, fmt.Errorf("implements entry %d: by needs at least one path", i)
		}
		for j, p := range e.By {
			clean := path.Clean(strings.TrimSpace(p))
			if clean == "" || clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
				return nil, fmt.Errorf("implements entry %d: path %q is not repo-relative", i, p)
			}
			e.By[j] = clean
		}
	}
	return &f, nil
}

// expand resolves the wlid: prefix (014 §1) to the full instance namespace.
func expand(v string) string {
	if rest, ok := strings.CutPrefix(strings.TrimSpace(v), "wlid:"); ok {
		return idPrefix + rest
	}
	return strings.TrimSpace(v)
}

// sectionDoc validates a section IRI — id/section/<doc-slug>/<anchor> — and
// returns its doc slug.
func sectionDoc(iri string) (string, error) {
	rest, ok := strings.CutPrefix(iri, sectionPrefix)
	if !ok {
		return "", fmt.Errorf("section %q is not a %s IRI", iri, sectionPrefix)
	}
	slug, anchor, ok := strings.Cut(rest, "/")
	if !ok || slug == "" || strings.Contains(anchor, "/") {
		return "", fmt.Errorf("section %q is not id/section/<doc-slug>/<anchor>", iri)
	}
	if !section.ValidAnchor(anchor) {
		return "", fmt.Errorf("section %q: anchor %q does not match the sec- grammar", iri, anchor)
	}
	return slug, nil
}

// pinnedDoc validates a versioned doc IRI — id/doc/<slug>/v<n> (014 §4) —
// and returns its doc slug.
func pinnedDoc(iri string) (string, error) {
	rest, ok := strings.CutPrefix(iri, docPrefix)
	if !ok {
		return "", fmt.Errorf("pinned %q is not a %s IRI", iri, docPrefix)
	}
	slug, version, ok := strings.Cut(rest, "/")
	if !ok || slug == "" || !versionRE.MatchString(version) {
		return "", fmt.Errorf("pinned %q is not id/doc/<slug>/v<n>", iri)
	}
	return slug, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/implements/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/implements
git commit -m "Parse the implements manifest with pinned-version validation"
```

---

## Task 9: Derive the claiming component (§6; AC6a, AC6b)

**Files:**
- Create: `internal/kg/implements/resolve.go`
- Test: `internal/kg/implements/resolve_test.go`

Depends on the platform-graph-design plan's `internal/kg/manifest`
(first-match-wins `Match`) and `internal/kg/iri` (`Component`).

- [ ] **Step 1: Write the failing test**

```go
package implements_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const twoComponentManifest = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["internal/ingest/**"]
  - iri: https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/research-stack/pfas
    name: pfas
    paths: ["internal/pfas/**"]
`

func parseBoth(t *testing.T, implYAML string) (*implements.File, *manifest.Manifest) {
	t.Helper()
	f, err := implements.Parse([]byte(implYAML))
	if err != nil {
		t.Fatalf("parse implements: %v", err)
	}
	m, err := manifest.Parse([]byte(twoComponentManifest))
	if err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	return f, m
}

// AC6b: paths spanning two components yield two claims, same pin each; a
// path matching no component is an error naming the path.
func TestResolveSplitsAcrossComponents(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/reader.go, internal/pfas/model.go]
`)
	claims, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %+v; want 2 (one per component)", claims)
	}
	for _, c := range claims {
		if c.Section != "https://worklode.io/ns/wl/id/section/spec-worklode-004/sec-4" ||
			c.Pinned != "https://worklode.io/ns/wl/id/doc/spec-worklode-004/v2" {
			t.Fatalf("claim %+v carries the wrong section/pin", c)
		}
	}
	if claims[0].Component == claims[1].Component {
		t.Fatalf("both claims name %s; want one per component", claims[0].Component)
	}
}

func TestResolveUnmatchedPathIsError(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [README.md]
`)
	_, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err == nil || !strings.Contains(err.Error(), "README.md") {
		t.Fatalf("err = %v; want an error naming README.md", err)
	}
}

// AC6a: no components.yaml → the implicit component, IRI = repo coords; an
// explicit whole-repo manifest naming the same IRI leaves subjects unchanged.
func TestResolveImplicitComponent(t *testing.T) {
	f, err := implements.Parse([]byte(`
implements:
  - section: wlid:section/spec-worklode-014/sec-6
    pinned:  wlid:doc/spec-worklode-014/v1
    by:      [internal/kg/implements/implements.go]
`))
	if err != nil {
		t.Fatalf("parse implements: %v", err)
	}
	claims, err := implements.Resolve(f, nil, "github.com/sunstoneinstitute/worklode")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	want := "https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/worklode"
	if len(claims) != 1 || claims[0].Component != want {
		t.Fatalf("claims = %+v; want one from %s", claims, want)
	}

	wholeRepo, err := manifest.Parse([]byte(`
repo: github.com/sunstoneinstitute/worklode
components:
  - iri: ` + want + `
    name: worklode
    paths: ["**"]
`))
	if err != nil {
		t.Fatalf("parse whole-repo manifest: %v", err)
	}
	explicit, err := implements.Resolve(f, wholeRepo, "github.com/sunstoneinstitute/worklode")
	if err != nil {
		t.Fatalf("Resolve with explicit manifest: %v", err)
	}
	if len(explicit) != 1 || explicit[0] != claims[0] {
		t.Fatalf("promotion changed the claim: implicit %+v, explicit %+v", claims, explicit)
	}
}

func TestResolveDeduplicatesAndRejectsConflictingPins(t *testing.T) {
	f, m := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/a.go]
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/b.go]
`)
	claims, err := implements.Resolve(f, m, "github.com/sunstoneinstitute/research-stack")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claims = %+v; want the duplicate collapsed", claims)
	}

	f2, _ := parseBoth(t, `
implements:
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v1
    by:      [internal/ingest/a.go]
  - section: wlid:section/spec-worklode-004/sec-4
    pinned:  wlid:doc/spec-worklode-004/v2
    by:      [internal/ingest/b.go]
`)
	if _, err := implements.Resolve(f2, m, "github.com/sunstoneinstitute/research-stack"); err == nil {
		t.Fatal("conflicting pins for one (component, section) resolved without error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/implements/`
Expected: FAIL — `undefined: implements.Resolve`, `undefined: implements.Claim` (via the tests)

- [ ] **Step 3: Write the implementation**

```go
package implements

import (
	"fmt"
	"sort"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

// Claim is one derived implementation claim: Component (derived from paths,
// 014 §6 — never declared) satisfies Section, validated against Pinned.
type Claim struct {
	Component string
	Section   string
	Pinned    string
}

// Resolve derives the claim set for a repository. m is the repo's
// components.yaml; nil means the single-component default, an implicit
// component whose IRI is the repo coordinates (unchanged when a whole-repo
// components.yaml later declares it — 014 §6). An entry whose paths span
// several components splits into one claim per component; a path matching no
// component is an error naming the path, because an unattributable claim is
// an uncheckable claim.
func Resolve(f *File, m *manifest.Manifest, repoCoords string) ([]Claim, error) {
	implicit := ""
	if m == nil {
		var err error
		implicit, err = iri.Component(repoCoords)
		if err != nil {
			return nil, fmt.Errorf("implicit component for %q: %w", repoCoords, err)
		}
	}

	seen := map[Claim]bool{}
	pins := map[[2]string]string{} // (component, section) -> pinned
	var out []Claim
	for _, e := range f.Implements {
		components := map[string]bool{}
		for _, p := range e.By {
			if m == nil {
				components[implicit] = true
				continue
			}
			c, ok := m.Match(p)
			if !ok {
				return nil, fmt.Errorf("implements: path %q matches no component in components.yaml", p)
			}
			components[c.IRI] = true
		}
		for comp := range components {
			key := [2]string{comp, e.Section}
			if prev, ok := pins[key]; ok && prev != e.Pinned {
				return nil, fmt.Errorf("implements: %s claims %s at both %s and %s — one pin per (component, section)",
					comp, e.Section, prev, e.Pinned)
			}
			pins[key] = e.Pinned
			c := Claim{Component: comp, Section: e.Section, Pinned: e.Pinned}
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Component != out[j].Component {
			return out[i].Component < out[j].Component
		}
		return out[i].Section < out[j].Section
	})
	return out, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/implements/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/implements
git commit -m "Derive implementation claims from paths via the component manifest"
```

---

## Task 10: Project claims as `wl:implements` triples

**Files:**
- Create: `internal/kg/implements/triples.go`
- Test: `internal/kg/implements/triples_test.go`

Depends on the runtime-layer plan's Task 6 (`graphproj.Triple`,
`graphproj.Render`). This is the pure half of 007's future
`observed/repo-implements` deriver; the deriver itself (input fetching,
named-graph PUT, triggers) belongs to spec 007's plan.

- [ ] **Step 1: Write the failing test**

```go
package implements_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/implements"
)

func TestTriples(t *testing.T) {
	claims := []implements.Claim{
		{
			Component: "https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/worklode",
			Section:   "https://worklode.io/ns/wl/id/section/spec-worklode-004/sec-4",
			Pinned:    "https://worklode.io/ns/wl/id/doc/spec-worklode-004/v2",
		},
		{
			Component: "https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/worklode",
			Section:   "https://worklode.io/ns/wl/id/section/spec-worklode-013/sec-3.1",
			Pinned:    "https://worklode.io/ns/wl/id/doc/spec-worklode-013/v1",
		},
	}
	got := string(graphproj.Render(implements.Triples(claims)))
	want := "<https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/worklode> " +
		"<https://worklode.io/ns/wl/ontology#implements> " +
		"<https://worklode.io/ns/wl/id/section/spec-worklode-004/sec-4> .\n" +
		"<https://worklode.io/ns/wl/id/component/github.com/sunstoneinstitute/worklode> " +
		"<https://worklode.io/ns/wl/ontology#implements> " +
		"<https://worklode.io/ns/wl/id/section/spec-worklode-013/sec-3.1> .\n"
	if got != want {
		t.Fatalf("Render = %q\nwant %q", got, want)
	}
}

// Two claims on one section differing only in pin (two entries pre-dedupe,
// or historic data) still emit one edge: the pin is not part of the edge.
func TestTriplesEdgeIsPinFree(t *testing.T) {
	claims := []implements.Claim{
		{Component: "https://x/c", Section: "https://x/s", Pinned: "https://x/d/v1"},
		{Component: "https://x/c", Section: "https://x/s", Pinned: "https://x/d/v2"},
	}
	got := string(graphproj.Render(implements.Triples(claims)))
	if want := "<https://x/c> <https://worklode.io/ns/wl/ontology#implements> <https://x/s> .\n"; got != want {
		t.Fatalf("Render = %q; want the single deduplicated edge %q", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/implements/`
Expected: FAIL — `undefined: implements.Triples`

- [ ] **Step 3: Write the implementation**

```go
package implements

import (
	"github.com/sunstoneinstitute/worklode/internal/graphproj"
	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

// Triples renders the claim set as <component> wl:implements <section>
// edges — the payload of 007's observed/repo-implements named graph.
//
// Only the edge is emitted. 014 §6 also wants the pinned version in the
// graph for the stale-claim query, but names no predicate or annotation
// encoding for a per-edge value; that mint belongs to the 006 vocabulary PR
// (see this plan's Overlaps section). Claims carry the pin in Go until then.
func Triples(claims []Claim) []graphproj.Triple {
	ts := make([]graphproj.Triple, 0, len(claims))
	for _, c := range claims {
		ts = append(ts, graphproj.Triple{
			S: c.Component,
			P: iri.Ontology + "implements",
			O: c.Section,
		})
	}
	return ts
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/implements/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/implements
git commit -m "Project implementation claims as wl:implements triples"
```

---

## Task 11: Full verification

- [ ] **Step 1: Build, vet, and the non-Postgres suites**

Run: `go build ./... && go vet ./... && go test ./internal/kg/... ./internal/cmd/`
Expected: clean, PASS.

- [ ] **Step 2: Postgres suites**

Run: `go test ./...`
Expected: PASS (store/api/cmd suites need the docker-compose Postgres, as
before).

- [ ] **Step 3: Docs grep**

Run: `grep -rnE '\b(ls|lsc|lsid):' docs/ ; grep -rn '/ls/' docs/`
Expected: no output.

- [ ] **Step 4: Acceptance-criteria walkthrough**

Confirm each criterion this plan owns maps to green evidence, and each it
does not maps to a named owner:

| AC | Status |
|---|---|
| 1 | docs half: Step 3 grep. rdf-registry half (`rdf/wl/`, publish base): runtime-layer plan + rdf-registry branch `worklode-io-spec` |
| 2 | callouts already applied (006:65,109,328); ontology absence of `wl:Plan` → 006 vocabulary PR |
| 3 | `iri.Section` + `TestParse`/`TestLintRules`; graph resolution → deferred |
| 3a | `TestCompareRenumberIsViolationInsertIsNot` |
| 4 | deferred — needs the graph server (009) |
| 5 | local gate: `TestCompareRemovedAnchorIsViolation`; SHACL form → 006 PR |
| 6 | edge set: Tasks 8–10; graph write + full-replace no-op → 007 deriver plan |
| 6a | `TestResolveImplicitComponent` |
| 6b | `TestResolveSplitsAcrossComponents`, `TestResolveUnmatchedPathIsError` |
| 6c | lint/diff halves: `TestLintDepthLimitIsConfigurable`, `TestCompareLoweredLimitNamesOrphans`; claim-vs-depth rejection needs the published section set → deferred with publication |
| 7 | deferred — coverage query needs the graph |
| 8 | `Changed` is the exact `wl:lastRevisedIn` input (`TestCompareChangedIsExact`); the staleness query → 007 |
| 9 | 006 vocabulary PR (concept scheme) |
| 10 | deferred — revision lifecycle |
| 11 | DB+API: `TestCreateTaskAllKinds`, `TestMigrateRoundTrip`; `wlc:TaskKind` → 006 PR |
| 12 | deferred — projection (`prov:wasGeneratedBy`) |

- [ ] **Step 5: Report the deliberate leftovers**

Restate in the completion summary what was intentionally not built, per the
Deferred table below, so nobody mistakes it for a gap.

---

## Deferred to owning specs' plans

| Work | Owner / blocker |
|---|---|
| `wl:Section`, `wl:lastRevisedIn`, `wl:DesignDoc`/`ADR`/`Spec` TTL, `wlc:DesignDocStatus` without `implemented`, `wlc:TaskKind` (six), disjointness updates, widened `wl:status` domain, DCAT/PROV version properties, §7 SHACL shapes | spec 006's rdf-registry PR — extends `rdf/wl/{ontology,concept}.ttl` and `rdf/shapes/wl-shapes.ttl` created by the runtime-layer plan; must fold in the 014 amendment callouts already sitting in 006 |
| ADR-0006 amendment (versioned sibling IRIs as a named exception) | same rdf-registry PR |
| Versioned named graphs, the single-transaction publication, `lode doc list/show/coverage/revise/publish`, `lode drift --docs`, the web view, crit-gated revisions, the server-side depth-limit setting | blocked on the graph server (spec 009, cross-repo) and 007's overview surface |
| The `observed/repo-implements` deriver (fetch manifests at branch head, named-graph PUT, push/schedule triggers) and the coverage/stale/orphan standing queries | spec 007's plan, consuming this plan's `implements` package |
| `prov:wasGeneratedBy` authorship projection (§9) | 006 projection work |
| Onboarding the existing `docs/specs/` corpus | candidate spec 015-successor ("spec 015" in 014's text, already taken by the runtime layer — the umbrella will need to assign the next free number); explicitly out of scope per 014 §Adoption |

## Overlaps and open questions

1. **`internal/kg/iri` vs `internal/graphproj` duplicate the runtime IRI
   grammar.** The platform-graph-design plan's `iri` package and the
   runtime-layer plan's `graphproj/iri.go` both mint
   artifact/deployment/environment/commit IRIs under the same base, with
   different behavior on bad input (`iri` validates and errors;
   `graphproj` percent-escapes). Two implementations of one published
   identifier scheme will drift. This plan stays out of the collision — it
   takes doc/section IRIs from `iri` and only `Triple`/`Render` from
   `graphproj` — but whichever plan lands second should consolidate to a
   single minting path (suggestion: `graphproj` re-exporting `iri`, keeping
   its escaping at the boundary).
2. **The pin has no graph encoding.** 014 §6 requires the deriver to emit
   "the pinned version for staleness testing" but mints no predicate for it,
   and §Amendments forbids a coverage-flavored predicate. 006 publishes RDF
   1.2 (umbrella, resolved questions), so a triple-term annotation on the
   `wl:implements` edge is the likely shape — but that is a vocabulary
   decision for the 006 PR. Until then `implements.Claim` carries the pin in
   Go and `Triples` emits only the edge.
3. **AC1 is literally unsatisfiable while prose describes the rename.**
   Resolved here by rewording historical mentions to the colon-free form
   (`` `ls` ``), including 014 §1's own table. If reviewers prefer keeping
   `ls:` verbatim in the rename table, AC1's wording needs a carve-out in the
   spec instead — one or the other, not both.
4. **Depth limit lives in a flag until the server setting exists.**
   §10 makes it a server setting because it governs expressible claims
   installation-wide. `lode doc anchors --depth-limit` is an interim
   authoring aid only; the publication path must read the server value, and
   the flag should then become an override-for-preview or be removed.
5. **Spec numbering collision.** 014 names "candidate spec 015" for
   onboarding, but 015 is already the runtime layer (and 016/017/018/019
   exist). Purely editorial; noting it so the next umbrella update assigns
   the real number.
