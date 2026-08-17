---
status: superseded
covers: docs/specs/025-documents-in-the-backbone.md
---
# Design-doc sync, part 1 — client foundations

> **Superseded — the on-ramp this plan built was retired.** `lode doc
> sync` and the 025 §5.1 store shipped, never ran against a non-empty
> corpus, and were removed; 025 §16 records why. Backbone authoring
> (025 §7/§9.2) replaces it — see
> `2026-08-03-documents-in-the-backbone-2-document-store.md`.

> **For agentic workers:** REQUIRED SUB-SKILL: Use
> superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use
> checkbox (`- [ ]`) syntax for tracking.

**Goal:** The client-side groundwork for spec 034: the `spec_corpus`/`plan_corpus`
config keys, the generalized corpus resolution in `internal/designdoc`, corpus
loading with file-derived document identity (025 §16.3), `lode task add
--body-file` (025 §18), and the `wl:Plan` ontology mirror (025 §17).

**Architecture:** Everything here is client/library-side Go plus the `ns/`
Turtle files — no server, no schema, no HTTP. Part 2
(`2026-08-09-design-doc-sync-2-document-store.md`) builds the backbone store
this part's corpus loader will feed; part 3 wires them together. Config keys
follow the `worktree_dir` precedent exactly: parsed by the flat reader,
repo-scoped, never merged into the user-level `Config`, read through a
dedicated function. Corpus loading and identity derivation live in
`internal/designdoc` beside the existing parser.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3` (already a dependency), cobra,
Turtle/SHACL under `ns/` validated with `riot`.

## Global constraints

- Spec 034 is the source of truth; §12 is the acceptance list. This part lands
  §12 items 1, 6, 7 and the client half of 5.
- Identity grammar (025 §16.3): `<KEY>-SPEC-<n>` / `<KEY>-ADR-<n>` from the
  filename's leading number; `<KEY>-PLAN-<spec-ordinal>-<plan-ordinal>` where
  spec-ordinal is the implemented spec's number (`NO-SPEC` or absent
  `implements` → `0`) and plan-ordinal counts plans implementing that spec in
  ascending filename order (date prefix, then slug). The 029 cutover
  reconciliation is out of scope.
- Within `spec_corpus`, a file is an ADR iff its frontmatter carries
  `kind: adr`; otherwise a spec. Every file in `plan_corpus` is a plan (025 §16.1).
- A document whose frontmatter fails to parse is a sync error, never a
  silently skipped row (025 §16.2).
- The config parser stays a flat `key = "value"` reader; unknown keys must
  keep erroring (034 §12.1).
- Run `go build ./...` and the named package tests before every commit. Never
  put `Co-authored-by` or any agent advertisement in commit messages.

## Tasks

### Task 1 — spec_corpus and plan_corpus config keys

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
```

Add the two repo-scoped corpus keys to the client config
(`internal/cli/client.go`), mirroring `worktree_dir` (spec 008 §6): parsed into
dedicated `Config` fields, zeroed on the user-level load path, excluded from
`merge()`, and read through a new `CorporaFrom` function that only consults the
repo-local config.

**Files:**
- Modify: `internal/cli/client.go` (Config struct ~L46-70, `WorktreeDirFrom`
  ~L131-144, `loadConfigFrom` ~L160-219, `parseConfig` ~L221-252, `merge`
  ~L274-292)
- Test: `internal/cli/client_test.go` (helpers `writeRepoConfig` L997,
  `repoTestHome` L1011 already exist)

**Interfaces produced (used by tasks 2-3 and part 3):**

```go
// Corpora is the repo-scoped corpus declaration (spec 025 §16.1). Zero value:
// nothing configured to sync.
type Corpora struct {
	Root    string // absolute repo root — the directory holding .worklode/
	SpecDir string // absolute spec corpus dir; "" when spec_corpus is unset
	PlanDir string // absolute plan corpus dir; "" when plan_corpus is unset
}

func CorporaFrom(startDir string) (Corpora, error)
```

- [ ] **Step 1: Write the failing tests** — append to
  `internal/cli/client_test.go`:

```go
// spec_corpus / plan_corpus are repo-scoped like worktree_dir (spec 025 §16.1):
// CorporaFrom, not LoadConfig, is the sole reader.

func TestCorporaFromRepoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode",
		"spec_corpus = \"docs/specs\"\nplan_corpus = \"docs/plans\"\n")

	c, err := cli.CorporaFrom(filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatalf("CorporaFrom: %v", err)
	}
	if c.Root != repo {
		t.Errorf("Root = %q, want %q", c.Root, repo)
	}
	if want := filepath.Join(repo, "docs", "specs"); c.SpecDir != want {
		t.Errorf("SpecDir = %q, want %q", c.SpecDir, want)
	}
	if want := filepath.Join(repo, "docs", "plans"); c.PlanDir != want {
		t.Errorf("PlanDir = %q, want %q", c.PlanDir, want)
	}
}

func TestCorporaFromKeyPresenceEnablesEachCorpus(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "spec_corpus = \"design\"\n")

	c, err := cli.CorporaFrom(repo)
	if err != nil {
		t.Fatalf("CorporaFrom: %v", err)
	}
	if want := filepath.Join(repo, "design"); c.SpecDir != want {
		t.Errorf("SpecDir = %q, want %q", c.SpecDir, want)
	}
	if c.PlanDir != "" {
		t.Errorf("PlanDir = %q, want \"\" (plan_corpus unset)", c.PlanDir)
	}
}

func TestCorporaFromNoRepoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	c, err := cli.CorporaFrom(filepath.Join(home, "git", "bare"))
	if err != nil {
		t.Fatalf("CorporaFrom: %v", err)
	}
	if c != (cli.Corpora{}) {
		t.Errorf("CorporaFrom = %+v, want zero Corpora", c)
	}
}

func TestCorporaFromRejectsAbsolutePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "spec_corpus = \"/etc/specs\"\n")
	if _, err := cli.CorporaFrom(repo); err == nil {
		t.Fatal("CorporaFrom accepted an absolute spec_corpus; want error")
	}
}

// The keys never reach the merged Config, mirroring
// TestLoadConfigFromNeverPopulatesWorktreeDir.
func TestLoadConfigFromNeverPopulatesCorpora(t *testing.T) {
	home, workDir := repoTestHome(t,
		"server = \"https://wl.example.com\"\nspec_corpus = \"user-specs\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode",
		"spec_corpus = \"repo-specs\"\nplan_corpus = \"repo-plans\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.SpecCorpus != "" || cfg.PlanCorpus != "" {
		t.Fatalf("Config carries corpus keys (%q, %q); want empty — CorporaFrom is the sole reader",
			cfg.SpecCorpus, cfg.PlanCorpus)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli -run 'TestCorporaFrom|TestLoadConfigFromNeverPopulatesCorpora' -v`
Expected: compile error — `cli.Corpora`, `cli.CorporaFrom`, `cfg.SpecCorpus`
undefined.

- [ ] **Step 3: Implement** in `internal/cli/client.go`:

Add to the `Config` struct, right after `WorktreeDir`:

```go
	// SpecCorpus / PlanCorpus carry the spec_corpus / plan_corpus keys when
	// Config is produced directly by parseConfig — which is how CorporaFrom
	// reads them. Like WorktreeDir they are repo-scoped only (spec 025 §16.1)
	// and are NOT populated by LoadConfig/loadConfigFrom; CorporaFrom is the
	// sole reader.
	SpecCorpus string
	PlanCorpus string
```

Add two `case` arms to `parseConfig`'s switch (before `default`):

```go
		case "spec_corpus":
			cfg.SpecCorpus = val
		case "plan_corpus":
			cfg.PlanCorpus = val
```

In `loadConfigFrom`, next to the existing `cfg.WorktreeDir = ""` line for the
user-level file, also zero the new fields:

```go
			// spec_corpus/plan_corpus are repo-scoped only (spec 025 §16.1);
			// CorporaFrom is the sole reader.
			cfg.SpecCorpus, cfg.PlanCorpus = "", ""
```

Extend `merge`'s trailing comment to name them (no code — like `worktree_dir`
they are deliberately not merged), and add after `WorktreeDirFrom`:

```go
// Corpora is the repo-scoped corpus declaration (spec 025 §16.1): which
// directories `lode doc sync` reads, and as which document kind. A key's
// presence enables its corpus; the zero value means nothing is configured.
type Corpora struct {
	Root    string // absolute repo root — the directory holding .worklode/
	SpecDir string // absolute spec corpus dir; "" when spec_corpus is unset
	PlanDir string // absolute plan corpus dir; "" when plan_corpus is unset
}

// CorporaFrom reads startDir's repo-local config for spec_corpus/plan_corpus
// (spec 025 §16.1). Like WorktreeDirFrom it never consults the user-level config
// or the keychain, but unlike it a malformed repo config is an error here —
// sync must not silently degrade to "nothing configured" (025 §16.2).
func CorporaFrom(startDir string) (Corpora, error) {
	repoPath, ok := findRepoConfig(startDir)
	if !ok {
		return Corpora{}, nil
	}
	data, err := os.ReadFile(repoPath)
	if err != nil {
		return Corpora{}, fmt.Errorf("read %s: %w", repoPath, err)
	}
	cfg, err := parseConfig(string(data))
	if err != nil {
		return Corpora{}, fmt.Errorf("parse %s: %w", repoPath, err)
	}
	// repoPath is <root>/.worklode/config.toml (or .lode/): root is two up.
	root := filepath.Dir(filepath.Dir(repoPath))
	c := Corpora{Root: root}
	for _, k := range []struct {
		key, val string
		dst      *string
	}{
		{"spec_corpus", cfg.SpecCorpus, &c.SpecDir},
		{"plan_corpus", cfg.PlanCorpus, &c.PlanDir},
	} {
		if k.val == "" {
			continue
		}
		if filepath.IsAbs(k.val) {
			return Corpora{}, fmt.Errorf("%s: %s = %q must be a repo-relative directory", repoPath, k.key, k.val)
		}
		*k.dst = filepath.Join(root, filepath.FromSlash(k.val))
	}
	return c, nil
}
```

Also update the `Config` doc comment's recognized-key list to include
`spec_corpus` and `plan_corpus`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli -v`
Expected: all PASS, including the pre-existing unknown-key test
(`bogus = "value"` still errors — acceptance 034 §12.1).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/client.go internal/cli/client_test.go
git commit -m "cli config: repo-scoped spec_corpus/plan_corpus keys (spec 025 §16.1)"
```

### Task 2 — Generalize designdoc.FindCorpus and honor spec_corpus in lode show

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

`designdoc.FindCorpus` (`internal/designdoc/resolve.go:23`) hardcodes
`docs/specs`. Split out the repo-root walk as `FindRepoRoot`, keep `FindCorpus`
as the conventional-default wrapper, and make `lode show`'s doc path
(`internal/cmd/docrender.go` `runDocShow`) prefer a configured `spec_corpus`
over the default.

**Files:**
- Modify: `internal/designdoc/resolve.go:23-38`
- Modify: `internal/cmd/docrender.go:47-58` (`runDocShow`)
- Test: `internal/designdoc/resolve_test.go`

**Interfaces produced:**

```go
// designdoc package:
func FindRepoRoot(dir string) string // "" when no .worklode ancestor
func FindCorpus(dir string) string   // unchanged behavior: <root>/docs/specs
```

- [ ] **Step 1: Write the failing test** — append to
  `internal/designdoc/resolve_test.go` (an *internal* test file, `package
  designdoc` — no qualifier):

```go
func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".worklode"), 0o755); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := FindRepoRoot(nested); got != root {
		t.Errorf("FindRepoRoot(%q) = %q, want %q", nested, got, root)
	}
	if got := FindRepoRoot(t.TempDir()); got != "" {
		t.Errorf("FindRepoRoot outside a repo = %q, want \"\"", got)
	}
	// FindCorpus stays the conventional-default wrapper.
	if got, want := FindCorpus(nested), filepath.Join(root, "docs", "specs"); got != want {
		t.Errorf("FindCorpus = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/designdoc -run TestFindRepoRoot -v`
Expected: compile error — `designdoc.FindRepoRoot` undefined.

- [ ] **Step 3: Implement** — replace `FindCorpus` in
  `internal/designdoc/resolve.go`:

```go
// FindRepoRoot walks up from dir to the nearest directory containing a
// ".worklode" directory — the repo root the corpus config is relative to
// (spec 025 §16.1). Returns "" when no repo root is found.
func FindRepoRoot(dir string) string {
	d, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	for {
		if st, err := os.Stat(filepath.Join(d, ".worklode")); err == nil && st.IsDir() {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

// FindCorpus returns the conventional spec corpus, docs/specs under the repo
// root — the default a repo without a spec_corpus key gets (025 §16.1). Returns
// "" when no repo root is found.
func FindCorpus(dir string) string {
	root := FindRepoRoot(dir)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "docs", "specs")
}
```

In `internal/cmd/docrender.go` `runDocShow`, replace the corpus lookup
(currently `corpus := designdoc.FindCorpus(cwd)` + empty check) with:

```go
	corpus := designdoc.FindCorpus(cwd)
	if corpora, err := cli.CorporaFrom(cwd); err == nil && corpora.SpecDir != "" {
		corpus = corpora.SpecDir
	}
	if corpus == "" {
		return errors.New("not inside a worklode repo (no .worklode directory found)")
	}
```

(A malformed repo config degrades to the conventional default here — `lode
show` is a read command and must keep working; `lode doc sync` in part 3 is
where a malformed config errors.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/designdoc ./internal/cmd`
Expected: PASS (existing resolve/show tests unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/designdoc/resolve.go internal/designdoc/resolve_test.go internal/cmd/docrender.go
git commit -m "designdoc: split FindRepoRoot out of FindCorpus; lode show honors spec_corpus"
```

### Task 3 — Corpus loader with file-derived identity

```yaml
kind: feature
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

The sync's read side (025 §16.1, §3, §5): load every document from the configured
corpora through `designdoc.Parse`, derive kind, ordinal, status, title,
anchored sections, and frontmatter edges, and derive plan ordinals corpus-wide.
Pure library code — no git, no HTTP.

**Files:**
- Create: `internal/designdoc/corpus.go`
- Test: `internal/designdoc/corpus_test.go`

**Interfaces produced (consumed by part 3's `lode doc sync`):**

```go
type SectionMeta struct {
	Anchor   string // "sec-4.1a", never empty — anchorless headings are skipped
	Heading  string
	Depth    int // 2..6
	Position int // 0-based document order over the anchored sections
}

type EdgeMeta struct {
	SrcAnchor    string // "" = document-level ("." in the AnchorMap)
	Rel          string // implements | amends | amendedBy | replaces | isReplacedBy
	Target       string // the raw reference with any fragment stripped; "NO-SPEC" allowed
	TargetAnchor string // "sec-2" when the reference carried #sec-2, else ""
}

type CorpusDoc struct {
	Path, Filename  string
	Kind            string // "spec" | "adr" | "plan"
	Ordinal         string // "14" for spec/adr; "34-1" for plan (025 §16.3)
	Status, Title   string
	Source          []byte          // the full file, frontmatter included
	FrontmatterJSON json.RawMessage // the YAML header re-encoded as JSON
	Sections        []SectionMeta   // empty for plans (025 §9)
	Edges           []EdgeMeta
}

// LoadSyncCorpus loads specDir as SPEC/ADR documents and planDir as PLAN
// documents; either may be "" (that corpus is not configured). Results are
// sorted by kind then filename.
func LoadSyncCorpus(specDir, planDir string) ([]CorpusDoc, error)
```

Rules the implementation must enforce (each one is a test):

- glob `*.md` per directory (matching `corpusFilenames`), so `index.yaml`
  never syncs;
- missing frontmatter, unparseable frontmatter, or empty `status` → error
  naming the file (025 §16.2: a sync error, not a skipped row);
- spec/adr ordinal from `leadingNumber(filename)` (resolve.go:233); a
  spec-corpus file without a leading number → error;
- title = the first `# ` heading line of the preamble, hash and whitespace
  stripped; no H1 → error naming the file;
- sections: only `Section.Anchor != ""` entries, as `SectionMeta`; a
  duplicate anchor within one document → error;
- edges from frontmatter, exactly spec 025 §5.1's relation list: plans'
  `implements` (one edge per ref, document-level), and both kinds'
  `amends`/`amendedBy`/`replaces`/`isReplacedBy` AnchorMaps (map key `"."` →
  `SrcAnchor: ""`, `"#sec-3"` → `"sec-3"`); `requires`/`isRequiredBy`/`task`
  are not synced. No frontmatter key produces `blocks` today — the store
  accepts the rel (part 2) but the extractor emits none;
- plan spec-ordinal: first `implements` entry; `NO-SPEC` or an empty list →
  `0`; otherwise `leadingNumber(path.Base(<ref sans fragment>))` — no leading
  number → error;
- plan plan-ordinal: within each spec-ordinal group, ascending by filename,
  1-based; `Ordinal = fmt.Sprintf("%d-%d", specOrd, i+1)`;
- `FrontmatterJSON`: `yaml.Unmarshal` the header's inner YAML into
  `map[string]any`, then `json.Marshal` (both accessible inside the package
  via `Frontmatter.inner`).

- [ ] **Step 1: Write the failing tests** — create
  `internal/designdoc/corpus_test.go`:

```go
package designdoc_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func writeDoc(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const specSrc = `---
status: accepted
issued: 2026-01-01
amends:
  "#sec-1":
    - 025-documents-in-the-backbone.md#sec-2
---
# Spec 034 — Design-doc sync

## 0. Why {#sec-0}

Intro.

## 1. Scope {#sec-1}

Body.
`

const adrSrc = `---
status: draft
kind: adr
---
# ADR 7 — A decision

## 1. Decision {#sec-1}

Text.
`

func specPlanDirs(t *testing.T) (string, string) {
	t.Helper()
	specDir, planDir := t.TempDir(), t.TempDir()
	writeDoc(t, specDir, "034-design-doc-sync.md", specSrc)
	writeDoc(t, specDir, "007-a-decision.md", adrSrc)
	writeDoc(t, planDir, "2026-08-09-sync-1-foundations.md",
		"---\nstatus: draft\nimplements: docs/specs/025-documents-in-the-backbone.md\n---\n# Part 1\n\nProse.\n")
	writeDoc(t, planDir, "2026-08-10-sync-2-store.md",
		"---\nstatus: draft\nimplements: docs/specs/025-documents-in-the-backbone.md\n---\n# Part 2\n\nProse.\n")
	writeDoc(t, planDir, "2026-07-01-standalone.md",
		"---\nstatus: draft\nimplements: NO-SPEC\n---\n# Standalone\n\nProse.\n")
	return specDir, planDir
}

func TestLoadSyncCorpusIdentity(t *testing.T) {
	specDir, planDir := specPlanDirs(t)
	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	got := map[string]string{} // filename -> kind/ordinal
	for _, d := range docs {
		got[d.Filename] = d.Kind + "/" + d.Ordinal
	}
	want := map[string]string{
		"034-design-doc-sync.md":            "spec/34",
		"007-a-decision.md":                 "adr/7",
		"2026-08-09-sync-1-foundations.md":  "plan/34-1",
		"2026-08-10-sync-2-store.md":        "plan/34-2",
		"2026-07-01-standalone.md":          "plan/0-1",
	}
	for f, w := range want {
		if got[f] != w {
			t.Errorf("%s: identity = %q, want %q", f, got[f], w)
		}
	}
}

func TestLoadSyncCorpusSectionsAndEdges(t *testing.T) {
	specDir, planDir := specPlanDirs(t)
	docs, err := designdoc.LoadSyncCorpus(specDir, planDir)
	if err != nil {
		t.Fatalf("LoadSyncCorpus: %v", err)
	}
	byFile := map[string]designdoc.CorpusDoc{}
	for _, d := range docs {
		byFile[d.Filename] = d
	}

	spec := byFile["034-design-doc-sync.md"]
	if spec.Title != "Spec 034 — Design-doc sync" || spec.Status != "accepted" {
		t.Errorf("spec title/status = %q/%q", spec.Title, spec.Status)
	}
	if len(spec.Sections) != 2 || spec.Sections[0].Anchor != "sec-0" ||
		spec.Sections[1].Anchor != "sec-1" || spec.Sections[1].Depth != 2 ||
		spec.Sections[1].Position != 1 {
		t.Errorf("spec sections = %+v", spec.Sections)
	}
	if len(spec.Edges) != 1 || spec.Edges[0] != (designdoc.EdgeMeta{
		SrcAnchor: "sec-1", Rel: "amends",
		Target: "025-documents-in-the-backbone.md", TargetAnchor: "sec-2",
	}) {
		t.Errorf("spec edges = %+v", spec.Edges)
	}
	if !strings.Contains(string(spec.FrontmatterJSON), `"status":"accepted"`) {
		t.Errorf("FrontmatterJSON = %s", spec.FrontmatterJSON)
	}

	plan := byFile["2026-08-09-sync-1-foundations.md"]
	if len(plan.Sections) != 0 {
		t.Errorf("plan carries sections: %+v (025 §9: plans take none)", plan.Sections)
	}
	if len(plan.Edges) != 1 || plan.Edges[0] != (designdoc.EdgeMeta{
		Rel: "implements", Target: "docs/specs/025-documents-in-the-backbone.md",
	}) {
		t.Errorf("plan edges = %+v", plan.Edges)
	}
	noSpec := byFile["2026-07-01-standalone.md"]
	if len(noSpec.Edges) != 1 || noSpec.Edges[0].Target != "NO-SPEC" {
		t.Errorf("NO-SPEC plan edges = %+v", noSpec.Edges)
	}
}

func TestLoadSyncCorpusErrors(t *testing.T) {
	cases := map[string]struct{ dir, name, content string }{
		"bad frontmatter":   {"spec", "010-bad.md", "---\nstatus: [unclosed\n---\n# T\n"},
		"no frontmatter":    {"spec", "011-none.md", "# T\n\nBody.\n"},
		"no status":         {"spec", "012-nostatus.md", "---\nissued: 2026-01-01\n---\n# T\n"},
		"no leading number": {"spec", "notes.md", "---\nstatus: draft\n---\n# T\n"},
		"no h1":             {"spec", "013-noh1.md", "---\nstatus: draft\n---\nBody only.\n"},
		"dup anchor":        {"spec", "014-dup.md", "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\n## 2. B {#sec-1}\n"},
		"plan bad implements": {"plan", "2026-01-01-p.md",
			"---\nstatus: draft\nimplements: docs/specs/nonumber.md\n---\n# P\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			specDir, planDir := t.TempDir(), t.TempDir()
			if tc.dir == "spec" {
				writeDoc(t, specDir, tc.name, tc.content)
			} else {
				writeDoc(t, planDir, tc.name, tc.content)
			}
			if _, err := designdoc.LoadSyncCorpus(specDir, planDir); err == nil {
				t.Fatalf("LoadSyncCorpus accepted %s; want error", name)
			} else if !strings.Contains(err.Error(), tc.name) {
				t.Errorf("error %q does not name the file %s", err, tc.name)
			}
		})
	}
}

func TestLoadSyncCorpusEmptyDirsAreOptional(t *testing.T) {
	docs, err := designdoc.LoadSyncCorpus("", "")
	if err != nil || len(docs) != 0 {
		t.Fatalf("LoadSyncCorpus(\"\",\"\") = %v, %v; want empty, nil", docs, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/designdoc -run TestLoadSyncCorpus -v`
Expected: compile error — `LoadSyncCorpus`, `CorpusDoc` undefined.

- [ ] **Step 3: Implement** — create `internal/designdoc/corpus.go`
  (`package designdoc`). Skeleton with the load-bearing logic:

```go
package designdoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// (SectionMeta, EdgeMeta, CorpusDoc — exactly as in the interface block above.)

// LoadSyncCorpus loads the configured corpora for `lode doc sync` (025 §16.1/§5).
func LoadSyncCorpus(specDir, planDir string) ([]CorpusDoc, error) {
	var out []CorpusDoc
	if specDir != "" {
		files, err := corpusFilenames(specDir)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			d, err := loadSpecOrADR(specDir, f)
			if err != nil {
				return nil, err
			}
			out = append(out, d)
		}
	}
	if planDir != "" {
		plans, err := loadPlans(planDir)
		if err != nil {
			return nil, err
		}
		out = append(out, plans...)
	}
	return out, nil
}

// loadDoc parses one file and derives the kind-independent fields: status,
// title, FrontmatterJSON. Frontmatter absence, a parse failure, an empty
// status, or a missing H1 are sync errors naming the file (025 §16.2).
func loadDoc(dir, name string) (*Document, CorpusDoc, error) {
	p := filepath.Join(dir, name)
	src, err := os.ReadFile(p)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("read %s: %w", p, err)
	}
	doc, err := Parse(src)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	if doc.Frontmatter == nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: no frontmatter", name)
	}
	if doc.Frontmatter.Status == "" {
		return nil, CorpusDoc{}, fmt.Errorf("%s: frontmatter has no status", name)
	}
	title, ok := docTitle(doc.Preamble)
	if !ok {
		return nil, CorpusDoc{}, fmt.Errorf("%s: no H1 title", name)
	}
	fmJSON, err := frontmatterJSON(doc.Frontmatter)
	if err != nil {
		return nil, CorpusDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	return doc, CorpusDoc{
		Path: p, Filename: name,
		Status: doc.Frontmatter.Status, Title: title,
		Source: src, FrontmatterJSON: fmJSON,
	}, nil
}
```

plus, in the same file: `docTitle(preamble string) (string, bool)` (first line
starting `# `, trimmed); `frontmatterJSON` (`yaml.Unmarshal([]byte(f.inner),
&m)` into `map[string]any`, then `json.Marshal`); `loadSpecOrADR` (calls
`loadDoc`, sets `Kind` from `Frontmatter.Kind == "adr"`, `Ordinal` from
`leadingNumber(name)` — error when `!ok` — then `sectionMetas` and
`anchorEdges`); `sectionMetas(doc *Document, name string) ([]SectionMeta,
error)` (anchored sections only, duplicate-anchor error); `anchorEdges(fm
*Frontmatter) []EdgeMeta` iterating the four AnchorMaps with their rel names
in a fixed order (`amends`, `amendedBy`, `replaces`, `isReplacedBy`), sorting
map keys for determinism, splitting each value with `splitFragment`
(resolve.go:189); `loadPlans(planDir string)` doing the two passes:

```go
func loadPlans(planDir string) ([]CorpusDoc, error) {
	files, err := corpusFilenames(planDir) // already sorted ascending
	if err != nil {
		return nil, err
	}
	type pending struct {
		doc     CorpusDoc
		specOrd int
	}
	var plans []pending
	for _, f := range files {
		doc, cd, err := loadDoc(planDir, f) // doc.Sections deliberately unused: plans carry none (025 §9)
		if err != nil {
			return nil, err
		}
		cd.Kind = "plan"
		specOrd, err := planSpecOrdinal(doc.Frontmatter, f)
		if err != nil {
			return nil, err
		}
		for _, ref := range doc.Frontmatter.Implements {
			base, frag := splitFragment(ref)
			cd.Edges = append(cd.Edges, EdgeMeta{Rel: "implements", Target: base, TargetAnchor: frag})
		}
		cd.Edges = append(cd.Edges, anchorEdges(doc.Frontmatter)...)
		plans = append(plans, pending{doc: cd, specOrd: specOrd})
	}
	// Second pass: number within each spec-ordinal group, ascending filename
	// (files is sorted, so arrival order is corpus order — 025 §16.3).
	counts := map[int]int{}
	var out []CorpusDoc
	for _, p := range plans {
		counts[p.specOrd]++
		p.doc.Ordinal = fmt.Sprintf("%d-%d", p.specOrd, counts[p.specOrd])
		out = append(out, p.doc)
	}
	return out, nil
}

// planSpecOrdinal derives the plan id's spec ordinal from the first
// implements entry (025 §16.3): NO-SPEC or an absent key → 0.
func planSpecOrdinal(fm *Frontmatter, name string) (int, error) {
	if len(fm.Implements) == 0 {
		return 0, nil
	}
	base, _ := splitFragment(fm.Implements[0])
	if base == "NO-SPEC" {
		return 0, nil
	}
	n, ok := leadingNumber(path.Base(base))
	if !ok {
		return 0, fmt.Errorf("%s: implements %q has no leading spec number to derive the plan id from (025 §16.3)", name, base)
	}
	return n, nil
}
```

(`CorpusDoc` deliberately carries only the JSON form of the header; callers
inside the package that need the parsed struct use the `*Document` `loadDoc`
returns — `doc.Frontmatter` is exported.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/designdoc -v`
Expected: all PASS, including the pre-existing parser tests.

- [ ] **Step 5: Sanity-check against the real corpus** — a throwaway
  verification, not a committed test (the repo's own corpus is a moving
  target):

```bash
cat > /tmp/corpuscheck_test.go <<'EOF'
package designdoc_test

import (
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

func TestRealCorpusLoads(t *testing.T) {
	docs, err := designdoc.LoadSyncCorpus("../../docs/specs", "../../docs/plans")
	if err != nil {
		t.Fatalf("real corpus: %v", err)
	}
	for _, d := range docs {
		t.Logf("%-6s %-8s %s", d.Kind, d.Ordinal, d.Filename)
	}
}
EOF
cp /tmp/corpuscheck_test.go internal/designdoc/
go test ./internal/designdoc -run TestRealCorpusLoads -v
rm internal/designdoc/corpuscheck_test.go
```

Expected: PASS with a plausible identity line per real document. If a real
document trips a loader error, fix the loader (or flag the document to the
human) before committing.

- [ ] **Step 6: Commit**

```bash
git add internal/designdoc/corpus.go internal/designdoc/corpus_test.go
git commit -m "designdoc: corpus loader with file-derived doc identity (spec 025 §16.3)"
```

### Task 4 — lode task add --body-file

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
```

Spec 025 §18: `--body-file <file>` beside `--body <string>`, matching `gh` —
`-` reads stdin, and the two flags are mutually exclusive.

**Files:**
- Modify: `internal/cmd/task.go:57-103` (`newTaskAddCmd`)
- Test: `internal/cmd/task_test.go` (exists)

**Interfaces produced:**

```go
// resolveBody returns the task body: bodyFile's contents when set ("-" =
// stdin), else body. Flag exclusivity is cobra's job, not this function's.
func resolveBody(body, bodyFile string, stdin io.Reader) (string, error)
```

- [ ] **Step 1: Write the failing tests** — append to
  `internal/cmd/task_test.go`:

```go
func TestResolveBody(t *testing.T) {
	f := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(f, []byte("from file\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		body, bodyFile, stdin, want string
		wantErr                     bool
	}{
		"inline":       {body: "inline", want: "inline"},
		"file":         {bodyFile: f, want: "from file\n"},
		"stdin":        {bodyFile: "-", stdin: "from stdin", want: "from stdin"},
		"missing file": {bodyFile: filepath.Join(t.TempDir(), "nope.md"), wantErr: true},
		"neither":      {want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := resolveBody(tc.body, tc.bodyFile, strings.NewReader(tc.stdin))
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolveBody = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTaskAddBodyFlagsMutuallyExclusive(t *testing.T) {
	cmd := newTaskAddCmd()
	cmd.SetArgs([]string{"--title", "t", "--body", "x", "--body-file", "y"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "none of the others can be") {
		t.Fatalf("err = %v; want cobra mutual-exclusion error", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cmd -run 'TestResolveBody|TestTaskAddBodyFlagsMutuallyExclusive' -v`
Expected: compile error — `resolveBody` undefined.

- [ ] **Step 3: Implement** in `internal/cmd/task.go`. Add the helper (near
  the top of the file, after the imports — it already imports `io` and `os`):

```go
// resolveBody returns the task body from --body / --body-file (spec 025 §18,
// the gh convention): bodyFile wins when set, with "-" reading stdin. Flag
// exclusivity is enforced by cobra (MarkFlagsMutuallyExclusive), not here.
func resolveBody(body, bodyFile string, stdin io.Reader) (string, error) {
	if bodyFile == "" {
		return body, nil
	}
	if bodyFile == "-" {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(bodyFile)
	if err != nil {
		return "", fmt.Errorf("read body file: %w", err)
	}
	return string(b), nil
}
```

In `newTaskAddCmd`: add `var bodyFile string` to the var block; register the
flag and exclusivity after the existing `--body` flag:

```go
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "read the task body from a file (\"-\" for stdin)")
	cmd.MarkFlagsMutuallyExclusive("body", "body-file")
```

and at the top of `RunE`, before `newAPIClientWithConfig`:

```go
			body, err := resolveBody(body, bodyFile, cmd.InOrStdin())
			if err != nil {
				return err
			}
```

(`body` is already the name of the captured flag variable; the local
shadowing keeps the `CreateTaskInput` literal below unchanged.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/task.go internal/cmd/task_test.go
git commit -m "task add: --body-file, mutually exclusive with --body (spec 025 §18)"
```

### Task 5 — wl:Plan in ns/

```yaml
kind: chore
priority: medium
```

Spec 025 §17: mirror 025 §9's accepted reintroduction of plans-as-documents
into `ns/` — `wl:Plan` as a **sibling** of `wl:DesignDoc` (not a subclass),
taking no sections or anchors, plus the shape its synced `status` needs. The
governing spec already exists; this is the mirror catching up, so no spec edit
is required. `wlc:TaskKind` must not be touched (CLAUDE.md).

**Files:**
- Modify: `ns/ontology.ttl` (class block after `wl:Section` ~L68-74;
  disjointness members ~L215-217)
- Modify: `ns/shapes.ttl` (after `wl:DesignDocShape` ~L55-62)

- [ ] **Step 1: Add the class** — in `ns/ontology.ttl`, after the
  `wl:Section` block (~L74), insert:

```ttl
wl:Plan a owl:Class ;
    rdfs:subClassOf foaf:Document , prov:Entity ;
    wl:layer wlc:intent ;
    rdfs:comment """An executable document: accepting it mints its tasks (025 §4-§5). A sibling of
        wl:DesignDoc, not a subclass — a plan is spent by execution rather than superseded, and
        takes no wl:Section parts and no anchors. 025 §2 dropped the class when plan-shaped work
        was a task subtree; 025 §9 (accepted) reintroduced plans as documents, mirrored here by
        spec 025 §17.""" .
```

- [ ] **Step 2: Extend the disjointness axiom** — in the first
  `owl:AllDisjointClasses` members list (~L216), add `wl:Plan` after
  `wl:Section`:

```ttl
[] a owl:AllDisjointClasses ;
   owl:members ( wl:Component wl:DesignDoc wl:Section wl:Plan wl:Task wl:Deliverable wl:Workstream
                 wl:Skill wl:Issue wl:PullRequest ) .
```

(Disjoint from `wl:DesignDoc` is exactly what "sibling, not subclass" means.)

- [ ] **Step 3: Add the shape** — in `ns/shapes.ttl`, after
  `wl:DesignDocShape` (~L62), insert:

```ttl
wl:PlanShape a sh:NodeShape ;
    sh:targetClass wl:Plan ;
    sh:property [
        sh:path wl:status ;
        sh:minCount 1 ;
        sh:node [ sh:property [ sh:path skos:inScheme ; sh:hasValue wlc:DesignDocStatus ] ] ;
        sh:message "A Plan carries exactly one wl:status, drawn from wlc:DesignDocStatus (025 §17; plans reuse the editorial lifecycle enum)." ;
    ] .
```

(No new SKOS scheme: the synced kind's `status` values are the existing
`wlc:DesignDocStatus` concepts, so `ns/concept.ttl` is untouched.)

- [ ] **Step 4: Validate**

Run: `riot --validate ns/ontology.ttl ns/concept.ttl ns/shapes.ttl`
(If `riot` is missing: `brew install jena`, then re-run.)
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add ns/ontology.ttl ns/shapes.ttl
git commit -m "ns: mirror 025 §9's wl:Plan — sibling of wl:DesignDoc, no sections (spec 025 §17)"
```
