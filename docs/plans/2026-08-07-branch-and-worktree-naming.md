---
status: accepted
implements: docs/specs/030-branch-and-worktree-naming.md
---
# Branch and worktree naming — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the fixed `lode/<id>-<slug>` branch prefix with a
server-rendered Go template defaulting to `{{ .id }}-{{ .slug }}`, and move
worktrees from `<git-root>/wt/` to a configurable base directory defaulting to
`.worktrees`.

**Architecture:** The server owns branch names (spec 030 §1) — it renders the
template and hands the result to clients, who never render one. The correlation
pattern that reverses a branch back to a task id is derived from the same
template at configuration time (§2). The worktree path is the client's
concern: `<git-root>/<worktree_dir>/<branch>`, with the base directory read
from repo-local config (§3). The hook guard becomes "is there a `<base>`
segment in this path, and an ID-shaped substring below it".

**Tech Stack:** Go 1.x, `text/template`, `regexp`, cobra, pgx. No new
dependencies.

## Global Constraints

- **Clean break, no legacy recognition** (spec 030 §5). `wl/`, `lode/`, and
  `wt/` disappear entirely. Do not add compatibility fallbacks.
- **Default branch template:** `{{ .id }}-{{ .slug }}` — exact string.
- **Default worktree base:** `.worktrees` — exact string.
- **Template fields:** `.id`, `.slug`, `.projectId`, `.kind`. Rendering uses
  `missingkey=error`. `.projectId` and `.kind` are sanitized to the slug
  charset at render, so a template using them always yields a legal git ref.
- **The server is the only renderer.** No client-side template evaluation.
- Every backbone call in `internal/hookrun` stays best-effort: a hook must
  never fail an event, so config errors there downgrade to the default, never
  to an error return.
- No new Prometheus metrics — this change adds no endpoint, loop, outbound
  call, or store operation.
- Run `go build ./... && go vet ./...` before each commit. Store tests need
  Postgres with pgvector; they skip silently when it is unreachable, so a
  green run without it proves less than it looks like.

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/branchname.go` (new) | Template parsing, validation, rendering, and the derived correlation regex. Extracted from `changes.go` because it is a self-contained unit with its own test surface. |
| `internal/store/changes.go` (modify) | Drops the prefix globals; `TaskIDFromRef` delegates to the new file. |
| `internal/store/tasks.go` (modify) | `BranchFor` renders the template. |
| `internal/api/server.go`, `internal/api/lifecycle.go` (modify) | `Config.BranchTemplate`; both hand-built branch strings collapse to `store.BranchFor`. |
| `internal/cmd/serve.go` (modify) | Reads `LODE_BRANCH_TEMPLATE`. |
| `internal/worktree/worktree.go` (modify) | `Layout` type: base-dir validation, `Dir`, `ParseDir`. |
| `internal/cli/client.go` (modify) | `Config.WorktreeDir` from `worktree_dir` / `LODE_WORKTREE_DIR`. |
| `internal/cmd/lifecycle.go`, `internal/cmd/task.go` (modify) | Thread the layout through `next`/`resume`/`done`/`block`/`status`. |
| `internal/hookrun/hookrun.go` (modify) | Thread the layout through all nine guard sites; `offerScan` walks the base directory. |

---

## Tasks

### Task 1 — Branch-name template in the store

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Create `internal/store/branchname.go` holding the template, its validation, and
the derived correlation regex. Delete the prefix machinery from
`internal/store/changes.go:50-93` (`branchPrefix`, `branchPrefixPattern`,
`DefaultBranchPrefix`, `buildBranchPattern`, `SetBranchPrefix`, `BranchPrefix`)
and move `TaskIDFromRef` (`changes.go:95-107`) into the new file. Update
`BranchFor` in `internal/store/tasks.go:437-442`.

Keep the package-level, mutex-guarded shape the prefix code already used —
webhook handlers read this concurrently, and the existing `branchPatternMu`
pattern is what the rest of the package expects.

**Interfaces produced** (Tasks 2 and 4 depend on these exact names):

```go
const DefaultBranchTemplate = "{{ .id }}-{{ .slug }}"
func SetBranchTemplate(text string) error   // "" ⇒ DefaultBranchTemplate
func BranchTemplate() string
func BranchFor(t *Task) string
func TaskIDFromRef(ref string) string       // unchanged signature
```

- [ ] **Step 1: Write the failing tests**

Create `internal/store/branchname_test.go`:

```go
package store

import (
	"regexp"
	"strings"
	"testing"
)

func TestSetBranchTemplateValid(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	cases := []struct{ name, tmpl, want string }{
		{"default", "", "WL-7-fix-the-thing"},
		{"explicit default", DefaultBranchTemplate, "WL-7-fix-the-thing"},
		{"namespaced", "lode/{{ .id }}-{{ .slug }}", "lode/WL-7-fix-the-thing"},
		{"id only", "{{ .id }}", "WL-7"},
		{"projectId", "{{ .projectId }}/{{ .id }}-{{ .slug }}", "worklode/WL-7-fix-the-thing"},
		{"kind", "{{ .kind }}/{{ .id }}-{{ .slug }}", "feature/WL-7-fix-the-thing"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := SetBranchTemplate(c.tmpl); err != nil {
				t.Fatalf("SetBranchTemplate(%q) = %v, want nil", c.tmpl, err)
			}
			task := &Task{ID: "WL-7", Title: "Fix the thing", ProjectID: "worklode", Kind: "feature"}
			if got := BranchFor(task); got != c.want {
				t.Errorf("BranchFor = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSetBranchTemplateRejects(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	cases := []struct{ name, tmpl string }{
		{"unparseable", "{{ .id "},
		{"unknown field", "{{ .nope }}-{{ .id }}"},
		{"no id reference", "{{ .slug }}"},
		{"empty render", "{{ if false }}{{ .id }}{{ end }}"},
		{"space", "{{ .id }} {{ .slug }}"},
		{"double dot", "{{ .id }}..{{ .slug }}"},
		{"double slash", "a//{{ .id }}"},
		{"leading slash", "/{{ .id }}"},
		{"trailing slash", "{{ .id }}/"},
		{"tilde", "~{{ .id }}"},
		{"caret", "^{{ .id }}"},
		{"colon", "a:{{ .id }}"},
		{"question", "{{ .id }}?"},
		{"star", "{{ .id }}*"},
		{"bracket", "{{ .id }}["},
		{"backslash", `{{ .id }}\x`},
		{"at-brace", "{{ .id }}@{x"},
		{"lock suffix", "{{ .id }}.lock"},
		{"dot-leading component", ".{{ .id }}"},
		{"dot-trailing component", "{{ .id }}./x"},
		{"control char", "{{ .id }}\x01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := SetBranchTemplate(c.tmpl); err == nil {
				t.Errorf("SetBranchTemplate(%q) = nil, want error", c.tmpl)
			}
		})
	}
}

// TestBranchRoundTrip is the property that makes the derived pattern
// trustworthy: whatever the template, a branch it renders must parse back to
// the id it was rendered from.
func TestBranchRoundTrip(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	tmpls := []string{
		DefaultBranchTemplate,
		"lode/{{ .id }}-{{ .slug }}",
		"{{ .projectId }}/{{ .id }}-{{ .slug }}",
		"{{ .kind }}/{{ .id }}",
		"{{ .id }}",
	}
	tasks := []*Task{
		{ID: "WL-7", Title: "Fix the thing", ProjectID: "worklode", Kind: "feature"},
		{ID: "SW-1234", Title: "A much longer title that will be truncated somewhere", ProjectID: "sw", Kind: "bug"},
		{ID: "X9-1", Title: "!!!", ProjectID: "p", Kind: "chore"},
	}
	for _, tmpl := range tmpls {
		if err := SetBranchTemplate(tmpl); err != nil {
			t.Fatalf("SetBranchTemplate(%q) = %v", tmpl, err)
		}
		for _, task := range tasks {
			branch := BranchFor(task)
			if got := TaskIDFromRef(branch); got != task.ID {
				t.Errorf("template %q: TaskIDFromRef(%q) = %q, want %q", tmpl, branch, got, task.ID)
			}
		}
	}
}

func TestTaskIDFromRefRejects(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate(""); err != nil {
		t.Fatal(err)
	}
	// Legacy prefixes are gone (spec 030 §5); a lowercase id never matches;
	// a bare id has no slug separator under the default template.
	for _, ref := range []string{"lode/WL-7-x", "wl/WL-7-x", "wl-7-x", "main", "WL-7", "feature/WL-7-x"} {
		if got := TaskIDFromRef(ref); got != "" {
			t.Errorf("TaskIDFromRef(%q) = %q, want \"\"", ref, got)
		}
	}
}

func TestDerivedPatternEscapesLiterals(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("a.b-{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	// The "." is a literal, not a wildcard.
	if got := TaskIDFromRef("axb-WL-7-x"); got != "" {
		t.Errorf("TaskIDFromRef(\"axb-WL-7-x\") = %q, want \"\" (dot must be literal)", got)
	}
	if got := TaskIDFromRef("a.b-WL-7-x"); got != "WL-7" {
		t.Errorf("TaskIDFromRef(\"a.b-WL-7-x\") = %q, want WL-7", got)
	}
}

func TestBranchTemplateReportsCurrent(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate("lode/{{ .id }}-{{ .slug }}"); err != nil {
		t.Fatal(err)
	}
	if got := BranchTemplate(); got != "lode/{{ .id }}-{{ .slug }}" {
		t.Errorf("BranchTemplate() = %q", got)
	}
}

// guard against the sentinel leaking into the compiled pattern
func TestDerivedPatternHasNoSentinel(t *testing.T) {
	t.Cleanup(func() { SetBranchTemplate("") })
	if err := SetBranchTemplate(""); err != nil {
		t.Fatal(err)
	}
	branchMu.RLock()
	src := branchPattern.String()
	branchMu.RUnlock()
	if strings.Contains(src, "\x00") {
		t.Errorf("derived pattern still contains a sentinel: %q", src)
	}
	if _, err := regexp.Compile(src); err != nil {
		t.Errorf("derived pattern does not compile: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/store -run 'TestSetBranchTemplate|TestBranchRoundTrip|TestTaskIDFromRef|TestDerivedPattern|TestBranchTemplate'`
Expected: compile failure — `undefined: SetBranchTemplate`, `undefined: DefaultBranchTemplate`, `undefined: branchMu`.

- [ ] **Step 3: Write `internal/store/branchname.go`**

```go
package store

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"
)

// DefaultBranchTemplate is the branch name Worklode hands out when
// LODE_BRANCH_TEMPLATE is unset (spec 030 §1).
const DefaultBranchTemplate = "{{ .id }}-{{ .slug }}"

// The branch template and the correlation pattern derived from it are read by
// webhook handlers concurrently, so both live behind one lock and are only
// ever replaced together.
var (
	branchMu      sync.RWMutex
	branchText    = DefaultBranchTemplate
	branchTmpl    = mustTemplate(DefaultBranchTemplate)
	branchPattern = mustPattern(DefaultBranchTemplate)
)

// branchFields are the template's fields. A field's zero value is never
// meaningful, so every render supplies all of them. There is no bare
// ".project": a project id, name, and key are three different things, and
// Task only ever carries an id, so .projectId is the only project-shaped
// field exposed.
type branchFields struct{ id, slug, projectID, kind string }

func (f branchFields) asMap() map[string]string {
	return map[string]string{"id": f.id, "slug": f.slug, "projectId": f.projectID, "kind": f.kind}
}

// sentinels are substituted for the fields when deriving the correlation
// pattern. NUL cannot survive validateRef, so a sentinel can never collide
// with a literal in an accepted template (spec 030 §2).
var sentinels = branchFields{
	id:        "\x00id\x00",
	slug:      "\x00slug\x00",
	projectID: "\x00projectId\x00",
	kind:      "\x00kind\x00",
}

// sampleFields render a representative branch for validation. The values are
// shaped like real ones: ids are uppercase-alpha + "-" + digits, and
// SlugifyTitle only ever emits [a-z0-9-].
var sampleFields = branchFields{id: "WL-1", slug: "sample-title", projectID: "sample", kind: "feature"}

func parseBranchTemplate(text string) (*template.Template, error) {
	return template.New("branch").Option("missingkey=error").Parse(text)
}

func mustTemplate(text string) *template.Template {
	t, err := parseBranchTemplate(text)
	if err != nil {
		panic("store: default branch template is invalid: " + err.Error())
	}
	return t
}

func mustPattern(text string) *regexp.Regexp {
	re, err := derivePattern(mustTemplate(text))
	if err != nil {
		panic("store: default branch template has no derivable pattern: " + err.Error())
	}
	return re
}

func render(t *template.Template, f branchFields) (string, error) {
	var b strings.Builder
	if err := t.Execute(&b, f.asMap()); err != nil {
		return "", err
	}
	return b.String(), nil
}

// derivePattern builds the branch → task-id pattern from the template itself:
// render with sentinels, quote the literal parts, then swap the sentinels for
// the field patterns (spec 030 §2).
func derivePattern(t *template.Template) (*regexp.Regexp, error) {
	out, err := render(t, sentinels)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(out, sentinels.id) {
		return nil, fmt.Errorf("template does not reference .id, so its branches cannot be correlated to a task")
	}
	pat := regexp.QuoteMeta(out)
	pat = strings.ReplaceAll(pat, regexp.QuoteMeta(sentinels.id), `([A-Z][A-Z0-9]*-[0-9]+)`)
	for _, s := range []string{sentinels.slug, sentinels.projectID, sentinels.kind} {
		pat = strings.ReplaceAll(pat, regexp.QuoteMeta(s), `[^/]*`)
	}
	return regexp.Compile("^" + pat + "$")
}

// refBadChars are the characters git refuses in a ref name.
var refBadChars = regexp.MustCompile(`[\x00-\x20\x7f ~^:?*\[\\]`)

// validateRef reports why ref is not a legal git branch name, or nil.
// Mirrors git check-ref-format, which is not shelled out to because the
// server does not otherwise need a git binary (spec 030 §1.2).
func validateRef(ref string) error {
	switch {
	case ref == "":
		return fmt.Errorf("renders to an empty branch name")
	case refBadChars.MatchString(ref):
		return fmt.Errorf("renders %q, which contains a character git forbids in a ref", ref)
	case strings.Contains(ref, ".."):
		return fmt.Errorf("renders %q, which contains %q", ref, "..")
	case strings.Contains(ref, "@{"):
		return fmt.Errorf("renders %q, which contains %q", ref, "@{")
	case strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/"):
		return fmt.Errorf("renders %q, which starts or ends with %q", ref, "/")
	}
	for _, part := range strings.Split(ref, "/") {
		switch {
		case part == "":
			return fmt.Errorf("renders %q, which has an empty path component", ref)
		case strings.HasPrefix(part, "."):
			return fmt.Errorf("renders %q, whose component %q starts with %q", ref, part, ".")
		case strings.HasSuffix(part, "."):
			return fmt.Errorf("renders %q, whose component %q ends with %q", ref, part, ".")
		case strings.HasSuffix(part, ".lock"):
			return fmt.Errorf("renders %q, whose component %q ends with %q", ref, part, ".lock")
		}
	}
	return nil
}

// SetBranchTemplate configures the branch-name template (LODE_BRANCH_TEMPLATE;
// "" means DefaultBranchTemplate) and the correlation pattern derived from it.
// It validates before installing anything, so a rejected template leaves the
// previous one in place. Called once at server start — a bad template is a
// startup failure, not a per-claim one (spec 030 §1.2).
func SetBranchTemplate(text string) error {
	if text == "" {
		text = DefaultBranchTemplate
	}
	tmpl, err := parseBranchTemplate(text)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	sample, err := render(tmpl, sampleFields)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	if err := validateRef(sample); err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	pattern, err := derivePattern(tmpl)
	if err != nil {
		return fmt.Errorf("branch template %q: %w", text, err)
	}
	branchMu.Lock()
	defer branchMu.Unlock()
	branchText, branchTmpl, branchPattern = text, tmpl, pattern
	return nil
}

// BranchTemplate returns the configured branch template.
func BranchTemplate() string {
	branchMu.RLock()
	defer branchMu.RUnlock()
	return branchText
}

// BranchFor returns the git branch for a task. SetBranchTemplate has already
// proved the template renders, and every field is supplied, so the render
// cannot fail here; the fallback exists so a branch name is never empty.
func BranchFor(t *Task) string {
	branchMu.RLock()
	tmpl := branchTmpl
	branchMu.RUnlock()
	f := branchFields{id: t.ID, slug: SlugifyTitle(t.Title), projectID: SlugifyTitle(t.ProjectID), kind: SlugifyTitle(t.Kind)}
	out, err := render(tmpl, f)
	if err != nil || out == "" {
		return t.ID + "-" + SlugifyTitle(t.Title)
	}
	return out
}

// TaskIDFromRef extracts a task id from a branch name, using the pattern
// derived from the configured template. It returns "" if ref does not match —
// including when the id part is lowercase, since task-id prefixes are always
// uppercase (e.g. WL-, SW-). A shape match is not proof the task exists;
// callers gate on taskExists before writing a binding.
func TaskIDFromRef(ref string) string {
	branchMu.RLock()
	defer branchMu.RUnlock()
	m := branchPattern.FindStringSubmatch(ref)
	if m == nil {
		return ""
	}
	return m[1]
}
```

- [ ] **Step 4: Delete the prefix machinery**

In `internal/store/changes.go`, delete the `branchPatternMu`/`branchPrefix`/
`branchPrefixPattern` var block, `DefaultBranchPrefix`, `buildBranchPattern`,
`SetBranchPrefix`, `BranchPrefix`, and the old `TaskIDFromRef` (lines 50–107).
Drop now-unused imports (`regexp`, `sync`) if nothing else in the file uses
them. In `internal/store/tasks.go`, delete the old `BranchFor` (lines 437–442)
— the new one lives in `branchname.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go build ./... && go test ./internal/store -run 'TestSetBranchTemplate|TestBranchRoundTrip|TestTaskIDFromRef|TestDerivedPattern|TestBranchTemplate'`
Expected: PASS. `go build ./...` will still fail in `internal/api` and
`internal/cmd` — those are Tasks 2 and 4. Confirm the only build errors are
`undefined: store.BranchPrefix` / `store.SetBranchPrefix` /
`store.DefaultBranchPrefix` in those packages.

- [ ] **Step 6: Fix in-package references and commit**

`grep -rn 'BranchPrefix' internal/store/` must come back empty. Update
`internal/store/changes_test.go` and any other in-package test referencing the
prefix to use `SetBranchTemplate` instead.

```bash
go test ./internal/store
git add internal/store/
git commit -m "store: render branch names from a template (spec 030 §1, §2)"
```

### Task 2 — Wire the template through the API server

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

Replace `api.Config.BranchPrefix` with `BranchTemplate`, fail `NewServer` on an
invalid template, and collapse the two hand-built branch strings in
`internal/api/lifecycle.go` (lines 105 and 135) onto `store.BranchFor`. Read
`LODE_BRANCH_TEMPLATE` in `internal/cmd/serve.go:84`.

**Interfaces consumed:** `store.SetBranchTemplate`, `store.BranchFor`,
`store.DefaultBranchTemplate` from Task 1.

- [ ] **Step 1: Write the failing tests**

In `internal/api/lifecycle_test.go`, rename `TestClaimBranchPrefix` (line 79) to
`TestClaimBranchTemplate` and change three lines inside it; the rest of the
body — actor, token, project, `createTaskViaAPI`, both `doReq` claims and the
claim-next assertions further down — stays exactly as it is.

```go
// TestClaimBranchTemplate checks Config.BranchTemplate (LODE_BRANCH_TEMPLATE)
// reaches the branch both claim endpoints hand out. The claim-next leg is
// what pins the wire field down: under a default-template server the CLI's
// fallback reconstruction is indistinguishable from the server's branch.
func TestClaimBranchTemplate(t *testing.T) {
	t.Cleanup(func() { store.SetBranchTemplate("") })
```

```go
	h, _, err := api.NewServer(st, api.Config{BranchTemplate: "team/{{ .id }}-{{ .slug }}"})
```

The two `"team/WL-1-fix-the-thing"` expectations already read correctly under
this template and need no edit — that is the point of choosing it.

Add beside it:

```go
func TestNewServerRejectsBadBranchTemplate(t *testing.T) {
	t.Cleanup(func() { store.SetBranchTemplate("") })
	st := newTestStore(t)
	for _, tmpl := range []string{"{{ .slug }}", "{{ .id ", "{{ .id }} {{ .slug }}"} {
		if _, _, err := api.NewServer(st, api.Config{BranchTemplate: tmpl}); err == nil {
			t.Errorf("NewServer accepted invalid branch template %q", tmpl)
		}
	}
}
```

Then update `internal/api/inbox_import_test.go:558`, dropping the prefix call:

```go
		"head":             map[string]any{"ref": taskID + "-old", "sha": "cafe"},
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/api -run 'TestClaimBranchTemplate|TestNewServerRejectsBadBranchTemplate'`
Expected: compile failure — `unknown field BranchTemplate in api.Config`.

- [ ] **Step 3: Change the config field**

In `internal/api/server.go`, replace the `BranchPrefix` field (lines 44–47):

```go
	// BranchTemplate (LODE_BRANCH_TEMPLATE) renders the task-branch names the
	// server hands out and correlates pushes by; empty means
	// store.DefaultBranchTemplate. An invalid template fails NewServer.
	BranchTemplate string
```

Delete the `branchPrefix` field from the `server` struct (lines 135–137).

- [ ] **Step 4: Validate at construction**

In `internal/api/server.go`, replace lines 229–230:

```go
	if err := store.SetBranchTemplate(cfg.BranchTemplate); err != nil {
		return nil, nil, err
	}
```

- [ ] **Step 5: Collapse the branch builders**

In `internal/api/lifecycle.go:105`:

```go
		"branch": store.BranchFor(t),
```

and in `toTaskPickJSON` (line 135):

```go
		Branch:   store.BranchFor(t),
```

Both call sites already hold a `*store.Task`. Leave the `Slug:` field beside
line 135 alone — it stays `SlugifyTitle(t.Title)`.

- [ ] **Step 6: Read the environment variable**

In `internal/cmd/serve.go:84`, replace the `BranchPrefix` line:

```go
				BranchTemplate:      os.Getenv("LODE_BRANCH_TEMPLATE"),
```

- [ ] **Step 7: Run the tests and commit**

```bash
go build ./... && go vet ./internal/api ./internal/cmd
go test ./internal/api
git add internal/api/ internal/cmd/serve.go
git commit -m "api: hand out template-rendered branch names (spec 030 §1)"
```

Expected: `internal/api` green. `internal/cmd` still fails to build until Task
4 — that is expected at this point.

### Task 3 — Worktree layout with a configurable base directory

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

Replace `worktree.DirName`/`worktree.ParseDir` with a `Layout` value carrying
the base directory, and add `WorktreeDir` to the CLI config. This task touches
`internal/worktree/worktree.go` and `internal/cli/client.go` only — the call
sites move in Tasks 4 and 5.

**Interfaces produced** (Tasks 4 and 5 depend on these exact names):

```go
// package worktree
const DefaultBase = ".worktrees"
type Layout struct{ /* unexported */ }
func NewLayout(base string) (Layout, error)   // "" ⇒ DefaultBase
func (l Layout) Base() string
func (l Layout) Dir(root, branch string) string
func (l Layout) ParseDir(path string) (taskID string, ok bool)

// package cli
type Config struct { /* ...existing... */; WorktreeDir string }
```

`Layout`'s zero value is deliberately unusable — always construct with
`NewLayout` so the base is validated exactly once.

- [ ] **Step 1: Write the failing worktree tests**

Replace `TestDirName`, `TestParseDir` and `TestParseDirGeneralPrefix` in
`internal/worktree/worktree_test.go` with:

```go
func TestNewLayoutRejects(t *testing.T) {
	for _, base := range []string{"/abs/path", "../escape", "a/../..", ".", "./"} {
		if _, err := worktree.NewLayout(base); err == nil {
			t.Errorf("NewLayout(%q) = nil error, want rejection", base)
		}
	}
}

func TestNewLayoutDefaults(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	if got := l.Base(); got != ".worktrees" {
		t.Errorf("Base() = %q, want .worktrees", got)
	}
}

func TestLayoutDir(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := l.Dir("/repo", "WL-7-fix-the-thing"), "/repo/.worktrees/WL-7-fix-the-thing"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	// A template containing "/" nests.
	if got, want := l.Dir("/repo", "team/WL-7-x"), "/repo/.worktrees/team/WL-7-x"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestLayoutParseDir(t *testing.T) {
	def, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	custom, err := worktree.NewLayout(".claude/worktrees")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		layout worktree.Layout
		path   string
		taskID string
		ok     bool
	}{
		{"default", def, "/repo/.worktrees/WL-7-fix-the-thing", "WL-7", true},
		{"bare id", def, "/repo/.worktrees/WL-7", "WL-7", true},
		{"nested", def, "/repo/.worktrees/team/SW-12-slug", "SW-12", true},
		{"id not at segment start", def, "/repo/.worktrees/worklode-WL-7-x", "WL-7", true},
		{"deep repo path", def, "/a/b/c/.worktrees/WL-1-x", "WL-1", true},
		{"trailing slash", def, "/repo/.worktrees/WL-7-x/", "WL-7", true},
		{"base repeated", def, "/repo/.worktrees/x/.worktrees/WL-7-x", "WL-7", true},
		{"no base segment", def, "/repo/wt/WL-7-fix", "", false},
		{"legacy wt is gone", def, "/repo/wt/WL-7", "", false},
		{"base but nothing below", def, "/repo/.worktrees", "", false},
		{"no id below base", def, "/repo/.worktrees/scratch", "", false},
		{"lowercase id", def, "/repo/.worktrees/wl-7-x", "", false},
		{"claude worktrees under default", def, "/repo/.claude/worktrees/WL-7-x", "", false},
		{"multi-segment base", custom, "/repo/.claude/worktrees/WL-7-x", "WL-7", true},
		{"multi-segment base not matched", custom, "/repo/.worktrees/WL-7-x", "", false},
		{"repo root", def, "/repo", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotID, gotOK := c.layout.ParseDir(c.path)
			if gotID != c.taskID || gotOK != c.ok {
				t.Errorf("ParseDir(%q) = (%q, %v), want (%q, %v)", c.path, gotID, gotOK, c.taskID, c.ok)
			}
		})
	}
}

func TestBranchNameFallback(t *testing.T) {
	if got, want := worktree.BranchName("WL-7", "fix-the-thing"), "WL-7-fix-the-thing"; got != want {
		t.Fatalf("BranchName = %q, want %q", got, want)
	}
}
```

Note the `claude worktrees under default` case: `internal/cmd/claude.go:40-50`
documents that Claude Code's own `.claude/worktrees/` must stay unrecognised
under the default layout, which is what keeps the `WorktreeCreate`/
`WorktreeRemove` handlers unreachable NOPs there.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/worktree`
Expected: compile failure — `undefined: worktree.NewLayout`.

- [ ] **Step 3: Implement `Layout`**

In `internal/worktree/worktree.go`, replace the package comment, `dirRe`,
`DirName`, `BranchName`, and `ParseDir` (lines 1–40) with:

```go
// Package worktree maps Worklode task identity onto git worktrees: the
// <base>/<branch> directory layout, the fallback branch name, and the lease
// identity string the backbone stores.
package worktree

// ...existing imports, plus nothing new...

// DefaultBase is the worktree base directory used when worktree_dir /
// LODE_WORKTREE_DIR is unset (spec 030 §3.1).
const DefaultBase = ".worktrees"

// idRe matches a task id anywhere in the path below the base directory. The
// base directory is the guard; this only extracts (spec 030 §3.2).
var idRe = regexp.MustCompile(`[A-Z][A-Z0-9]*-[0-9]+`)

// Layout is the resolved worktree directory layout for a checkout. Construct
// it with NewLayout — the zero value has no base and rejects every path.
type Layout struct {
	base  string   // slash-separated, as configured
	parts []string // base split into segments
}

// NewLayout validates a configured base directory. It is interpreted relative
// to the git root, so an absolute path or one escaping the root is refused.
func NewLayout(base string) (Layout, error) {
	if base == "" {
		base = DefaultBase
	}
	// IsAbs before trimming: trimming "/" off "/abs/path" would make an
	// absolute path look relative.
	if filepath.IsAbs(base) || strings.HasPrefix(base, "/") {
		return Layout{}, fmt.Errorf("worktree dir %q must be a path relative to the repository root, not an absolute path", base)
	}
	base = strings.Trim(filepath.ToSlash(base), "/")
	if base == "" {
		return Layout{}, fmt.Errorf("worktree dir must not be empty")
	}
	parts := strings.Split(base, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return Layout{}, fmt.Errorf("worktree dir %q must not contain %q or %q segments", base, ".", "..")
		}
	}
	return Layout{base: base, parts: parts}, nil
}

// Base returns the configured base directory, relative to the git root.
func (l Layout) Base() string { return l.base }

// Dir returns the worktree directory for a branch: <root>/<base>/<branch>.
// A branch containing "/" nests, which git worktree add handles.
func (l Layout) Dir(root, branch string) string {
	return filepath.Join(root, filepath.FromSlash(l.base), filepath.FromSlash(branch))
}

// ParseDir returns the task id when path lies under the base directory and an
// id appears below it. This is the uniform hook guard: ok=false ⇒ NOP.
func (l Layout) ParseDir(path string) (taskID string, ok bool) {
	if len(l.parts) == 0 {
		return "", false
	}
	segs := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	idx := lastIndexOf(segs, l.parts)
	if idx < 0 {
		return "", false
	}
	below := segs[idx+len(l.parts):]
	if len(below) == 0 {
		return "", false
	}
	id := idRe.FindString(strings.Join(below, "/"))
	if id == "" {
		return "", false
	}
	return id, true
}

// lastIndexOf returns the starting index of the last occurrence of sub in
// segs, or -1. The *last* occurrence wins so a repository that itself sits
// inside someone's worktree base still resolves against its own.
func lastIndexOf(segs, sub []string) int {
	for i := len(segs) - len(sub); i >= 0; i-- {
		if slices.Equal(segs[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

// BranchName is the client-side fallback branch for a task, used only when a
// server response carries no branch. The server is the authority: it renders
// LODE_BRANCH_TEMPLATE and every response carries the result (spec 030 §1).
func BranchName(taskID, slug string) string { return taskID + "-" + slug }
```

Add `"slices"` to the imports; `regexp`, `strings`, `filepath` and `fmt` are
already there.

- [ ] **Step 4: Run the worktree tests**

Run: `go test ./internal/worktree`
Expected: PASS.

- [ ] **Step 5: Write the failing CLI config test**

Add to `internal/cli/client_test.go` (it already has `writeRepoConfig` and the
`$HOME`-isolating helpers — reuse them):

```go
func TestWorktreeDirFromRepoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	cfg, err := loadConfigFrom(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorktreeDir != "wtrees" {
		t.Errorf("WorktreeDir = %q, want wtrees", cfg.WorktreeDir)
	}
}

func TestWorktreeDirEnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LODE_WORKTREE_DIR", "from-env")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	cfg, err := loadConfigFrom(repo)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorktreeDir != "from-env" {
		t.Errorf("WorktreeDir = %q, want from-env", cfg.WorktreeDir)
	}
}
```

- [ ] **Step 6: Run to verify they fail**

Run: `go test ./internal/cli -run TestWorktreeDir`
Expected: compile failure — `cfg.WorktreeDir undefined`.

- [ ] **Step 7: Add the config field**

In `internal/cli/client.go`, add to `Config` (after `CurrentProjectPath`):

```go
	// WorktreeDir is the worktree base directory, relative to the git root
	// (spec 030 §3.1). Empty means worktree.DefaultBase.
	WorktreeDir string
```

In `parseConfig`, add a case beside `current_project`:

```go
		case "worktree_dir":
			cfg.WorktreeDir = val
```

In `Config.merge`, after the `CurrentProject` block:

```go
	if repo.WorktreeDir != "" {
		cfg.WorktreeDir = repo.WorktreeDir
	}
```

In `loadConfigFrom`, beside the other environment overrides — before the
`LODE_TOKEN` early return, so it is not skipped:

```go
	if v := os.Getenv("LODE_WORKTREE_DIR"); v != "" {
		cfg.WorktreeDir = v
	}
```

- [ ] **Step 8: Run and commit**

```bash
go test ./internal/worktree ./internal/cli
git add internal/worktree/ internal/cli/
git commit -m "worktree: configurable base directory via Layout (spec 030 §3)"
```

### Task 4 — Thread the layout through the CLI lifecycle commands

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2, 3]
```

Update `internal/cmd/lifecycle.go` and `internal/cmd/task.go:525` to build a
`worktree.Layout` from the loaded config and use it for both the directory and
the guard. This is the task that makes `internal/cmd` build again.

**Interfaces consumed:** `worktree.NewLayout`, `Layout.Dir`, `Layout.ParseDir`,
`worktree.BranchName`, `cli.Config.WorktreeDir` from Task 3.

- [ ] **Step 1: Update the test expectations first, and run them failing**

In `internal/cmd/lifecycle_test.go`, change the four hardcoded branches (lines
204, 253, 420, 690–691) from `"lode/" + task.ID + "-…"` to `task.ID + "-…"`. In
`internal/cli/client_test.go`, the same at **both** `:222`
(`TestClientTaskLifecycle`) and `:438-439` (`TestClientBriefAndRebindWorktree`)
— both currently fail asserting a stale `lode/` branch. In
`internal/hookrun/hookrun_test.go:1400`, change the `git worktree add` fixture
from `"lode/"+taskID+"-"+slug` to `taskID+"-"+slug`.

Add to `internal/cmd/lifecycle_test.go`:

```go
func TestResolveWorktreeTaskRejectsNonWorktree(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	// A plain repo root is not a Worklode worktree.
	if _, _, err := resolveWorktreeTask(l, t.TempDir()); err == nil {
		t.Fatal("resolveWorktreeTask accepted a non-worktree directory")
	}
}
```

Run: `go test ./internal/cmd -run TestResolveWorktreeTask`
Expected: compile failure — `too many arguments in call to resolveWorktreeTask`.

- [ ] **Step 2: Add a layout helper**

In `internal/cmd/lifecycle.go`, above `resolveWorktreeTask`:

```go
// layoutFrom builds the worktree layout from a loaded config. A misconfigured
// worktree_dir is a user error worth reporting, not a silent fallback.
func layoutFrom(cfg cli.Config) (worktree.Layout, error) {
	l, err := worktree.NewLayout(cfg.WorktreeDir)
	if err != nil {
		return worktree.Layout{}, fmt.Errorf("worktree_dir: %w", err)
	}
	return l, nil
}
```

- [ ] **Step 3: Take the layout in `resolveWorktreeTask`**

Replace lines 35–47:

```go
// resolveWorktreeTask resolves dir to its enclosing git worktree root and the
// task id encoded in its <base>/<branch> path. It errors when dir is not
// inside a git repository, or when the repo root is not a Worklode worktree.
func resolveWorktreeTask(l worktree.Layout, dir string) (taskID, root string, err error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", "", fmt.Errorf("%s is not inside a git repository", dir)
	}
	taskID, ok = l.ParseDir(root)
	if !ok {
		return "", "", fmt.Errorf("%s is not a Worklode worktree (%s/<branch>); run this from inside one", root, l.Base())
	}
	return taskID, root, nil
}
```

- [ ] **Step 4: Update every `resolveWorktreeTask` caller**

`grep -n 'resolveWorktreeTask' internal/cmd/` lists them (`resume`, `done`,
`block`, `status`). Each already calls `newAPIClientWithConfig()` and so has a
`cli.Config` in hand; build the layout from it and pass it through. Where a
caller does not yet load the config, add the `newAPIClientWithConfig()` result
it already has — do not introduce a second config load.

- [ ] **Step 5: Update `runNext`**

In `internal/cmd/lifecycle.go`, after the `newAPIClientWithConfig()` call at
the top of `runNext`:

```go
	layout, err := layoutFrom(cfg)
	if err != nil {
		return err
	}
```

Replace the guard at line 147:

```go
	if inside, ok := layout.ParseDir(root); ok {
		return fmt.Errorf("already inside a worktree for %s; run `lode next` from the main repository, not from %s/", inside, layout.Base())
	}
```

Replace the fallback at line 185 and the directory at line 188:

```go
	if branch == "" {
		branch = worktree.BranchName(taskID, slug)
	}

	dir := layout.Dir(root, branch)
```

- [ ] **Step 6: Update the remaining prefix references**

In `internal/cmd/task.go:525`:

```go
				branch = resp.Task.ID + "-" + resp.Task.Slug
```

Then update the doc strings that name the old layout: `lifecycle.go:100`
(`creates its wt/<id>-<slug> worktree` → `creates its worktree`) and
`hook.go:26` (`does nothing outside a wt/<id>-<slug> session` → `does nothing
outside a Worklode worktree`). In `internal/cmd/claude.go:46-48`, replace
`under .claude/worktrees/, which worktree.ParseDir rejects` with `under
.claude/worktrees/, which the default layout rejects` and `Worklode's own
wt/<task-id> worktrees` with `Worklode's own worktrees`.

- [ ] **Step 7: Run the tests and commit**

```bash
go build ./... && go vet ./...
go test ./internal/cmd ./internal/cli
git add internal/cmd/
git commit -m "cmd: place worktrees under the configured base dir (spec 030 §3)"
```

Expected: `go build ./...` fully green for the first time since Task 1.

### Task 5 — Thread the layout through the hooks

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [3]
```

`internal/hookrun/hookrun.go` calls `worktree.ParseDir` at nine sites (lines
336, 475, 510, 529, 554, 587, 622, 665, and the guard reached from the package
doc at line 8). Give `Options` a lazily-resolved layout, and rewrite
`offerScan` (lines 452–503) to walk the configured base directory instead of
reading a flat `wt/`.

**Interfaces consumed:** `worktree.NewLayout`, `Layout.Base`, `Layout.ParseDir`
from Task 3.

- [ ] **Step 1: Move the two worktree fixtures onto the new layout**

`setupLeasedWorktree` (line 179) and `addWorktree` (line 1394) are what every
hookrun test builds its worktree with. Both hardcode the old shapes.

In `setupLeasedWorktree`, replace lines 197–198:

```go
	slug := strings.TrimPrefix(resp.Branch, task.ID+"-")
	wtDir = filepath.Join(root, ".worktrees", resp.Branch)
```

In `addWorktree`, replace the comment and body:

```go
// addWorktree creates a .worktrees/<taskID>-<slug> git worktree under root.
// Unlike setupLeasedWorktree it needs no backbone: session-end's guard only
// reads the directory path.
func addWorktree(t *testing.T, root, taskID, slug string) string {
	t.Helper()
	dir := filepath.Join(root, ".worktrees", taskID+"-"+slug)
	out, err := exec.Command("git", "-C", root, "worktree", "add", dir, "-b", taskID+"-"+slug).CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return dir
}
```

`setupLeasedWorktree` no longer references `store`; drop the import if nothing
else in the file uses it (`go vet ./internal/hookrun` will say).

- [ ] **Step 2: Write the failing offerScan tests**

Add to `internal/hookrun/hookrun_test.go`, modelled on
`TestSessionStartEmitsAdditionalContext` (line 355) — `newRealServer`,
`initGitRepo` and `setupLeasedWorktree` are the same helpers it uses:

```go
// offerScan runs when session-start fires OUTSIDE a worktree. A worktree whose
// lease has expired and whose marker is absent is offered for adoption; the
// walk must reach one nested under a "/"-containing branch (spec 030 §3.1).
func TestOfferScanFindsNestedWorktree(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Nested worktree")

	// Re-home it one level deeper, as a "team/<id>-<slug>" branch would.
	nested := filepath.Join(root, ".worktrees", "team", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, nested).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	ctx := offerScanContext(t, root)
	if !strings.Contains(ctx, ".worktrees/team/"+filepath.Base(nested)) {
		t.Fatalf("additionalContext does not offer the nested worktree: %q", ctx)
	}
	if !strings.Contains(ctx, taskID) {
		t.Fatalf("additionalContext missing task id %q: %q", taskID, ctx)
	}
}

// Spec 030 §5: the legacy wt/ directory is not recognised any more.
func TestOfferScanIgnoresLegacyWtDir(t *testing.T) {
	st, c, _ := newRealServer(t)
	root := initGitRepo(t)
	taskID, wtDir, _ := setupLeasedWorktree(t, c, root, "Legacy worktree")

	legacy := filepath.Join(root, "wt", filepath.Base(wtDir))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "move", wtDir, legacy).CombinedOutput(); err != nil {
		t.Fatalf("git worktree move: %v\n%s", err, out)
	}
	expireLease(t, st, taskID)

	if ctx := offerScanContext(t, root); ctx != "" {
		t.Fatalf("legacy wt/ worktree was offered for adoption: %q", ctx)
	}
}

// offerScanContext runs session-start from dir and returns the
// additionalContext it emitted ("" when it emitted nothing).
func offerScanContext(t *testing.T, dir string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), Options{
		Event:  "session-start",
		Stdin:  bytes.NewReader(payloadJSON(t, Payload{Cwd: dir, SessionID: "s-scan", HookEventName: "SessionStart"})),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if code != 0 {
		t.Fatalf("session-start exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stdout.Len() == 0 {
		return ""
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("stdout is not valid additionalContext JSON: %v\nstdout: %s", err, stdout.String())
	}
	return out.HookSpecificOutput.AdditionalContext
}
```

`expireLease` is a helper these tests need: the offer only fires on an expired
lease. Check whether `hookrun_test.go` already has one under another name
(`grep -n 'ExpiresAt\|expire' internal/hookrun/hookrun_test.go`) and reuse it;
if not, add one that releases the lease through `st` so `brief.Lease` comes
back nil, which satisfies the same `leaseGone` branch.

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/hookrun -run TestOfferScan`
Expected: `TestOfferScanFindsNestedWorktree` fails with an empty
additionalContext — the current code reads a flat `wt/` directory that no
longer exists. `TestOfferScanIgnoresLegacyWtDir` fails too, because the current
code still recognises `wt/`.

- [ ] **Step 4: Add the layout to `Options`**

In `internal/hookrun/hookrun.go`, beside the existing `Options.client()` /
`Options.now()` accessors (lines 97–120):

```go
	// Layout, when non-nil, overrides the worktree layout resolved from
	// config. Tests set it; production leaves it nil.
	Layout func() (worktree.Layout, error)
```

```go
// layout resolves the worktree layout from config. A hook must never fail an
// event, so a missing or malformed worktree_dir degrades to the default
// layout rather than erroring out.
func (o Options) layout() worktree.Layout {
	resolve := o.Layout
	if resolve == nil {
		resolve = func() (worktree.Layout, error) {
			cfg, err := cli.LoadConfig()
			if err != nil {
				return worktree.Layout{}, err
			}
			return worktree.NewLayout(cfg.WorktreeDir)
		}
	}
	l, err := resolve()
	if err != nil {
		warn(o, "resolve worktree layout: %v (using %s)", err, worktree.DefaultBase)
		l, _ = worktree.NewLayout("")
	}
	return l
}
```

Resolve it once per handler invocation, not once per `ParseDir` call: each
handler already takes `opts`, so add `l := opts.layout()` at the top of the
handler and pass `l` down where a helper needs it.

- [ ] **Step 5: Rewrite `offerScan`**

Replace the body of `offerScan` (lines 452–503). The `filepath.WalkDir` depth
cap of 3 below the base matches spec 030 §3.2; the five-worktree fetch cap is
existing behaviour and stays:

```go
// offerScan runs at session start OUTSIDE a worktree: it walks the configured
// worktree base directory under the repo root and, for up to five entries that
// parse as Worklode worktrees, flags any whose lease is expired/absent and
// whose session marker is stale/absent as adoptable. No claim, no model call.
func offerScan(ctx context.Context, opts Options, repoRoot string) {
	l := opts.layout()
	base := filepath.Join(repoRoot, filepath.FromSlash(l.Base()))
	if _, err := os.Stat(base); err != nil {
		return // no base dir ⇒ nothing to offer
	}

	c, err := opts.client()
	if err != nil {
		warn(opts, "load config: %v", err)
		return
	}

	now := opts.now()
	var lines []string
	fetched := 0
	walkErr := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == base {
			return nil //nolint:nilerr // a broken entry is skipped, never fatal
		}
		if fetched >= 5 {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(base, p)
		if relErr != nil {
			return nil
		}
		// A branch may nest (spec 030 §3.1); cap the walk rather than recurse
		// into a worktree's own contents.
		if depth := len(strings.Split(filepath.ToSlash(rel), "/")); depth > 3 {
			return filepath.SkipDir
		}
		taskID, ok := l.ParseDir(p)
		if !ok {
			return nil
		}
		fetched++

		bctx, cancel := context.WithTimeout(ctx, backboneTimeout)
		brief, _, briefErr := c.Brief(bctx, taskID)
		cancel()
		if briefErr != nil {
			return filepath.SkipDir // best-effort per worktree
		}

		leaseGone := brief.Lease == nil || brief.Lease.ExpiresAt.Before(now)
		if leaseGone && !sessionMarkerFresh(p) {
			shown := filepath.ToSlash(filepath.Join(l.Base(), rel))
			lines = append(lines, fmt.Sprintf(
				"Worklode worktree %s (%s: %s) is abandoned — `/lode:resume %s` to adopt it.",
				shown, taskID, brief.Task.Title, shown))
		}
		return filepath.SkipDir // do not descend into a worktree
	})
	if walkErr != nil {
		warn(opts, "scan %s: %v", base, walkErr)
	}

	if len(lines) > 0 {
		emitAdditionalContext(opts.Stdout, strings.Join(lines, "\n"))
	}
}
```

- [ ] **Step 6: Update the other eight guard sites**

At lines 336, 510, 529, 554, 587, 622, 665 replace `worktree.ParseDir(x)` with
`l.ParseDir(x)`, where `l` comes from `opts.layout()` at the top of the
enclosing handler. Update the two stale comments: the package doc at line 8
(`to a wt/<id>-<slug> worktree (worktree.Root → worktree.ParseDir)` → `to a
Worklode worktree (worktree.Root → Layout.ParseDir)`) and line 556 (`not a wt/
dir` → `not under the worktree base dir`).

- [ ] **Step 7: Run the tests and commit**

```bash
go build ./... && go vet ./internal/hookrun
go test ./internal/hookrun
git add internal/hookrun/
git commit -m "hookrun: guard on the configured worktree base dir (spec 030 §3.2)"
```

### Task 6 — Docs, gitignore, and the worktree migration

```yaml
kind: chore
priority: medium
blockedBy: [4, 5]
```

Everything outside Go: the ignore rule, the repo-facing docs, the `e2e` and
`transcript` fixtures that still reference the old shapes, and the two manual
commands that move this checkout's one worktree.

- [ ] **Step 1: Update `.gitignore`**

Replace the `wt/` line with `.worktrees/`.

- [ ] **Step 2: Sweep the remaining references**

```bash
grep -rn 'wt/\|lode/[A-Z]\|wl/[A-Z]\|LODE_BRANCH_PREFIX\|BranchPrefix' \
  --include='*.go' --include='*.md' --include='*.yaml' --include='*.yml' . \
  | grep -v '^./docs/specs/' | grep -v '^./docs/plans/'
```

Fix every hit outside `docs/specs/` and `docs/plans/` (historical documents
stay as written — the spec-030 amendment notes are what update them). Expect
hits in `README.md`, `internal/transcript/transcript_test.go`,
`internal/store/*_test.go` fixtures, `internal/api/agentsessions_test.go`, and
`e2e/`. A fixture path only needs to be *a* valid worktree path, so
`wt/WL-1-x` → `.worktrees/WL-1-x`.

- [ ] **Step 3: Update `CLAUDE.md`**

In the Conventions section, replace the task-branch bullet:

```markdown
- Task branches are rendered from `LODE_BRANCH_TEMPLATE` (default
  `{{ .id }}-{{ .slug }}`, e.g. `WL-7-fix-the-thing`); the server is the
  authority on branch names. Worktrees live under `worktree_dir` (default
  `.worktrees`), configurable per repo in `.worklode/config.toml`.
```

In the Architecture section, replace `worktree-bound leases (a claim binds a
task to a `wt/<task-id>-<slug>` worktree; ...)` with `worktree-bound leases (a
claim binds a task to a worktree under `.worktrees/`; ...)`.

- [ ] **Step 4: Verify the whole suite**

```bash
go build ./... && go vet ./... && go test ./...
go test -race -count=1 -tags e2e ./e2e/
./scripts/secfmt.py -l
```

All three must pass. The store and e2e suites need Postgres with pgvector on
`postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable` (or
`TEST_POSTGRES_DSN`) — they skip silently otherwise, so confirm Postgres is
reachable before reading a green run as proof.

- [ ] **Step 5: Commit the docs**

```bash
git add .gitignore CLAUDE.md README.md internal/ e2e/
git commit -m "docs: adopt template branch names and .worktrees (spec 030)"
```

- [ ] **Step 6: Migrate this checkout's worktree**

Worklode worktrees exist on one machine only (spec 030 §5), so this is a manual
two-command move rather than a compatibility layer. From the main checkout:

```bash
git worktree list   # confirm the set before moving anything
mkdir -p .worktrees
git worktree move wt/WL-3-execute-plan-data-platform-kg-requiremen \
                  .worktrees/WL-3-execute-plan-data-platform-kg-requiremen
git branch -m lode/WL-3-execute-plan-data-platform-kg-requiremen \
              WL-3-execute-plan-data-platform-kg-requiremen
rmdir wt 2>/dev/null || true
git worktree list   # confirm the new path and branch
```

WL-3's lease is already expired, so the stale `<host>:<path>` worktree identity
in the backbone rebinds on the next `/lode:resume` — the same path an expired
lease already takes (spec 008 §3, step 3). Do not commit anything here; this
step changes only local git state.
