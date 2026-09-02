---
status: accepted
covers:
  - spec: docs/specs/007-drift-and-overview.md#sec-2.2
    coverage: partial
replaces:
  ".":
    - 2026-07-30-platform-graph-design.md
---
# Component-boundary manifest (spec 007 §2.2) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `internal/kg/manifest` — the parser and path→component matcher
for the per-repo component-boundary manifest `.worklode/components.yaml`
(spec 007 §2.2) — plus Worklode's own trivial whole-repo manifest, so the
observed-layer derivers (007 §2.1–§2.3) and the `implements` claim model
(025 §11) have the path→component index they all consume.

**Provenance:** These were Tasks 2–3 of `2026-07-30-platform-graph-design.md`,
orphaned when that plan was marked superseded: its Task 1 (`internal/kg/iri`)
was re-planned into `2026-07-30-knowledge-graph-1-graph-foundations.md`
(executed under WL-25, merged), and nothing succeeded the manifest tasks
(WL-109). This plan is that successor, carried onto the settled
`internal/kg/iri` API — `iri.Component(slug string) string`, plain-string and
non-validating, not the old `(string, error)` signature.

**Architecture:** One pure package with no dependency beyond
`gopkg.in/yaml.v3` (already a direct dependency — no `go.mod` change).
`Parse` validates YAML into `Manifest`/`Component`; `Load` adds the
`os.IsNotExist` contract so callers can treat "no manifest" distinctly (the
implicit single-component default — 007 §2.2, 025 §11); `Match` maps a
repo-relative slash-separated path to its owning component. Glob semantics:
`**` spans zero or more whole segments; every other segment goes through
`path.Match`, so `*` never crosses `/`; first match wins; `ok=false` means a
coverage gap, never an error. No server, store, or CLI change; nothing here
talks to graph-server.

**Coverage:** `partial` on 007 §2.2 — this plan fixes the manifest format and
the path→component index; the repo-layout deriver that emits §2.2's
`<repo> dct:hasPart <component>` output is
`2026-07-30-drift-and-overview-1-repo-derivers.md` Task 5, which (with
derivers 1 and 3) closes the section.

**Consumers (they only call; re-plan nothing here):**

- `2026-07-30-drift-and-overview-1-repo-derivers.md` Tasks 4–6 —
  `Load`/`Parse`, `(*Manifest).Match`, the unmatched-path gap report.
- `2026-07-30-drift-and-overview-2-server-derivers.md` — `manifest.Parse`
  over bytes fetched via `RepoReader.FileAt`, `m.Match` in
  `PRAffectsTriples`; assumes the `Component.IRI` field name.
- `2026-07-30-design-documents-as-graph-objects.md` Tasks 8–10 —
  `internal/kg/implements` derives the claiming component through `Match`,
  with the implicit whole-repo component when the file is absent.

**Tech Stack:** Go, standard-library testing, `gopkg.in/yaml.v3`.

**Spec:** `docs/specs/007-drift-and-overview.md` §2.2 — read the consolidated
view in `docs/specs/inlined/`.

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/kg/manifest/manifest.go` | Parse + validate `.worklode/components.yaml`; first-match-wins path→component matching with `**` globs. |
| `internal/kg/manifest/manifest_test.go` | Parse, validation failures, glob semantics, first-match-wins, repo self-manifest. |
| `.worklode/components.yaml` | Worklode's own manifest — the trivial whole-repo form (007 §2.2). |

**Test commands** (no Postgres needed):
`go test -trimpath ./internal/kg/...`; before each commit,
`make build && make vet`.

---

## Tasks

### Task 1 — Parse the component-boundary manifest with first-match-wins globs

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Create: `internal/kg/manifest/manifest.go`
- Create: `internal/kg/manifest/manifest_test.go`

Format per spec 007 §2.2: `repo` plus a list of components, each `iri` +
`name` + `paths` globs; **first-match-wins; unmatched paths belong to no
component** (the gap is the caller's to report). `**` matches zero or more
whole path segments; `*` matches within a segment.

- [ ] **Step 1: Write the failing test**

```go
package manifest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/manifest"
)

const twoComponents = `
repo: github.com/sunstoneinstitute/research-stack
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/ingest
    name: ingest
    paths: ["cmd/ingest/**", "internal/ingest/**"]
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/research-stack/pfas
    name: pfas
    paths: ["internal/**"]
`

func TestParseAndMatch(t *testing.T) {
	m, err := manifest.Parse([]byte(twoComponents))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.Repo != "github.com/sunstoneinstitute/research-stack" {
		t.Fatalf("Repo = %q", m.Repo)
	}

	cases := []struct {
		path string
		want string // component name; "" = no component
	}{
		{"cmd/ingest/main.go", "ingest"},
		{"cmd/ingest/deep/nested/file.go", "ingest"},
		{"internal/ingest/reader.go", "ingest"}, // first match wins over pfas's internal/**
		{"internal/pfas/model.go", "pfas"},
		{"internal/x.go", "pfas"},
		{"cmd/ingestx/main.go", ""}, // * and ** do not cross a segment name
		{"README.md", ""},           // unmatched: no component, reported as a gap by callers
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			c, ok := m.Match(tc.path)
			switch {
			case tc.want == "" && ok:
				t.Fatalf("Match(%q) = %s; want no component", tc.path, c.Name)
			case tc.want != "" && (!ok || c.Name != tc.want):
				t.Fatalf("Match(%q) = %v, %v; want %s", tc.path, c, ok, tc.want)
			}
		})
	}
}

func TestGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**", "anything/at/all.go", true},
		{"**", "top.go", true},
		{"cmd/ingest/**", "cmd/ingest", true}, // ** matches zero segments
		{"cmd/*/main.go", "cmd/ingest/main.go", true},
		{"cmd/*/main.go", "cmd/ingest/sub/main.go", false},
		{"internal/**/db/**", "internal/a/b/db/x.go", true},
		{"internal/**/db/**", "internal/db/x.go", true},
		{"internal/**/db/**", "internal/a/x.go", false},
		{"*.go", "main.go", true},
		{"*.go", "cmd/main.go", false},
	}
	for _, tc := range cases {
		m := mustParse(t, `
repo: r
components:
  - iri: https://worklode.io/ns/id/component/r/c
    name: c
    paths: ["`+tc.pattern+`"]
`)
		_, ok := m.Match(tc.path)
		if ok != tc.want {
			t.Errorf("pattern %q vs %q = %v; want %v", tc.pattern, tc.path, ok, tc.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	cases := []struct{ name, yaml string }{
		{"not yaml", "{nope"},
		{"missing repo", "components: [{iri: i, name: n, paths: ['**']}]"},
		{"no components", "repo: r\ncomponents: []"},
		{"component without iri", "repo: r\ncomponents: [{name: n, paths: ['**']}]"},
		{"component without name", "repo: r\ncomponents: [{iri: i, paths: ['**']}]"},
		{"component without paths", "repo: r\ncomponents: [{iri: i, name: n}]"},
		{"bad glob", "repo: r\ncomponents: [{iri: i, name: n, paths: ['[']}]"},
		{"duplicate name", "repo: r\ncomponents: [{iri: i1, name: n, paths: ['a/**']}, {iri: i2, name: n, paths: ['b/**']}]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if m, err := manifest.Parse([]byte(tc.yaml)); err == nil {
				t.Fatalf("Parse accepted %+v; want an error", m)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := manifest.Load(filepath.Join(t.TempDir(), "components.yaml")); !os.IsNotExist(err) {
		t.Fatalf("Load on a missing file: %v; want os.IsNotExist", err)
	}
}

func mustParse(t *testing.T, y string) *manifest.Manifest {
	t.Helper()
	m, err := manifest.Parse([]byte(y))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return m
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/kg/manifest/...`
Expected: FAIL — `no required module provides package .../internal/kg/manifest`

- [ ] **Step 3: Write the implementation**

```go
// Package manifest reads the per-repo component-boundary manifest
// .worklode/components.yaml (spec 007 §2.2 — the authoring burden the spec
// accepts). The manifest is the single place component boundaries are
// declared: it fixes each component's IRI-bearing slug, and its path globs
// are the path→component index the observed-layer derivers consume.
package manifest

import (
	"fmt"
	"os"
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// Component is one declared component and the path globs it owns.
type Component struct {
	IRI   string   `yaml:"iri"`
	Name  string   `yaml:"name"`
	Paths []string `yaml:"paths"`
}

// Manifest is a parsed .worklode/components.yaml.
type Manifest struct {
	Repo       string      `yaml:"repo"`
	Components []Component `yaml:"components"`
}

// Load reads and parses the manifest at p. A missing file surfaces as
// os.IsNotExist so callers can treat "no manifest" distinctly (a
// single-component repo may get a default instead — 007 §2.2).
func Load(p string) (*Manifest, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return m, nil
}

// Parse parses and validates manifest YAML: repo and at least one component
// are required; every component needs an iri, a unique name, and at least one
// well-formed glob.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse component manifest: %w", err)
	}
	if strings.TrimSpace(m.Repo) == "" {
		return nil, fmt.Errorf("component manifest: repo is required")
	}
	if len(m.Components) == 0 {
		return nil, fmt.Errorf("component manifest: at least one component is required")
	}
	seen := map[string]bool{}
	for i, c := range m.Components {
		if strings.TrimSpace(c.IRI) == "" || strings.TrimSpace(c.Name) == "" {
			return nil, fmt.Errorf("component manifest: component %d needs iri and name", i)
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("component manifest: duplicate component name %q", c.Name)
		}
		seen[c.Name] = true
		if len(c.Paths) == 0 {
			return nil, fmt.Errorf("component manifest: component %q needs at least one path glob", c.Name)
		}
		for _, g := range c.Paths {
			if err := checkGlob(g); err != nil {
				return nil, fmt.Errorf("component manifest: component %q: %w", c.Name, err)
			}
		}
	}
	return &m, nil
}

// Match maps a repo-relative, slash-separated path to its owning component.
// First match wins (007 §2.2); ok=false means the path belongs to no
// component — a gap the caller reports, never an error.
func (m *Manifest) Match(p string) (*Component, bool) {
	p = strings.TrimPrefix(p, "./")
	for i := range m.Components {
		for _, g := range m.Components[i].Paths {
			if matchGlob(g, p) {
				return &m.Components[i], true
			}
		}
	}
	return nil, false
}

// checkGlob rejects patterns path.Match cannot evaluate, at parse time rather
// than silently at match time.
func checkGlob(pattern string) error {
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "**" {
			continue
		}
		if _, err := path.Match(seg, "probe"); err != nil {
			return fmt.Errorf("bad glob %q: %w", pattern, err)
		}
	}
	return nil
}

// matchGlob matches a slash-separated path against a slash-separated pattern:
// "**" spans zero or more whole segments; any other segment follows
// path.Match, so "*" never crosses a "/".
func matchGlob(pattern, p string) bool {
	return matchSegs(strings.Split(pattern, "/"), strings.Split(p, "/"))
}

func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for skip := 0; skip <= len(segs); skip++ {
			if matchSegs(pat[1:], segs[skip:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	if ok, err := path.Match(pat[0], segs[0]); err != nil || !ok {
		return false
	}
	return matchSegs(pat[1:], segs[1:])
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -trimpath ./internal/kg/manifest/... -v`
Expected: PASS. `gopkg.in/yaml.v3` is already in `go.mod`'s direct require
block, so no `go mod tidy` churn is expected.

- [ ] **Step 5: Commit**

```bash
git add internal/kg/manifest
git commit -m "Parse the component-boundary manifest with first-match-wins globs"
```

### Task 2 — Declare Worklode's whole-repo component manifest

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `.worklode/components.yaml`
- Test: `internal/kg/manifest/manifest_test.go` (append)

Worklode is a single-component repo, so it gets the trivial whole-repo
manifest (007 §2.2). The test pins the file to the parser and to the IRI
scheme (`iri.Component`, plain-string — the settled KG-1 API), so the two
packages and the checked-in manifest cannot drift apart.

- [ ] **Step 1: Write the failing test**

Append to `internal/kg/manifest/manifest_test.go`:

```go
func TestWorklodeRepoManifest(t *testing.T) {
	m, err := manifest.Load(filepath.Join("..", "..", "..", ".worklode", "components.yaml"))
	if err != nil {
		t.Fatalf("load repo manifest: %v", err)
	}
	if m.Repo != "github.com/sunstoneinstitute/worklode" {
		t.Fatalf("Repo = %q", m.Repo)
	}
	c, ok := m.Match("internal/store/tasks.go")
	if !ok || c.Name != "worklode" {
		t.Fatalf("Match = %v, %v; want the whole-repo worklode component", c, ok)
	}
	if want := iri.Component(m.Repo); c.IRI != want {
		t.Fatalf("component IRI = %q; want %q (006 scheme)", c.IRI, want)
	}
}
```

Add `"github.com/sunstoneinstitute/worklode/internal/kg/iri"` to that test
file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -trimpath ./internal/kg/manifest/ -run TestWorklodeRepoManifest`
Expected: FAIL — `os.IsNotExist` on `.worklode/components.yaml`

- [ ] **Step 3: Write the manifest**

Create `.worklode/components.yaml`:

```yaml
# Component-boundary manifest (spec 007 §2.2). Worklode is a
# single-component repo: the whole-repo form.
repo: github.com/sunstoneinstitute/worklode
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode
    name: worklode
    paths: ["**"]
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test -trimpath ./internal/kg/... -v`
Expected: PASS

- [ ] **Step 5: Run the full non-Postgres check**

Run: `make build && make vet && go test -trimpath ./internal/kg/...`
Expected: PASS — this plan touches no store/API/cmd code, so the Postgres
suites are unaffected; run `make test` too if a local Postgres is up.

- [ ] **Step 6: Commit**

```bash
git add .worklode/components.yaml internal/kg/manifest/manifest_test.go
git commit -m "Declare Worklode's whole-repo component manifest"
```

---

## Out of scope (owned elsewhere)

- The observed-layer derivers that consume this index
  (`observed/go-imports`, `observed/repo-layout`, `observed/pr-affects`,
  `observed/deploy`) and the unmatched-path gap reporting — the
  drift-and-overview series (spec 007).
- `internal/kg/implements` and the implicit whole-repo component when no
  manifest exists — `2026-07-30-design-documents-as-graph-objects.md`.
- The IRI grammar itself — `internal/kg/iri`, owned by
  `2026-07-30-knowledge-graph-1-graph-foundations.md` (landed).
