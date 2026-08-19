---
status: superseded
covers: docs/specs/006-knowledge-graph.md
isReplacedBy:
  ".":
    - 2026-07-30-knowledge-graph-1-graph-foundations.md
    - 2026-08-19-component-boundary-manifest.md
---
# Platform graph design record (spec 003) — Implementation Plan

> **Superseded (WL-109).** Task 1 (`internal/kg/iri`) was re-planned into
> `2026-07-30-knowledge-graph-1-graph-foundations.md` (executed under WL-25,
> merged, with a plain-string API replacing this plan's `(string, error)`
> signature). Tasks 2–3 (`internal/kg/manifest`, `.worklode/components.yaml`)
> carried forward into `2026-08-19-component-boundary-manifest.md`. The
> original `status: superseded` stamp (commit `9a698a9`) named no successor;
> this note makes the replacement deliberate.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the implementable gap spec 003 still owns in this repo: record
which of D1–D15 already shipped, and build the graph-server-independent
knowledge-graph groundwork — the `wl` IRI scheme and the component-boundary
manifest — that the future spec 006/007 plans consume.

**Architecture:** Spec 003 is a graduated design record: its backbone half
(D8–D12, D14, D15) shipped via migrations 0001–0005 and the existing plans;
its knowledge-graph half lives in specs 006/007/014/015 plus the cross-repo
009 hand-off, each an independent spec → plan cycle. This plan implements only
the two pure, dependency-free pieces every one of those cycles needs and none
yet owns: a new `internal/kg/iri` package minting canonical instance IRIs
(006 §Canonical IRI scheme, base per 025 §17, runtime grammar per 006 §10.1), and
a new `internal/kg/manifest` package parsing `.worklode/components.yaml`
(007 §1, the authoring burden accepted in D5), plus Worklode's own trivial
whole-repo manifest. No server, store, or CLI change; nothing here talks to
graph-server.

**Tech Stack:** Go 1.26, standard-library testing, `gopkg.in/yaml.v3`
(already in `go.sum` as an indirect dependency; becomes direct).

**Spec:** `docs/specs/006-knowledge-graph.md` — read with its
amendments: `docs/specs/025-documents-in-the-backbone.md` §1 (prefix
`wl:`/`wlc:`/`wlid:` under `https://worklode.io/ns/`) and
`docs/specs/006-knowledge-graph.md` §5 (kind-first runtime IRI grammar).

---

## Implementation status (D1–D15)

Spec 003's header says "partially implemented"; this is the ground truth per
decision, with code anchors. "Backbone half" = this repo's Postgres side;
"graph half" = the RDF knowledge graph.

| Decision | Status | Evidence / owner of the remainder |
|---|---|---|
| D1–D3 two stores, authority split | Backbone **shipped**; graph store **not started** | `deploy/base/migrations/0001_baseline.up.sql`, `internal/store/`; graph side → specs 006 + 009 |
| D4 vocabulary (`wl:` mint set) | **Not started** | rdf-registry PR, owned by spec 006 (mint set in 006 §Acceptance 2, renamed by 025 §17) |
| D5 two-layer graph, drift, per-repo manifest | **Not started** except the manifest groundwork in this plan | derivers + diff → spec 007 |
| D6 three layers, v1/v2 | Relational ingestion **shipped** (`internal/hooks/github.go`, `flux.go`, `push.go`, `deployment.go`); graph projection **not started** | projection → spec 006 |
| D7 Deliverable = declared definition-of-done | **Reserved, not built** — `internal/store/brief.go:26` keeps `DefinitionOfDone` nil for v1 | model → spec 006 |
| D8 commit-cadence heartbeat, `claim --next` | **Shipped** | `internal/hookrun/hookrun.go` (renew), `internal/cmd/task.go:461` (`--next`), `e2e/next_test.go`, `e2e/pickup_test.go` |
| D9 ranking + `--strict-focus` | **Shipped** | `internal/store/ranking.go:174-179` — `(is_critical, concern_rank, priority, fan_out)`, strict-focus variant |
| D10 `concern` enum + `project.focus` | **Shipped** | `deploy/base/migrations/0002_prioritization.up.sql` |
| D11 Task-as-bridge | Backbone side **shipped**; graph mirror **not started** | projection → spec 006 |
| D12 estimate-free | **Holds** — no effort columns exist; nothing to build | — |
| D13 naming (Worklode / `lode`) | **Done** | repo + `cmd/lode/` |
| D14 plugin: worktree-bound leases, hooks, daisy-chain | **Shipped** | `internal/worktree/worktree.go:21` (`wt/<id>-<slug>`), `internal/cmd/claude.go`, `internal/cmd/githooks.go`, plan `docs/plans/2026-07-24-worklode-plugin.md` |
| D15 `needs-decomposition` sizing | **Shipped** except the server-side-configurable ~100k budget knob | `0002_prioritization.up.sql:3`, `internal/cmd/task.go:244`; the budget is a guide for an agentic call (005 §sizing) with no mechanical consumer yet — deliberately unbuilt until one exists |

## What remains, and where it belongs

The whole remaining surface of 003 is the knowledge-graph half. Per the
umbrella (spec 000 §Spec map), each owning spec gets its own plan; this plan
must not pre-empt them:

- **Spec 006** — `wl:` ontology PR to rdf-registry; the backbone→graph
  projector (blocked on 009 must-haves 1, 2, 4, 5).
- **Spec 007** — observed-layer derivers and standing drift queries
  (needs 006).
- **Spec 014 / 015** — design-doc objects and runtime-layer nodes (need 006).
- **Spec 009** — prod graph-server, SPARQL read path, write auth: a
  **cross-repo hand-off to the data-platform team**, not implementable here.

What *is* implementable now, independent of graph-server, and required by all
of the above: stable IRI minting and the component-boundary manifest. That is
this plan's build scope.

---

## File Structure

**New files**

| Path | Responsibility |
|---|---|
| `internal/kg/iri/iri.go` | Mint `wl` instance IRIs from relational natural keys; namespace constants. Pure, no deps. |
| `internal/kg/iri/iri_test.go` | Table test: every instance pattern against the spec's own examples; rejections. |
| `internal/kg/manifest/manifest.go` | Parse + validate `.worklode/components.yaml`; first-match-wins path→component matching with `**` globs. |
| `internal/kg/manifest/manifest_test.go` | Parse, validation failures, glob semantics, first-match-wins, repo self-manifest. |
| `.worklode/components.yaml` | Worklode's own manifest — the trivial whole-repo form (007 §1). |

**Modified files**

| Path | Change |
|---|---|
| `go.mod` / `go.sum` | `gopkg.in/yaml.v3` promoted from indirect to direct (`go mod tidy`). |

**Test commands**

- This plan's packages (no Postgres needed): `go test ./internal/kg/...`
- Everything: `go test ./...` (store/API/cmd suites need Postgres via `store.OpenTestStore`)

---

## Task 1: The `wl` IRI scheme

**Files:**
- Create: `internal/kg/iri/iri.go`
- Test: `internal/kg/iri/iri_test.go`

The grammar is fixed by spec 006 §Canonical IRI scheme, with the base from
025 §17 (`https://worklode.io/ns/`) and the runtime patterns from 006 §10.1
(kind-first Artifact; Deployment/Environment/Commit mirror their tables'
natural keys). Instance IRIs are branch-free and version-free; slashes inside
a local id are permissible (slash namespace, opaque path).

- [ ] **Step 1: Write the failing test**

```go
package iri_test

import (
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
)

func TestNamespaces(t *testing.T) {
	if iri.Base != "https://worklode.io/ns/" {
		t.Fatalf("Base = %q; want the 025 §17 wl base", iri.Base)
	}
	if iri.Ontology != iri.Base+"ontology#" || iri.Concept != iri.Base+"concept/" {
		t.Fatalf("Ontology/Concept = %q / %q; want hash + slash namespaces", iri.Ontology, iri.Concept)
	}
}

func TestInstanceIRIs(t *testing.T) {
	const b = "https://worklode.io/ns/id/"
	cases := []struct {
		name string
		got  func() (string, error)
		want string
	}{
		{"component", func() (string, error) {
			return iri.Component("github.com/sunstoneinstitute/worklode")
		}, b + "component/github.com/sunstoneinstitute/worklode"},
		{"multi-component repo", func() (string, error) {
			return iri.Component("github.com/sunstoneinstitute/research-stack/pfas")
		}, b + "component/github.com/sunstoneinstitute/research-stack/pfas"},
		{"doc", func() (string, error) {
			return iri.Doc("spec-worklode-006")
		}, b + "doc/spec-worklode-006"},
		{"task", func() (string, error) {
			return iri.Task("WL-42")
		}, b + "task/WL-42"},
		{"deliverable", func() (string, error) {
			return iri.Deliverable("worklode-graph-live")
		}, b + "deliverable/worklode-graph-live"},
		{"issue", func() (string, error) {
			return iri.Issue("github.com", "sunstoneinstitute", "worklode", 7)
		}, b + "issue/github.com/sunstoneinstitute/worklode/7"},
		{"pr", func() (string, error) {
			return iri.PR("github.com", "sunstoneinstitute", "worklode", 42)
		}, b + "pr/github.com/sunstoneinstitute/worklode/42"},
		{"artifact kind-first (006 §10.1)", func() (string, error) {
			return iri.Artifact("docker_image", "ghcr.io/sunstoneinstitute/graph-server", "v1")
		}, b + "artifact/docker_image/ghcr.io/sunstoneinstitute/graph-server/v1"},
		{"artifact pypi", func() (string, error) {
			return iri.Artifact("pypi", "sunstone-py", "0.4.1")
		}, b + "artifact/pypi/sunstone-py/0.4.1"},
		{"deployment", func() (string, error) {
			return iri.Deployment("prod", "flux_kustomization", "graph-server")
		}, b + "deployment/prod/flux_kustomization/graph-server"},
		{"environment", func() (string, error) {
			return iri.Environment("prod")
		}, b + "environment/prod"},
		{"commit", func() (string, error) {
			return iri.Commit("github.com", "sunstoneinstitute", "worklode", "a16c2a7")
		}, b + "commit/github.com/sunstoneinstitute/worklode/a16c2a7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestInstanceIRIRejects(t *testing.T) {
	cases := []struct {
		name string
		got  func() (string, error)
	}{
		{"empty slug", func() (string, error) { return iri.Component("") }},
		{"blank slug", func() (string, error) { return iri.Task("  ") }},
		{"whitespace inside", func() (string, error) { return iri.Doc("spec 006") }},
		{"fragment char", func() (string, error) { return iri.Deliverable("a#b") }},
		{"query char", func() (string, error) { return iri.Environment("prod?x") }},
		{"empty middle part", func() (string, error) { return iri.Artifact("pypi", "", "1.0") }},
		{"non-positive number", func() (string, error) { return iri.PR("github.com", "o", "r", 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.got()
			if err == nil {
				t.Fatalf("minted %q; want an error", got)
			}
			if !strings.Contains(err.Error(), "iri") {
				t.Fatalf("error %q does not identify the iri package", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/...`
Expected: FAIL — `no required module provides package .../internal/kg/iri`

- [ ] **Step 3: Write the implementation**

```go
// Package iri mints Worklode knowledge-graph IRIs: the wl namespaces and the
// canonical instance grammar of spec 006 §Canonical IRI scheme, with the base
// of spec 025 §17 and the runtime patterns of spec 006 §10.1.
//
// An instance IRI mirrors the relational natural key, so projection stays a
// pure function of the row (006 §10.1). IRIs are branch-free and version-free;
// slashes inside a local id are permissible (slash namespace, opaque path).
// Parts are validated, not escaped: every natural key today is IRI-safe, and
// escaping would silently change the published identifier.
package iri

import (
	"fmt"
	"strconv"
	"strings"
)

// The wl namespaces (025 §17): schema is a hash namespace, concepts and
// instances are slash namespaces.
const (
	Base     = "https://worklode.io/ns/"
	Ontology = Base + "ontology#"
	Concept  = Base + "concept/"
	instance = Base + "id/"
)

// Component returns the IRI for a component slug — by default the repo
// coordinates, or coords/<sub> in a multi-component repo. The slug is fixed
// by the repo's .worklode/components.yaml so the IRI survives layout shifts.
func Component(slug string) (string, error) { return mint("component", slug) }

// Doc returns the IRI for a design document by its stable design-file slug.
func Doc(slug string) (string, error) { return mint("doc", slug) }

// Task returns the IRI for a backbone task id (e.g. "WL-42").
func Task(id string) (string, error) { return mint("task", id) }

// Deliverable returns the IRI for a declared definition-of-done (D7).
func Deliverable(slug string) (string, error) { return mint("deliverable", slug) }

// Issue returns the IRI for a forge issue.
func Issue(host, org, repo string, number int64) (string, error) {
	n, err := positive(number)
	if err != nil {
		return "", err
	}
	return mint("issue", host, org, repo, n)
}

// PR returns the IRI for a pull request.
func PR(host, org, repo string, number int64) (string, error) {
	n, err := positive(number)
	if err != nil {
		return "", err
	}
	return mint("pr", host, org, repo, n)
}

// Artifact returns the kind-first artifact IRI of 006 §10.1, mirroring the
// artifacts table's UNIQUE (kind, name, version).
func Artifact(kind, name, version string) (string, error) {
	return mint("artifact", kind, name, version)
}

// Deployment mirrors the deployments natural key (environment, target kind,
// target name) — stable and predictable before the work exists, which is what
// lets an Effect name its deployment as the declared target (006 §Deliverable).
func Deployment(environment, targetKind, targetName string) (string, error) {
	return mint("deployment", environment, targetKind, targetName)
}

// Environment returns the IRI for an environment name (e.g. "prod").
func Environment(name string) (string, error) { return mint("environment", name) }

// Commit returns the commit IRI of 006 §10.1.
func Commit(host, org, repo, sha string) (string, error) {
	return mint("commit", host, org, repo, sha)
}

// mint joins validated parts under the instance namespace.
func mint(typ string, parts ...string) (string, error) {
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return "", fmt.Errorf("iri: empty %s part", typ)
		}
		if strings.ContainsAny(p, " \t\n#?%") {
			return "", fmt.Errorf("iri: %s part %q contains a character unsafe in an IRI path", typ, p)
		}
	}
	return instance + typ + "/" + strings.Join(parts, "/"), nil
}

// positive renders a 1-based forge number, rejecting zero and negatives.
func positive(n int64) (string, error) {
	if n <= 0 {
		return "", fmt.Errorf("iri: number %d is not positive", n)
	}
	return strconv.FormatInt(n, 10), nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/kg/iri
git commit -m "Mint canonical wl instance IRIs"
```

---

## Task 2: The component-boundary manifest

**Files:**
- Create: `internal/kg/manifest/manifest.go`
- Test: `internal/kg/manifest/manifest_test.go`

Format per spec 007 §1: `repo` plus a list of components, each `iri` + `name`
+ `paths` globs; **first-match-wins; unmatched paths belong to no component**
(the gap is the caller's to report). `**` matches zero or more whole path
segments; `*` matches within a segment.

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

Run: `go test ./internal/kg/manifest/...`
Expected: FAIL — `no required module provides package .../internal/kg/manifest`

- [ ] **Step 3: Write the implementation**

```go
// Package manifest reads the per-repo component-boundary manifest
// .worklode/components.yaml (spec 007 §1; the authoring burden accepted in
// D5). The manifest is the single place component boundaries are declared: it
// fixes each component's IRI-bearing slug, and its path globs are the
// path→component index the observed-layer derivers consume.
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
// single-component repo may get a default instead — 007 §1).
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
// First match wins (007 §1); ok=false means the path belongs to no component
// — a gap the caller reports, never an error.
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

- [ ] **Step 4: Promote the yaml dependency**

Run: `go mod tidy`
Expected: `gopkg.in/yaml.v3` moves to the direct `require` block of `go.mod`;
no other change.

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/kg/manifest/... -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/kg/manifest go.mod go.sum
git commit -m "Parse the component-boundary manifest with first-match-wins globs"
```

---

## Task 3: Worklode's own manifest

**Files:**
- Create: `.worklode/components.yaml`
- Test: `internal/kg/manifest/manifest_test.go` (append)

Worklode is a single-component repo, so it gets the trivial whole-repo
manifest (007 §1). The test pins the file to the parser and to the IRI
scheme, so the two packages and the checked-in manifest cannot drift apart.

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
	want, err := iri.Component(m.Repo)
	if err != nil {
		t.Fatalf("mint component IRI: %v", err)
	}
	if c.IRI != want {
		t.Fatalf("component IRI = %q; want %q (006 scheme)", c.IRI, want)
	}
}
```

Add `"github.com/sunstoneinstitute/worklode/internal/kg/iri"` to that test
file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/kg/manifest/ -run TestWorklodeRepoManifest`
Expected: FAIL — `os.IsNotExist` on `.worklode/components.yaml`

- [ ] **Step 3: Write the manifest**

Create `.worklode/components.yaml`:

```yaml
# Component-boundary manifest (spec 007 §1; authoring burden accepted in
# spec 003 D5). Worklode is a single-component repo: the whole-repo form.
repo: github.com/sunstoneinstitute/worklode
components:
  - iri: https://worklode.io/ns/id/component/github.com/sunstoneinstitute/worklode
    name: worklode
    paths: ["**"]
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/kg/... -v`
Expected: PASS

- [ ] **Step 5: Run the full non-Postgres check**

Run: `go build ./... && go vet ./... && go test ./internal/kg/... ./internal/repourl/...`
Expected: PASS — this plan touches no store/API/cmd code, so the Postgres
suites are unaffected; run `go test ./...` too if a local Postgres is up.

- [ ] **Step 6: Commit**

```bash
git add .worklode/components.yaml internal/kg/manifest/manifest_test.go
git commit -m "Declare Worklode's whole-repo component manifest"
```

---

## Out of scope (deferred to owning specs' plans)

- `wl:` ontology / SKOS / SHACL sources and the rdf-registry PR — spec 006.
- The backbone→graph projector service and Workstream named graphs — spec 006
  (blocked on spec 009 must-haves: prod graph-server, SPARQL path, write
  auth, fixed branch).
- Observed-layer derivers (`observed/go-imports`, `observed/repo-layout`,
  `observed/pr-affects`, `observed/deploy`) and the standing drift queries —
  spec 007; they will consume this plan's `iri` and `manifest` packages.
- Deliverable modelling, `DefinitionOfDone` in `internal/store/brief.go` —
  spec 006.
- Design-doc graph objects and section IRIs — spec 014; runtime nodes —
  spec 015.
- The server-side-configurable ~100k decomposition budget (D15) — spec 005
  remainder; unbuilt until something consumes it mechanically.
