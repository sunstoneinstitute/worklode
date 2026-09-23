# Clause Model Increment 4a: Gate Trailer and Spec Reconciler Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A project can turn on the design authority gate in `.worklode/config.toml`, a pull request touching a guarded path must carry a `Spec:` trailer that `lode gate check` validates in CI, and a server-side subscriber turns that trailer into a `governedBy` link on the task the branch names when no plan governs it.

**Architecture:** A new pure package `internal/gate` holds the `[gate]` config, guarded-path matching and the trailer grammar, so the CLI (offline, in CI) and the server (the reconciler) parse the same thing. `lode gate check` in `internal/cmd` reads the repo config, diffs base to head with `internal/gitexec`, and exits non-zero with the reason. The reconciler is a second `eventbus` subscriber, `spec-reconciler`, beside `doc-lifecycle`: it reads the GitHub `pull_request.*` and `push` events the hooks already record, finds the task from the branch name or the `Worklode-Task:` trailer, and writes `task_governed_by` rows with a new `source = 'gate'` through the existing `Govern`. One migration widens that CHECK.

**Tech Stack:** Go, Postgres, `github.com/pelletier/go-toml/v2` (already a dependency), cobra, the `eventbus` loop, GitHub Actions.

**Spec:** `docs/specs2/12-spec-refactoring-design-tree.md` S4, S18, S29, S30; `docs/specs2/11-design-authority-gate.md` §3 and §4. Tracking issue #687, "Increment 4" minus lineage and migration.

**Base:** branch `clause-increment-4a` from `clause-increment-2` at `88174a01`. Sibling of `clause-increment-3` (same base); see the coordination notes in the preface.

## Preface: scope, rulings, deferrals

Rulings, recorded in spec 12 as S50 to S54 by Task 8:

- R1 (S50) `lode gate check` is offline and syntactic: it validates the trailer's form and the closed `none` list against the repo config and the diff, and never calls the server. Resolving the cited clause against the backbone is the reconciler's job. Cost if wrong: a well-formed trailer naming a clause that does not exist passes CI, and the reconciler counts it as `unknown_target`.
- R2 (S51) The gate is the third writer of governing links, `source = 'gate'`. It writes only when the task carries no `source = 'plan'` link, it is idempotent, and nothing removes a gate link automatically. Cost if wrong: a task planned after its PR opened keeps a redundant gate link until an architect ungoverns it.
- R3 (S52) Section refs (`Spec: WL-SPEC-4 sec-5`) are accepted everywhere until increment 3's per-project settings land; the transitional switch is then one allowlist line, key `gate_trailer_sections`, in `internal/store/projectsettings.go`. Not a column, not a `.worklode/config.toml` key. Cost if wrong: none now; one line later.
- R4 (S53) The `[gate]` table is decoded with `go-toml/v2` in `internal/gate`. The hand-rolled key=value parser in `internal/cli` learns to skip TOML tables so every other `lode` command keeps working in a repo that enables the gate. Cost if wrong: none; the two parsers never read the same keys.
- R5 (S54) CI runs `lode gate check` on `pull_request` events only, in a job that exits 0 when the repo has no `[gate]` table. Worklode's own `.worklode/config.toml` does not gain a `[gate]` table in this increment; enabling it is a one-line follow-up once the trailer habit exists. Cost if wrong: none; the job is a no-op until then.

Deferred with a note in the specs:

- `lode doctor` warning when a gated repository does not require pull requests. The store knows branch rules (`BranchRules`, from the `repository_ruleset` hook) but no API route exposes them; `lode doctor` only parses the `[gate]` table in this increment.
- The per-project trailer key: `[gate] trailer` is read by `lode gate check`; the reconciler uses `Spec:` (the default). A project that renames the key gets a working CI gate and a silent reconciler until the key reaches the server with increment 3's settings.
- Lineage and split/merge (S22), the refactor primitive (S24), A2 migration, and the refactor process as a writer of links (S3) beyond what increment 3 does at plan close.

Coordination with increment 3 (sibling branch `clause-increment-3`): increment 3 owns migration `0084_plan_lifecycle`, the `projects.settings` jsonb column and `internal/store/projectsettings.go`, `SetClauseStatus`, the S23 staleness trigger, and the rename of spec 12's "Recorded from increment 1" heading. This increment takes migration `0085_gate_source` and S50 to S54. Whichever branch rebases second lets `check-migrations.sh` renumber. Shared files that will conflict on rebase, all with one-line additions on this side: `docs/specs2/12` recorded list, `docs/specs2/09` L1 list, L3 allowlist and the command table, `internal/cmd/namerule_test.go`, `deploy/base/kustomization.yaml`.

## Global Constraints

- Every `go build` and `go test` carries `-trimpath`; prefer `make test`, `make vet`. Never bare `go test`.
- Store and API tests need Postgres on `localhost:5432` and skip silently without it; confirm a run with `-v`. Another SDD run shares this Postgres, so a package timeout on an unrelated test is re-run once in isolation before it counts as a failure.
- Commit messages carry no `Co-authored-by` and no reference to AI or agents.
- Prose in `docs/specs2/` is plain language: no em dash character, no "X, not Y" / "rather than" / "instead of" antithesis.
- ADR 036: every shape crossing HTTP lives in `internal/model` (stdlib only). This increment adds none.
- Every route is in `internal/api/router.go`'s `routeGuards`. This increment adds no route.
- `internal/cmd` decides, `internal/cli` renders. `lode gate check` prints a verdict over values that never cross the API, which `internal/cmd` may print itself (CLAUDE.md, "What legitimately renders in internal/cmd").
- New command names follow `internal/cmd/CLAUDE.md`'s nine rules; `internal/cmd/namerule_test.go` enforces them. `plugins/claude/lode/skills/worklode/references/commands.md` is regenerated when a command is added (`TestCommandReference` says how). `docs/agent-surfaces.md` §"When the CLI changes" is read and followed.
- Migrations: numbered by `./scripts/check-migrations.sh`, listed in `deploy/base/kustomization.yaml` in the same commit, with a down file that reverses the up (`deploy/base/AGENTS.md`).
- Store writes map Postgres constraint violations to sentinel errors (`internal/store/AGENTS.md`).
- `lode-hook` and `lode-statusline` must not gain imports of cobra, goldmark, `internal/api`, `internal/store`, `internal/watch`, Prometheus or Kubernetes (`internal/disttest/deps_test.go`). `internal/gate` is imported by `internal/cmd` and `internal/api` only; `internal/cli` does not import it.
- Server-side loops with meaningful outcomes get `worklode_*` metrics with bounded labels (CLAUDE.md, "Metrics").
- Workflow edits follow the `worklode-ci` skill: the docs-only skip and the `can-be-tested` label keep working.

---

### Task 1: `internal/gate` config and guarded paths

**Files:**
- Create: `internal/gate/config.go`
- Create: `internal/gate/config_test.go`
- Modify: `internal/cli/client.go` (`parseConfig`, around lines 380 to 420)
- Test: `internal/cli/client_test.go` (find the existing `parseConfig` tests and add one)

**Interfaces:**
- Produces: `gate.Config{Paths, Regex []string; Trailer string}`, `gate.DefaultTrailer = "Spec:"`, `gate.Parse(data []byte) (Config, bool, error)`, `gate.Load(repoRoot string) (Config, bool, error)`, `gate.Guards`, `(Config) Guards() (Guards, error)`, `(Guards) Match(path string) bool`.

- [ ] **Step 1: Write the failing tests**

```go
package gate

import "testing"

func TestParseGateTable(t *testing.T) {
	data := []byte(`current_project = "worklode"
project_key = "WL"

[gate]
paths = ["internal/cmd/**", "ns/*.ttl"]
regex = ["^internal/store/(tasks|claim)\\.go$"]
`)
	cfg, ok, err := Parse(data)
	if err != nil || !ok {
		t.Fatalf("Parse: ok=%v err=%v", ok, err)
	}
	if cfg.Trailer != "Spec:" {
		t.Errorf("Trailer defaults to Spec:, got %q", cfg.Trailer)
	}
	if len(cfg.Paths) != 2 || len(cfg.Regex) != 1 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestParseNoGateTable(t *testing.T) {
	_, ok, err := Parse([]byte(`current_project = "worklode"` + "\n"))
	if err != nil || ok {
		t.Fatalf("no table: ok=%v err=%v, want false nil", ok, err)
	}
}

func TestParseEmptyGateTableIsAnError(t *testing.T) {
	if _, _, err := Parse([]byte("[gate]\ntrailer = \"Spec:\"\n")); err == nil {
		t.Fatal("a [gate] table naming no paths and no regex must be refused")
	}
	if _, _, err := Parse([]byte("[gate]\nregex = [\"(\"]\n")); err == nil {
		t.Fatal("an invalid regex must be refused at parse time")
	}
}

func TestGuardsMatch(t *testing.T) {
	cfg := Config{
		Paths: []string{"internal/cmd/**", "ns/*.ttl", "**/router.go", "deploy/base/**"},
		Regex: []string{`^internal/store/(tasks|claim)\.go$`},
	}
	g, err := cfg.Guards()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"internal/cmd/gate.go":            true,
		"internal/cmd/sub/deep.go":        true,
		"internal/cmdx/gate.go":           false,
		"ns/concept.ttl":                  true,
		"ns/sub/concept.ttl":              false,
		"internal/api/router.go":          true,
		"router.go":                       true,
		"deploy/base/migrations/0085.sql": true,
		"internal/store/tasks.go":         true,
		"internal/store/docs.go":          false,
		"README.md":                       false,
	}
	for p, want := range cases {
		if got := g.Match(p); got != want {
			t.Errorf("Match(%q) = %v, want %v", p, got, want)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -trimpath ./internal/gate -run 'TestParse|TestGuards' -v`
Expected: FAIL to build, `undefined: Parse`, `undefined: Config`.

- [ ] **Step 3: Write `internal/gate/config.go`**

```go
// Package gate is the design authority gate (11-design-authority-gate.md §3,
// §4; 12-spec-refactoring-design-tree.md S4, S29, S30): which changed paths
// ask for a Spec: trailer, and what a trailer may say. It is pure so the CLI
// (in CI, offline) and the server's reconciler parse the same thing.
package gate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// DefaultTrailer is the key a declaration line starts with when the [gate]
// table names none.
const DefaultTrailer = "Spec:"

// Config is the [gate] table of .worklode/config.toml (11 §3). Paths are
// globstar patterns, Regex is for what a glob cannot say, Trailer is the key
// the PR body or commit message must carry.
type Config struct {
	Paths   []string `toml:"paths"`
	Regex   []string `toml:"regex"`
	Trailer string   `toml:"trailer"`
}

// Load reads the [gate] table from repoRoot/.worklode/config.toml. ok is
// false when the file or the table is absent: the gate is off (11 §3,
// "Without a [gate] table nothing runs").
func Load(repoRoot string) (Config, bool, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, ".worklode", "config.toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, false, nil
	}
	if err != nil {
		return Config{}, false, err
	}
	return Parse(data)
}

// Parse decodes the [gate] table out of a whole config file. Other keys are
// ignored; internal/cli owns them.
func Parse(data []byte) (Config, bool, error) {
	var file struct {
		Gate *Config `toml:"gate"`
	}
	if err := toml.Unmarshal(data, &file); err != nil {
		return Config{}, false, fmt.Errorf("parse .worklode/config.toml: %w", err)
	}
	if file.Gate == nil {
		return Config{}, false, nil
	}
	cfg := *file.Gate
	if cfg.Trailer == "" {
		cfg.Trailer = DefaultTrailer
	}
	if len(cfg.Paths) == 0 && len(cfg.Regex) == 0 {
		return Config{}, false, errors.New("[gate] names no paths and no regex")
	}
	if _, err := cfg.Guards(); err != nil {
		return Config{}, false, err
	}
	return cfg, true, nil
}

// Guards is the compiled form of Config's paths and regex.
type Guards struct {
	patterns []*regexp.Regexp
}

// Guards compiles the config. A bad regex is reported with its source.
func (c Config) Guards() (Guards, error) {
	var g Guards
	for _, p := range c.Paths {
		g.patterns = append(g.patterns, globRegexp(p))
	}
	for _, r := range c.Regex {
		re, err := regexp.Compile(r)
		if err != nil {
			return Guards{}, fmt.Errorf("[gate] regex %q: %w", r, err)
		}
		g.patterns = append(g.patterns, re)
	}
	return g, nil
}

// Match reports whether a repo-relative path is guarded.
func (g Guards) Match(path string) bool {
	for _, re := range g.patterns {
		if re.MatchString(path) {
			return true
		}
	}
	return false
}

// globRegexp turns a globstar pattern into an anchored regexp: `**` crosses
// slashes (`**/` also matches nothing, so `**/x` matches `x`), `*` and `?`
// stay inside one path segment.
func globRegexp(glob string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(glob); i++ {
		c := glob[i]
		switch {
		case c == '*' && i+1 < len(glob) && glob[i+1] == '*':
			i++
			if i+1 < len(glob) && glob[i+1] == '/' {
				i++
				b.WriteString(`(?:.*/)?`)
			} else {
				b.WriteString(`.*`)
			}
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}
```

- [ ] **Step 4: Make `internal/cli`'s parser skip TOML tables**

In `internal/cli/client.go`, `parseConfig` fails on any line without `=`. A `[gate]` table header, and the keys inside it, must be skipped so every `lode` command keeps working in a gated repo. Add a `table` flag to the loop, before the `strings.Cut`:

```go
	inTable := false
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			// A TOML table. The keys this parser knows are all top-level;
			// tables belong to other readers (internal/gate reads [gate]).
			inTable = true
			continue
		}
		if inTable {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
```

Update the doc comment above `Config` (around line 36) with one sentence: "Tables such as `[gate]` are skipped here and read by their own package." Add a test beside the existing `parseConfig` tests:

```go
func TestParseConfigSkipsTables(t *testing.T) {
	cfg, err := parseConfig("current_project = \"worklode\"\n\n[gate]\npaths = [\"internal/cmd/**\"]\ntrailer = \"Spec:\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CurrentProject != "worklode" {
		t.Errorf("CurrentProject = %q", cfg.CurrentProject)
	}
}
```

- [ ] **Step 5: Run the tests**

Run: `go test -trimpath ./internal/gate ./internal/cli -run 'TestParse|TestGuards' -v`
Expected: PASS for all four gate tests and the cli test.

- [ ] **Step 6: Commit**

```bash
git add internal/gate/config.go internal/gate/config_test.go internal/cli/client.go internal/cli/client_test.go
git commit -m "Read the [gate] table and match guarded paths (S29)"
```

---

### Task 2: the trailer grammar and the gate verdict

**Files:**
- Create: `internal/gate/trailer.go`
- Create: `internal/gate/trailer_test.go`

**Interfaces:**
- Consumes: `designdoc.ParseClauseRef(base) (ClauseRef, bool)`, `designdoc.ParseShorthand(base) (Shorthand, bool)`, `designdoc.SectionRef{Shorthand, Anchor}` (all exist on this branch, `internal/designdoc/resolve.go`).
- Produces: `gate.Declaration`, `gate.NoneReasons`, `gate.ErrNoTrailer`, `gate.Find(key, text string) (Declaration, error)`, `gate.ParseLine(key, line string) (Declaration, bool, error)`, `gate.Input{Changed, Texts []string}`, `gate.Check(cfg Config, in Input) (Verdict, error)`, `gate.Verdict{Guarded []string; Declaration *Declaration}`.

- [ ] **Step 1: Write the failing tests**

```go
package gate

import (
	"errors"
	"strings"
	"testing"
)

func TestParseLineForms(t *testing.T) {
	ok := map[string]func(Declaration) bool{
		"Spec: WL-CL-456":                func(d Declaration) bool { return d.Clause != nil && d.Clause.Number == 456 && d.Qualifier == "" },
		"Spec: WL-CL-456 amended":        func(d Declaration) bool { return d.Clause != nil && d.Qualifier == "amended" },
		"Spec: WL-CL-456 fix":            func(d Declaration) bool { return d.Clause != nil && d.Qualifier == "fix" },
		"Spec: WL-SPEC-4 sec-5":          func(d Declaration) bool { return d.Section != nil && d.Section.Anchor == "sec-5" && d.Section.Shorthand.Number == 4 },
		"Spec: WL-SPEC-4#sec-5 amended":  func(d Declaration) bool { return d.Section != nil && d.Section.Anchor == "sec-5" && d.Qualifier == "amended" },
		"Spec: none refactor":            func(d Declaration) bool { return d.None == "refactor" && d.Clause == nil },
		"  Spec:   none   tests  ":       func(d Declaration) bool { return d.None == "tests" },
	}
	for line, check := range ok {
		d, matched, err := ParseLine("Spec:", line)
		if err != nil || !matched {
			t.Errorf("ParseLine(%q): matched=%v err=%v", line, matched, err)
			continue
		}
		if !check(d) {
			t.Errorf("ParseLine(%q) = %+v", line, d)
		}
	}
	bad := []string{
		"Spec: none fix",          // 11 §4: a fix with nothing to cite is refused
		"Spec: none",              // no reason
		"Spec: none later",        // not in the closed list
		"Spec: WL-SPEC-4",         // a section ref needs an anchor
		"Spec: WL-CL-456 sometime", // unknown qualifier
		"Spec: 456",
		"Spec:",
	}
	for _, line := range bad {
		if _, matched, err := ParseLine("Spec:", line); !matched || err == nil {
			t.Errorf("ParseLine(%q) should match the key and fail, got matched=%v err=%v", line, matched, err)
		}
	}
	if _, matched, _ := ParseLine("Spec:", "Specification: WL-CL-1"); matched {
		t.Error("a longer word starting with the key is not the trailer")
	}
}

func TestFindTakesTheFirstTrailer(t *testing.T) {
	text := "Adds a thing.\n\nSpec: WL-CL-12\nWorklode-Task: WL-99\nSpec: WL-CL-13\n"
	d, err := Find("Spec:", text)
	if err != nil || d.Clause == nil || d.Clause.Number != 12 {
		t.Fatalf("Find = %+v, %v", d, err)
	}
	if _, err := Find("Spec:", "no trailer here\n"); !errors.Is(err, ErrNoTrailer) {
		t.Errorf("Find on text without a trailer = %v, want ErrNoTrailer", err)
	}
	if _, err := Find("Spec:", "Spec: none later\n"); err == nil || errors.Is(err, ErrNoTrailer) {
		t.Errorf("a malformed trailer is an error, not ErrNoTrailer: %v", err)
	}
}

func TestCheck(t *testing.T) {
	cfg := Config{Paths: []string{"internal/cmd/**"}, Trailer: "Spec:"}
	v, err := Check(cfg, Input{Changed: []string{"README.md", "docs/x.md"}})
	if err != nil || len(v.Guarded) != 0 {
		t.Fatalf("no guarded path: %+v %v", v, err)
	}
	_, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"body\n", "a commit\n"}})
	if err == nil || !strings.Contains(err.Error(), "internal/cmd/gate.go") || !strings.Contains(err.Error(), "Spec:") {
		t.Fatalf("guarded path without trailer must name the path and the key: %v", err)
	}
	v, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"body\n", "Feat\n\nSpec: WL-CL-3\n"}})
	if err != nil || v.Declaration == nil || v.Declaration.Clause == nil {
		t.Fatalf("trailer in a commit passes: %+v %v", v, err)
	}
	_, err = Check(cfg, Input{Changed: []string{"internal/cmd/gate.go"}, Texts: []string{"Spec: none fix\n"}})
	if err == nil {
		t.Fatal("a malformed trailer fails the check")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test -trimpath ./internal/gate -run 'TestParseLine|TestFind|TestCheck' -v`
Expected: FAIL to build, `undefined: ParseLine`.

- [ ] **Step 3: Write `internal/gate/trailer.go`**

```go
package gate

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
)

// NoneReasons is 11 §4's closed list of "how" changes a Spec: none trailer
// may cite. "fix" is in the list but is refused without a cited ref.
var NoneReasons = []string{"fix", "refactor", "perf", "copy", "tests", "build", "config"}

// ErrNoTrailer is returned by Find when no line starts with the key.
var ErrNoTrailer = errors.New("no trailer")

// Declaration is one parsed trailer line (11 §4). Exactly one of Clause,
// Section and None is set.
type Declaration struct {
	Clause    *designdoc.ClauseRef  // Spec: WL-CL-456
	Section   *designdoc.SectionRef // Spec: WL-SPEC-4 sec-5 (transitional, S52)
	None      string                // Spec: none <reason>: the reason
	Qualifier string                // "", "amended", or a NoneReasons word on a cited ref
	Line      string                // the line as written, trimmed
}

// String renders the declaration in its canonical trailer form, without the key.
func (d Declaration) String() string {
	var ref string
	switch {
	case d.None != "":
		return "none " + d.None
	case d.Clause != nil:
		ref = fmt.Sprintf("%s-CL-%d", d.Clause.Key, d.Clause.Number)
	case d.Section != nil:
		ref = fmt.Sprintf("%s-%s-%d %s", d.Section.Shorthand.Key, d.Section.Shorthand.Type, d.Section.Shorthand.Number, d.Section.Anchor)
	}
	if d.Qualifier != "" {
		ref += " " + d.Qualifier
	}
	return ref
}

// Find scans text line by line and parses the first line that starts with
// key. A later Spec: line is ignored: one declaration per body (11 §4).
func Find(key, text string) (Declaration, error) {
	for _, line := range strings.Split(text, "\n") {
		d, matched, err := ParseLine(key, line)
		if !matched {
			continue
		}
		return d, err
	}
	return Declaration{}, ErrNoTrailer
}

// ParseLine parses one line. matched reports whether the line starts with
// key followed by a space or the end of the line; err is set when it does
// and the rest is not a valid declaration.
func ParseLine(key, line string) (d Declaration, matched bool, err error) {
	line = strings.TrimSpace(line)
	rest, ok := strings.CutPrefix(line, key)
	if !ok || (rest != "" && rest[0] != ' ' && rest[0] != '\t') {
		return Declaration{}, false, nil
	}
	d.Line = line
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return d, true, fmt.Errorf("%q names nothing: a clause ref, a section ref, or none <reason>", line)
	}
	if fields[0] == "none" {
		if len(fields) != 2 || !slices.Contains(NoneReasons, fields[1]) {
			return d, true, fmt.Errorf("%q: none takes one reason from %s", line, strings.Join(NoneReasons, ", "))
		}
		if fields[1] == "fix" {
			return d, true, fmt.Errorf("%q: a fix cites the section it restores (11 §4)", line)
		}
		d.None = fields[1]
		return d, true, nil
	}
	if c, ok := designdoc.ParseClauseRef(fields[0]); ok {
		d.Clause = &c
		return qualified(d, fields[1:])
	}
	base, anchor, hasAnchor := strings.Cut(fields[0], "#")
	sh, ok := designdoc.ParseShorthand(base)
	if !ok {
		return d, true, fmt.Errorf("%q: %q is not a clause ref, a section ref or none", line, fields[0])
	}
	fields = fields[1:]
	if !hasAnchor {
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "sec-") {
			return d, true, fmt.Errorf("%q: a section ref needs its anchor, %s-%s-%d sec-N", line, sh.Key, sh.Type, sh.Number)
		}
		anchor, fields = fields[0], fields[1:]
	}
	d.Section = &designdoc.SectionRef{Shorthand: sh, Anchor: anchor}
	return qualified(d, fields)
}

// qualified attaches the optional qualifier after a cited ref: "amended", or
// a NoneReasons word such as "fix".
func qualified(d Declaration, fields []string) (Declaration, bool, error) {
	switch {
	case len(fields) == 0:
		return d, true, nil
	case len(fields) == 1 && (fields[0] == "amended" || slices.Contains(NoneReasons, fields[0])):
		d.Qualifier = fields[0]
		return d, true, nil
	}
	return d, true, fmt.Errorf("%q: after the ref only amended or one of %s may follow", d.Line, strings.Join(NoneReasons, ", "))
}

// Input is what the gate decides on: the repo-relative paths a change
// touches, and the texts that may carry the trailer, PR body first, then
// commit messages newest first.
type Input struct {
	Changed []string
	Texts   []string
}

// Verdict is a passing check: which guarded paths changed and, when any
// did, the declaration that covered them.
type Verdict struct {
	Guarded     []string
	Declaration *Declaration
}

// Check applies 11 §3 and §4 offline: no guarded path changed, or a valid
// trailer is present. The error names the guarded paths and the reason.
func Check(cfg Config, in Input) (Verdict, error) {
	g, err := cfg.Guards()
	if err != nil {
		return Verdict{}, err
	}
	var v Verdict
	for _, p := range in.Changed {
		if g.Match(p) {
			v.Guarded = append(v.Guarded, p)
		}
	}
	if len(v.Guarded) == 0 {
		return v, nil
	}
	d, err := Find(cfg.Trailer, strings.Join(in.Texts, "\n"))
	switch {
	case errors.Is(err, ErrNoTrailer):
		return v, fmt.Errorf("guarded paths changed (%s) and no %s trailer names the design clause this change makes true (11 §4)",
			strings.Join(v.Guarded, ", "), cfg.Trailer)
	case err != nil:
		return v, fmt.Errorf("guarded paths changed (%s): %w", strings.Join(v.Guarded, ", "), err)
	}
	v.Declaration = &d
	return v, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -trimpath ./internal/gate -v`
Expected: PASS for every test in the package.

- [ ] **Step 5: Commit**

```bash
git add internal/gate/trailer.go internal/gate/trailer_test.go
git commit -m "Parse the Spec: trailer and decide the gate offline (S30)"
```

---

### Task 3: `lode gate check` and the doctor line

**Files:**
- Create: `internal/cmd/gate.go`
- Create: `internal/cmd/gate_test.go`
- Modify: `internal/cmd/namerule_test.go` (`l1Entities` gains `"gate"`, `l3DomainActions` gains `"check"`)
- Modify: `internal/cmd/doctor.go` (`runDoctorChecks`, one new check)
- Regenerate: `plugins/claude/lode/skills/worklode/references/commands.md` (as `TestCommandReference` instructs)

**Interfaces:**
- Consumes: `gate.Load`, `gate.Check`, `gitexec.Line(dir, args...) (string, bool)`, `gitexec.Text(dir, args...) (string, error)`, doctor's `pass(name, detail)`, `fail(name, detail, fix)`, `skip(name, detail)`.
- Produces: `lode gate check --base <rev> [--head <rev>] [--body-file <path>]`.

- [ ] **Step 1: Write the failing test**

The command needs a git repo. Build one in a temp dir with `gitexec`, the way other `internal/cmd` tests that shell out to git do (search for `gitexec.Run(dir, "init"` in `internal/cmd/*_test.go` and copy that helper's shape; if none exists, write the init lines inline with `user.email`/`user.name` set through `-c`).

```go
package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// gateRepo builds a repo with one base commit and one head commit that
// changes internal/cmd/x.go, and returns the dir with the two revisions.
func gateRepo(t *testing.T, gateTable string, headMessage string) (dir, base, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if err := gitexec.Run(dir, append([]string{"-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, ".worklode"), 0o755)
	os.MkdirAll(filepath.Join(dir, "internal", "cmd"), 0o755)
	os.WriteFile(filepath.Join(dir, ".worklode", "config.toml"), []byte("current_project = \"p\"\n"+gateTable), 0o644)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "base")
	base, _ = gitexec.Line(dir, "rev-parse", "HEAD")
	os.WriteFile(filepath.Join(dir, "internal", "cmd", "x.go"), []byte("package cmd\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", headMessage)
	head, _ = gitexec.Line(dir, "rev-parse", "HEAD")
	return dir, base, head
}

const gateTable = "\n[gate]\npaths = [\"internal/cmd/**\"]\n"

func TestGateCheckRefusesWithoutTrailer(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command")
	_, err := runGateCheck(dir, base, head, "")
	if err == nil {
		t.Fatal("guarded change without a trailer must fail")
	}
}

func TestGateCheckPassesWithTrailerInCommit(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command\n\nSpec: WL-CL-7\n")
	out, err := runGateCheck(dir, base, head, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "WL-CL-7") || !strings.Contains(out, "internal/cmd/x.go") {
		t.Errorf("verdict should name the declaration and the guarded file: %q", out)
	}
}

func TestGateCheckPassesWithTrailerInBodyFile(t *testing.T) {
	dir, base, head := gateRepo(t, gateTable, "change a command")
	body := filepath.Join(dir, "body.md")
	os.WriteFile(body, []byte("Summary\n\nSpec: none refactor\n"), 0o644)
	if _, err := runGateCheck(dir, base, head, body); err != nil {
		t.Fatal(err)
	}
}

func TestGateCheckIsANoOpWithoutTable(t *testing.T) {
	dir, base, head := gateRepo(t, "", "change a command")
	out, err := runGateCheck(dir, base, head, "")
	if err != nil || !strings.Contains(out, "no [gate] table") {
		t.Fatalf("no table: out=%q err=%v", out, err)
	}
}
```

Add `"strings"` to the imports. `runGateCheck(dir, base, head, bodyFile string) (string, error)` is the function Step 3 defines; the cobra command is a thin wrapper over it, and the naming test walks the built tree, so add a case to the `lode gate check` dispatch only if the existing `show_test.go`-style runLode tests make it a one-liner.

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test -trimpath ./internal/cmd -run 'TestGateCheck' -v`
Expected: FAIL to build, `undefined: runGateCheck`.

- [ ] **Step 3: Write `internal/cmd/gate.go`**

```go
package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/gate"
	"github.com/sunstoneinstitute/worklode/internal/gitexec"
)

// newGateCmd is the design authority gate (11-design-authority-gate.md): the
// check CI runs on a pull request that touches a guarded path.
func newGateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gate",
		Short: "The design authority gate",
	}
	cmd.AddCommand(newGateCheckCmd())
	return cmd
}

func newGateCheckCmd() *cobra.Command {
	var base, head, bodyFile string
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Refuse a change to a guarded path that names no design clause",
		Long: `Reads the [gate] table of .worklode/config.toml, diffs --base to --head,
and requires a Spec: trailer on the pull request body (--body-file) or in a
commit message when a guarded path changed. Exit status 1 with the reason
when the trailer is missing or malformed. Without a [gate] table it does
nothing. The trailer is checked for form here; the server resolves the
clause it names (12-spec-refactoring-design-tree.md S50).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir, err := os.Getwd()
			if err != nil {
				return err
			}
			out, err := runGateCheck(dir, base, head, bodyFile)
			if out != "" {
				fmt.Fprint(cmd.OutOrStdout(), out)
			}
			if err != nil {
				cmd.SilenceUsage = true
			}
			return err
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "base revision of the change (the pull request's base sha)")
	cmd.Flags().StringVar(&head, "head", "HEAD", "head revision of the change")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "file holding the pull request body")
	_ = cmd.MarkFlagRequired("base")
	return cmd
}

// runGateCheck is the command without cobra: the repo root is found from
// dir, the config read, the diff and the messages taken from git. The
// returned text is the verdict line for the caller to print; a non-nil error
// is the refusal.
func runGateCheck(dir, base, head, bodyFile string) (string, error) {
	root, ok := gitexec.Line(dir, "rev-parse", "--show-toplevel")
	if !ok {
		return "", errors.New("gate: not inside a git repository")
	}
	cfg, enabled, err := gate.Load(root)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if !enabled {
		return "gate: no [gate] table in .worklode/config.toml, nothing to check\n", nil
	}
	if head == "" {
		head = "HEAD"
	}
	diff, err := gitexec.Text(root, "diff", "--name-only", base+"..."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	var in gate.Input
	for _, p := range strings.Split(diff, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			in.Changed = append(in.Changed, p)
		}
	}
	if bodyFile != "" {
		body, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("gate: %w", err)
		}
		in.Texts = append(in.Texts, string(body))
	}
	// One NUL after each message keeps a multi-paragraph body whole; newest
	// first so the final commit's trailer wins when several carry one.
	log, err := gitexec.Text(root, "log", "--format=%B%x00", base+".."+head)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	for _, m := range strings.Split(log, "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			in.Texts = append(in.Texts, m)
		}
	}
	v, err := gate.Check(cfg, in)
	if err != nil {
		return "", fmt.Errorf("gate: %w", err)
	}
	if len(v.Guarded) == 0 {
		return "gate: no guarded path changed\n", nil
	}
	return fmt.Sprintf("gate: %s covers %s\n", cfg.Trailer+" "+v.Declaration.String(), strings.Join(v.Guarded, ", ")), nil
}

func init() {
	rootCmd.AddCommand(newGateCmd())
}
```

If `rootCmd.AddCommand` is done elsewhere than an `init` in the command's own file (read how `doctor.go:368` does it), follow that pattern instead of the `init` above.

- [ ] **Step 4: The naming rule and the command reference**

In `internal/cmd/namerule_test.go`: add `"gate": true,` to `l1Entities` and `"check": true,` to `l3DomainActions`. Run `go test -trimpath ./internal/cmd -run 'TestNam|TestCommandReference' -v`; follow `TestCommandReference`'s failure message to regenerate `plugins/claude/lode/skills/worklode/references/commands.md`. Read `docs/agent-surfaces.md` §"When the CLI changes" and do what it asks for a new command (a register line if the section requires one).

- [ ] **Step 5: The doctor line**

In `internal/cmd/doctor.go`, `runDoctorChecks`, after the `current_project` check, add a `gate` check. It reads the repo root the way the existing checks find `.worklode/config.toml` (they resolve `dir`; use the same root):

```go
	// 5. The design authority gate's table parses (11 §3). Whether the
	// repository also requires pull requests is not checked here yet: the
	// store knows branch rules but no API route exposes them.
	switch cfg, enabled, err := gate.Load(dir); {
	case err != nil:
		checks = append(checks, fail("gate", err.Error(), "fix the [gate] table in .worklode/config.toml"))
	case !enabled:
		checks = append(checks, skip("gate", "not enabled (no [gate] table)"))
	default:
		checks = append(checks, pass("gate", fmt.Sprintf("%d path patterns, %d regex, trailer %q; PR requirement not checked", len(cfg.Paths), len(cfg.Regex), cfg.Trailer)))
	}
```

If `runDoctorChecks` resolves the repo root under a different name than `dir`, use that. Add one test beside the existing doctor tests that runs the checks in a temp repo with a `[gate]` table and asserts a `gate` check with `OK: true`, copying the setup the nearest doctor test uses.

- [ ] **Step 6: Run the package tests**

Run: `go test -trimpath ./internal/cmd -run 'TestGate|TestDoctor|TestNam|TestCommandReference|TestRenderRule|TestFileRule' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/gate.go internal/cmd/gate_test.go internal/cmd/namerule_test.go internal/cmd/doctor.go internal/cmd/doctor_test.go plugins/claude/lode/skills/worklode/references/commands.md docs/agent-surfaces.md
git commit -m "Add lode gate check and a doctor line for the gate table (S29, S30)"
```

Add `docs/agent-surfaces.md` only if Step 4 changed it.

---

### Task 4: migration, `source = 'gate'`

**Files:**
- Create: `deploy/base/migrations/0085_gate_source.up.sql`
- Create: `deploy/base/migrations/0085_gate_source.down.sql`
- Modify: `deploy/base/kustomization.yaml` (list both files after the last migration pair)

Read `deploy/base/AGENTS.md` first. Run `./scripts/check-migrations.sh --no-fix` after creating the files; if it renumbers because increment 3's `0084` is not on this branch yet, keep `0085` anyway (the number is claimed in the coordination file) unless the script refuses a gap; then take what it assigns and record the number in the coordination file.

- [ ] **Step 1: The up migration**

```sql
-- The design authority gate is the third writer of a task's governing links
-- (11-design-authority-gate.md §4, 12-spec-refactoring-design-tree.md S4,
-- S51): a Spec: trailer on a planless task's pull request or push.
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_source_check;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_source_check
    CHECK (source IN ('plan', 'manual', 'gate'));
```

- [ ] **Step 2: The down migration**

```sql
DELETE FROM task_governed_by WHERE source = 'gate';
ALTER TABLE task_governed_by DROP CONSTRAINT task_governed_by_source_check;
ALTER TABLE task_governed_by ADD CONSTRAINT task_governed_by_source_check
    CHECK (source IN ('plan', 'manual'));
```

Postgres names the inline column CHECK from `0082_clauses.up.sql` `task_governed_by_source_check`. Confirm with a store test run (the store tests apply every migration) rather than by assumption: `go test -trimpath ./internal/store -run TestGovernAndUngovern -v` must pass after Step 3.

- [ ] **Step 3: List the files and check**

Add the two files to `deploy/base/kustomization.yaml` after the `0083_clause_graph` pair. Run `./scripts/check-migrations.sh --no-fix` and `go test -trimpath ./internal/store -run TestGovernAndUngovern -v`.

- [ ] **Step 4: Commit**

```bash
git add deploy/base/migrations/0085_gate_source.up.sql deploy/base/migrations/0085_gate_source.down.sql deploy/base/kustomization.yaml
git commit -m "Allow source = gate on a task's governing link"
```

---

### Task 5: store helpers for the reconciler

**Files:**
- Modify: `internal/store/governedby.go`
- Modify: `internal/store/governedby_test.go`

**Interfaces:**
- Consumes: `resolveDocRef(tx, projectID, base string) (int64, bool, error)` (`docedges.go:793`), `ensureClauses(tx, docID)`, `arrangedClauses(tx, docID) ([]clauseRow, error)`, `ErrNotFound`, `designdoc.SectionRef`.
- Produces: `HasPlanGovernance(tx *sql.Tx, taskID string) (bool, error)`, `ClauseAtSection(tx *sql.Tx, ref designdoc.SectionRef) (int64, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/governedby_test.go`, reusing its helpers (`openDocStore`, `mustCreateDoc`, `clauseDocV1`, `createTask`, `acceptDoc`, the plan-accept flow used by `TestAcceptPlanGovernsMintedTasks`):

```go
func TestClauseAtSection(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	sh, _ := designdoc.ParseShorthand(fmt.Sprintf("P1-SPEC-%d", d.Number))
	var got int64
	err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		var err error
		got, err = ClauseAtSection(tx, designdoc.SectionRef{Shorthand: sh, Anchor: "sec-1.1"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	arr := arrangementOf(t, s, d.ID)
	if arr[1].Anchor != "sec-1.1" {
		t.Fatalf("fixture drift: %+v", arr)
	}
	var want int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT id FROM clauses WHERE number = $1`, arr[1].Number).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("ClauseAtSection = %d, want clause %d at sec-1.1", got, want)
	}
	err = s.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := ClauseAtSection(tx, designdoc.SectionRef{Shorthand: sh, Anchor: "sec-9"})
		return err
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown anchor = %v, want ErrNotFound", err)
	}
}

func TestHasPlanGovernance(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	now := s.Now()
	task := createTask(t, s, now, CreateTaskInput{Project: "p1", Title: "planless", Kind: "feature", Priority: "medium", CreatedBy: "stig"})
	var clauseID int64
	if err := s.db.QueryRowContext(context.Background(), `SELECT clause_id FROM doc_clauses WHERE doc_id = $1 AND position = 0`, d.ID).Scan(&clauseID); err != nil {
		t.Fatal(err)
	}
	check := func(want bool, after string) {
		t.Helper()
		var got bool
		if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
			var err error
			got, err = HasPlanGovernance(tx, task.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("HasPlanGovernance after %s = %v, want %v", after, got, want)
		}
	}
	check(false, "creation")
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error { return Govern(tx, task.ID, clauseID, "gate") }); err != nil {
		t.Fatal(err)
	}
	check(false, "a gate link")
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE task_governed_by SET source = 'plan' WHERE task_id = $1`, task.ID); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check(true, "a plan link")
}
```

`createTask`'s input type and field names come from `internal/store/tasks_test.go`; copy the call shape the file already uses (the `CreateTaskInput` here is what the existing helper takes; adjust the literal to match). If `createTask` needs `openTaskStore` rather than `openDocStore`, check what `TestAcceptPlanGovernsMintedTasks` does and use the same store opener.

- [ ] **Step 2: Run them to see them fail**

Run: `go test -trimpath ./internal/store -run 'TestClauseAtSection|TestHasPlanGovernance' -v`
Expected: FAIL to build, `undefined: ClauseAtSection`, `undefined: HasPlanGovernance`.

- [ ] **Step 3: Write the helpers**

Append to `internal/store/governedby.go`:

```go
// HasPlanGovernance reports whether a plan governs the task: any link with
// source = 'plan'. The gate writes only when this is false (S51).
func HasPlanGovernance(tx *sql.Tx, taskID string) (bool, error) {
	var one int
	err := tx.QueryRow(
		`SELECT 1 FROM task_governed_by WHERE task_id = $1 AND source = 'plan' LIMIT 1`, taskID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("plan governance of task %s: %w", taskID, err)
	}
	return true, nil
}

// ClauseAtSection resolves a section ref (WL-SPEC-4 plus sec-5) to the
// clause arranged at that anchor, splitting the document first when it
// predates the clause tables. ErrNotFound when the project, the document or
// the anchor is unknown.
func ClauseAtSection(tx *sql.Tx, ref designdoc.SectionRef) (int64, error) {
	var projectID string
	err := tx.QueryRow(`SELECT id FROM projects WHERE key = $1`, ref.Shorthand.Key).Scan(&projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("project %s: %w", ref.Shorthand.Key, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("project %s: %w", ref.Shorthand.Key, err)
	}
	base := fmt.Sprintf("%s-%s-%d", ref.Shorthand.Key, ref.Shorthand.Type, ref.Shorthand.Number)
	docID, ok, err := resolveDocRef(tx, projectID, base)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("document %s: %w", base, ErrNotFound)
	}
	if err := ensureClauses(tx, docID); err != nil {
		return 0, err
	}
	entries, err := arrangedClauses(tx, docID)
	if err != nil {
		return 0, err
	}
	for _, c := range entries {
		if c.anchor == ref.Anchor {
			return c.id, nil
		}
	}
	return 0, fmt.Errorf("%s has no clause at %s: %w", base, ref.Anchor, ErrNotFound)
}
```

Check `resolveDocRef`'s exact contract at `docedges.go:793` (it takes the project id and a ref base; confirm the shorthand form `P1-SPEC-1` resolves there, as `TestAcceptPlanGovernsMintedTasks` already relies on). Add the `designdoc` import.

- [ ] **Step 4: Run the tests**

Run: `go test -trimpath ./internal/store -run 'TestClauseAtSection|TestHasPlanGovernance|TestGovern|TestAcceptPlan' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/governedby.go internal/store/governedby_test.go
git commit -m "Resolve a section ref to its clause and read plan governance for the gate"
```

---

### Task 6: the `spec-reconciler` subscriber

**Files:**
- Create: `internal/api/gate.go`
- Create: `internal/api/gate_test.go`
- Modify: `internal/api/server.go` (subscriber registration next to `docLifecycleSubscriber`, around lines 1313 and 1345)
- Modify: `internal/api/metrics.go` (one CounterVec) or the metrics struct the server holds; read how `watcherMetrics` is held on `server` (line 360) and follow it

**Interfaces:**
- Consumes: `eventbus.Options`, `eventbus.Run`, `eventbus.Outcome{Applied,Suppressed,Error}`, `store.Event{Source, Type, Payload, ID}`, `store.TaskIDFromRef`, `store.TaskIDFromBody`, `(*Store).RecordEvent(ctx, source, externalID, typ, payload, apply)`, `store.Govern`, `store.HasPlanGovernance`, `store.ClauseIDByRef`, `store.ClauseAtSection`, `gate.Find`, `gate.DefaultTrailer`, `store.ErrNotFound`.
- Produces: subscriber name `spec-reconciler`; metric `worklode_spec_reconciler_total{outcome}` with outcomes `linked`, `no_task`, `no_trailer`, `malformed`, `none`, `planned`, `unknown_target`, `already`.

- [ ] **Step 1: Read the pattern**

Read `internal/api/docwatch.go` (the handler shape and how it returns outcomes), `internal/api/docwatch_test.go` (how a handler is exercised with a hand-built `store.Event` and a test server; reuse its constructor), and `internal/api/governedby.go` (the `task.governed` event payload the POST handler records; the reconciler mirrors it and adds `"source": "gate"`).

- [ ] **Step 2: Write the failing tests**

```go
package api

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// prEvent builds a github pull_request.opened event for a branch and body.
func prEvent(t *testing.T, id int64, headRef, body string) store.Event {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"action":       "opened",
		"pull_request": map[string]any{"body": body, "head": map[string]any{"ref": headRef}},
	})
	return store.Event{ID: id, Source: "github", Type: "pull_request.opened", Payload: payload}
}

func TestSpecReconcilerGovernsAPlanlessTask(t *testing.T) {
	st, h, token := newTestServer(t)
	seedProjectWithKey(t, st, "WL")
	// A spec with anchored sections, so WL-CL-1 and sec-1.1 exist.
	createDocViaAPI(t, h, token, "spec", "s", clauseDocV1)
	task := createTaskViaAPI(t, h, token, `{"project":"WL","title":"planless","kind":"feature","priority":"medium"}`)
	srv := serverOf(h) // however docwatch_test.go reaches the *server behind h

	out, err := srv.handleSpecReconcile(context.Background(), prEvent(t, 1001, task.Branch, "Adds it.\n\nSpec: WL-CL-1\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("first delivery: %v %v", out, err)
	}
	gov, err := st.GovernedBy(context.Background(), task.ID)
	if err != nil || len(gov) != 1 || gov[0].Clause != "WL-CL-1" || gov[0].Source != "gate" {
		t.Fatalf("governed_by = %+v, %v", gov, err)
	}

	// Redelivery of the same event is absorbed.
	out, err = srv.handleSpecReconcile(context.Background(), prEvent(t, 1001, task.Branch, "Spec: WL-CL-1\n"))
	if err != nil || out != eventbus.OutcomeSuppressed {
		t.Fatalf("redelivery: %v %v", out, err)
	}

	// A section ref resolves to the clause at that anchor.
	out, err = srv.handleSpecReconcile(context.Background(), prEvent(t, 1002, task.Branch, "Spec: WL-SPEC-1 sec-1.1\n"))
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("section ref: %v %v", out, err)
	}
	gov, _ = st.GovernedBy(context.Background(), task.ID)
	if len(gov) != 2 {
		t.Fatalf("after section ref: %+v", gov)
	}
}

func TestSpecReconcilerLeavesPlannedAndNoneAlone(t *testing.T) {
	st, h, token := newTestServer(t)
	seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, "spec", "s", clauseDocV1)
	task := createTaskViaAPI(t, h, token, `{"project":"WL","title":"t","kind":"feature","priority":"medium"}`)
	srv := serverOf(h)

	out, _ := srv.handleSpecReconcile(context.Background(), prEvent(t, 2001, task.Branch, "Spec: none refactor\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("none: %v", out)
	}
	out, _ = srv.handleSpecReconcile(context.Background(), prEvent(t, 2002, task.Branch, "no trailer\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("no trailer: %v", out)
	}
	out, _ = srv.handleSpecReconcile(context.Background(), prEvent(t, 2003, "feature/no-task", "Spec: WL-CL-1\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("no task: %v", out)
	}
	out, _ = srv.handleSpecReconcile(context.Background(), prEvent(t, 2004, task.Branch, "Spec: WL-CL-999\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("unknown clause: %v", out)
	}
	if gov, _ := st.GovernedBy(context.Background(), task.ID); len(gov) != 0 {
		t.Fatalf("nothing should be linked yet: %+v", gov)
	}

	// A plan link blocks the gate.
	if err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		id, err := store.ClauseIDByRef(tx, "WL", 1)
		if err != nil {
			return err
		}
		return store.Govern(tx, task.ID, id, "plan")
	}); err != nil {
		t.Fatal(err)
	}
	out, _ = srv.handleSpecReconcile(context.Background(), prEvent(t, 2005, task.Branch, "Spec: WL-CL-2\n"))
	if out != eventbus.OutcomeSuppressed {
		t.Errorf("planned task: %v", out)
	}
	if gov, _ := st.GovernedBy(context.Background(), task.ID); len(gov) != 1 {
		t.Fatalf("plan link only: %+v", gov)
	}
}

func TestSpecReconcilerReadsPushCommits(t *testing.T) {
	st, h, token := newTestServer(t)
	seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, "spec", "s", clauseDocV1)
	task := createTaskViaAPI(t, h, token, `{"project":"WL","title":"t","kind":"feature","priority":"medium"}`)
	srv := serverOf(h)
	payload, _ := json.Marshal(map[string]any{
		"ref": "refs/heads/" + task.Branch,
		"commits": []map[string]any{
			{"id": "a", "message": "first\n\nSpec: WL-CL-1\n"},
			{"id": "b", "message": "second\n\nSpec: WL-CL-2\n"},
		},
	})
	out, err := srv.handleSpecReconcile(context.Background(), store.Event{ID: 3001, Source: "github", Type: "push", Payload: payload})
	if err != nil || out != eventbus.OutcomeApplied {
		t.Fatalf("push: %v %v", out, err)
	}
	gov, _ := st.GovernedBy(context.Background(), task.ID)
	if len(gov) != 1 || gov[0].Clause != "WL-CL-2" {
		t.Fatalf("the final commit's trailer wins: %+v", gov)
	}
}
```

Adapt the helper names to what `internal/api` tests already have: `createDocViaAPI` may be spelled differently (search `governedby_test.go` and `clauses_test.go` in `internal/api` for how they create a spec with anchored sections; `clauseDocV1` may live in the store package only, in which case copy its body into a local const). `serverOf(h)` stands for whatever `docwatch_test.go` uses to reach the `*server`. `task.Branch` is on `model.Task`; if `createTaskViaAPI` returns a different shape, decode `branch` from the create response. Add `"database/sql"` to the imports.

- [ ] **Step 3: Run them to see them fail**

Run: `go test -trimpath ./internal/api -run 'TestSpecReconciler' -v`
Expected: FAIL to build, `handleSpecReconcile undefined`.

- [ ] **Step 4: Write `internal/api/gate.go`**

```go
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/eventbus"
	"github.com/sunstoneinstitute/worklode/internal/gate"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// specReconcilerSubscriber is the eventbus subscriber that turns a Spec:
// trailer into a governing link (12-spec-refactoring-design-tree.md S4, S18;
// 11-design-authority-gate.md §4).
const specReconcilerSubscriber = "spec-reconciler"

// reconcilerOutcomes is the bounded label set of worklode_spec_reconciler_total.
var reconcilerOutcomes = []string{"linked", "no_task", "no_trailer", "malformed", "none", "planned", "unknown_target", "already"}

// reconcilerMetrics counts what the subscriber did with each event. Nil-safe.
type reconcilerMetrics struct {
	outcomes *prometheus.CounterVec
}

func newReconcilerMetrics(reg prometheus.Registerer) *reconcilerMetrics {
	m := &reconcilerMetrics{outcomes: prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "worklode_spec_reconciler_total",
		Help: "Spec: trailers seen by the spec-reconciler subscriber, by outcome.",
	}, []string{"outcome"})}
	for _, o := range reconcilerOutcomes {
		m.outcomes.WithLabelValues(o)
	}
	reg.MustRegister(m.outcomes)
	return m
}

func (m *reconcilerMetrics) Outcome(o string) {
	if m != nil {
		m.outcomes.WithLabelValues(o).Inc()
	}
}

// errReconcileSkip aborts the RecordEvent transaction without recording
// anything: the event was understood and deliberately not acted on.
var errReconcileSkip = errors.New("spec-reconciler: skip")

// handleSpecReconcile reads the GitHub pull_request.* and push events the
// hooks record, finds the task the branch names, and governs it by the
// clause the Spec: trailer cites when no plan governs it (S51). Section refs
// are accepted until the per-project switch lands (S52). Redelivery is
// absorbed by the task.governed event's external id.
func (s *server) handleSpecReconcile(ctx context.Context, ev store.Event) (eventbus.Outcome, error) {
	if ev.Source != "github" {
		return eventbus.OutcomeSuppressed, nil
	}
	var taskID string
	var texts []string
	switch {
	case strings.HasPrefix(ev.Type, "pull_request."):
		var p struct {
			PullRequest struct {
				Body string `json:"body"`
				Head struct {
					Ref string `json:"ref"`
				} `json:"head"`
			} `json:"pull_request"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return eventbus.OutcomeApplied, fmt.Errorf("spec-reconciler: event %d payload: %w", ev.ID, err)
		}
		taskID = store.TaskIDFromRef(p.PullRequest.Head.Ref)
		if taskID == "" {
			taskID = store.TaskIDFromBody(p.PullRequest.Body)
		}
		texts = []string{p.PullRequest.Body}
	case ev.Type == "push":
		var p struct {
			Ref     string `json:"ref"`
			Commits []struct {
				Message string `json:"message"`
			} `json:"commits"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return eventbus.OutcomeApplied, fmt.Errorf("spec-reconciler: event %d payload: %w", ev.ID, err)
		}
		taskID = store.TaskIDFromRef(strings.TrimPrefix(p.Ref, "refs/heads/"))
		// Newest first: the final commit's trailer wins (11 §4).
		for i := len(p.Commits) - 1; i >= 0; i-- {
			texts = append(texts, p.Commits[i].Message)
		}
	default:
		return eventbus.OutcomeSuppressed, nil
	}
	if taskID == "" {
		s.reconcilerMetrics.Outcome("no_task")
		return eventbus.OutcomeSuppressed, nil
	}
	decl, err := gate.Find(gate.DefaultTrailer, strings.Join(texts, "\n"))
	switch {
	case errors.Is(err, gate.ErrNoTrailer):
		s.reconcilerMetrics.Outcome("no_trailer")
		return eventbus.OutcomeSuppressed, nil
	case err != nil:
		s.log.Warn("spec-reconciler: malformed trailer", "event", ev.ID, "task", taskID, "err", err)
		s.reconcilerMetrics.Outcome("malformed")
		return eventbus.OutcomeSuppressed, nil
	case decl.None != "":
		s.reconcilerMetrics.Outcome("none")
		return eventbus.OutcomeSuppressed, nil
	}

	outcome := "linked"
	payload, _ := json.Marshal(map[string]any{"task": taskID, "clause": decl.String(), "source": "gate", "event": ev.ID})
	_, inserted, err := s.store.RecordEvent(ctx, "gate", fmt.Sprintf("spec-reconciler-%d", ev.ID), "task.governed", payload,
		func(tx *sql.Tx, _ int64) error {
			planned, err := store.HasPlanGovernance(tx, taskID)
			if err != nil {
				return err
			}
			if planned {
				outcome = "planned"
				return errReconcileSkip
			}
			var clauseID int64
			if decl.Clause != nil {
				clauseID, err = store.ClauseIDByRef(tx, decl.Clause.Key, decl.Clause.Number)
			} else {
				clauseID, err = store.ClauseAtSection(tx, *decl.Section)
			}
			if err != nil {
				return err
			}
			return store.Govern(tx, taskID, clauseID, "gate")
		})
	switch {
	case errors.Is(err, errReconcileSkip):
		s.reconcilerMetrics.Outcome(outcome)
		return eventbus.OutcomeSuppressed, nil
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("spec-reconciler: trailer names nothing", "event", ev.ID, "task", taskID, "spec", decl.String(), "err", err)
		s.reconcilerMetrics.Outcome("unknown_target")
		return eventbus.OutcomeSuppressed, nil
	case err != nil:
		return eventbus.OutcomeError, fmt.Errorf("spec-reconciler: event %d: %w", ev.ID, err)
	case !inserted:
		s.reconcilerMetrics.Outcome("already")
		return eventbus.OutcomeSuppressed, nil
	}
	s.reconcilerMetrics.Outcome("linked")
	return eventbus.OutcomeApplied, nil
}
```

Check two things against the code before keeping this shape: whether `RecordEvent`'s apply error surfaces wrapped so `errors.Is` finds `errReconcileSkip` and `store.ErrNotFound` (read `events.go:59-135`); and what `eventbus.Outcome` the loop expects for a handler error (docwatch returns `OutcomeApplied` with an error for a payload it cannot parse so the batch is not retried forever; keep that convention for parse errors and use `OutcomeError` only for a store failure worth retrying). If `s.store` is named differently on `server`, use the field the other handlers use.

- [ ] **Step 5: Wire the subscriber and the metric**

In `internal/api/server.go`:
- Add `reconcilerMetrics *reconcilerMetrics` to the `server` struct beside `watcherMetrics`.
- Where `EnsureEventSubscriber(docLifecycleSubscriber)` runs (around line 1313), also `EnsureEventSubscriber(specReconcilerSubscriber)`.
- Where the `doc-lifecycle` loop starts (around line 1345), set `s.reconcilerMetrics = newReconcilerMetrics(reg)` and start a second `eventbus.Run` goroutine with `Name: specReconcilerSubscriber`, `Handler: s.handleSpecReconcile`, the same `busMetrics` (its labels carry the subscriber name), the same `Poll` and `Log`, and the same error logging with the subscriber's name. Rewrite the comment that says this is "the process's one subscriber loop" to say the two loops share one `eventbus.Metrics`.

- [ ] **Step 6: Run the tests**

Run: `go test -trimpath ./internal/api -run 'TestSpecReconciler|TestNewServer|TestMetrics|TestDocLifecycle' -v`
Expected: PASS. Then `go test -trimpath ./internal/api` for the package.

- [ ] **Step 7: Commit**

```bash
git add internal/api/gate.go internal/api/gate_test.go internal/api/server.go
git commit -m "Govern a planless task from its Spec: trailer with the spec-reconciler subscriber (S4, S18)"
```

---

### Task 7: the CI job

**Files:**
- Modify: `.github/workflows/pr-checks.yml`

Read the `worklode-ci` skill (`.claude/skills/worklode-ci/SKILL.md`) and the whole of `pr-checks.yml` first. The new job must not run on `merge_group` (no pull request body to read), must respect the gate job's `run` output so docs-only PRs still skip, and must be added to the `checks` aggregate job's `needs` in the way the other jobs are, with the same skipped-is-fine handling.

- [ ] **Step 1: Add the job**

After the `validate-kustomize` job:

```yaml
  spec-gate:
    needs: gate
    if: github.event_name == 'pull_request' && needs.gate.outputs.run == 'true' && needs.gate.outputs.code == 'true'
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Build lode
        run: go build -trimpath -o bin/lode ./cmd/lode
      - name: Check the Spec: trailer on guarded paths
        env:
          GH_TOKEN: ${{ github.token }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
          BASE: ${{ github.event.pull_request.base.sha }}
          HEAD: ${{ github.event.pull_request.head.sha }}
        run: |
          set -euo pipefail
          gh pr view "$PR_NUMBER" --json body --jq .body > "$RUNNER_TEMP/pr-body.md"
          bin/lode gate check --base "$BASE" --head "$HEAD" --body-file "$RUNNER_TEMP/pr-body.md"
```

Use the same `actions/checkout` and `actions/setup-go` versions the other workflow files in `.github/workflows/` pin; copy them exactly.

- [ ] **Step 2: Add it to the aggregate**

In the `checks` job, add `spec-gate` to `needs` and to whatever expression treats a skipped job as passing, exactly as `validate-kustomize` is handled.

- [ ] **Step 3: Validate the YAML**

Run: `python3 -c 'import yaml,sys; yaml.safe_load(open(".github/workflows/pr-checks.yml"))'` (or `yq` if installed). Expected: no output. Note in the report that the job's first real run happens when a PR against `main` carries this file; it exits 0 on this repository until a `[gate]` table is added (S54).

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/pr-checks.yml
git commit -m "Run lode gate check on pull requests"
```

---

### Task 8: specs 03, 09, 11 and 12 record what shipped

**Files:**
- Modify: `docs/specs2/11-design-authority-gate.md` §3 and §4
- Modify: `docs/specs2/03-tasks-and-execution.md` §4 (governing clauses paragraph), §9.6 (a sibling subsection), §9.7 (one metric row)
- Modify: `docs/specs2/09-cli-and-skills.md` (L1 list, L3 allowlist, the command table)
- Modify: `docs/specs2/12-spec-refactoring-design-tree.md` (S50 to S54 in the recorded list)

Plain language throughout: no em dash, no "X, not Y", "rather than", "instead of". Locate insertions by heading and content; line numbers drift.

- [ ] **Step 1: Spec 11**

§3, after the paragraph ending "Without a `[gate]` table nothing runs.", add:

```markdown
`lode gate check --base <sha> --head <sha> [--body-file <path>]` is the check itself: it reads the table, diffs the two revisions, and exits 1 with the reason when a guarded path changed and no valid trailer is present on the pull request body or in a commit message. It checks the trailer's form offline and never calls the server; the server resolves the clause the trailer names (§4). `lode doctor` reports whether the table parses. Whether the repository requires pull requests is not checked yet, since no API route exposes the branch rules the server records.
```

§4, after the code block listing the three `Spec:` forms, add:

```markdown
The server's `spec-reconciler` subscriber reads the same trailer from the pull request body or the pushed commits (the final commit wins), finds the task the branch names, and writes a `governedBy` link with `source = 'gate'` when no plan governs the task. A `none` trailer writes nothing. A trailer naming a clause or section that does not exist is counted and logged and writes nothing. Section refs are accepted everywhere until the per-project switch `gate_trailer_sections` arrives with the plan lifecycle increment (12-spec-refactoring-design-tree.md S50 to S52).
```

- [ ] **Step 2: Spec 03**

In §4's "Governing clauses" paragraph, after the sentence naming `task.governed` and `task.ungoverned`, add:

```markdown
A link's `source` is `plan`, `manual` or `gate`: the plan that minted the task, an architect's hand, or the design authority gate reading a `Spec:` trailer on a planless task's pull request (11-design-authority-gate.md §4). The gate writes only when no plan link exists and never removes a link.
```

After §9.6, add a sibling subsection (numbered so §9.7 Metrics keeps its number: use `### 9.6a The spec-reconciler subscriber` and, if the file's headings carry `{#sec-...}` anchors, `{#sec-9.6a}`):

```markdown
### 9.6a The `spec-reconciler` subscriber

One rule. On a GitHub `pull_request.*` or `push` event for a task branch, read the `Spec:` trailer from the pull request body or the pushed commits, newest first. When it names a clause (`WL-CL-<n>`) or a section (`WL-SPEC-4 sec-5`, resolved to the clause arranged at that anchor) and the task carries no `source = 'plan'` link, write a `governedBy` link with `source = 'gate'`. `none`, a missing or malformed trailer, an unknown task, an unknown target and an already governed task are counted and change nothing. Redelivery is absorbed by the `task.governed` event's external id, `spec-reconciler-<event id>`. The subscriber runs behind `SubscriberLock` beside `doc-lifecycle` and starts with it.
```

In §9.7's table add a row:

```markdown
| `worklode_spec_reconciler_total` | counter | `outcome` in `linked|no_task|no_trailer|malformed|none|planned|unknown_target|already` |
```

- [ ] **Step 3: Spec 09**

L1 row: add `gate` to the closed list of entities, with a clause noting it names the design authority gate (11 §3). L3 row: add `check` to the allowlist after the last verb. Command table: add a row `| \`gate\` | L1 | \`check\` | |` in the same column shape as the `doctor` row, placed after `doc`.

- [ ] **Step 4: Spec 12**

In the recorded-decisions list (heading "Recorded from increment 1" on this branch; increment 3 renames it, do not rename here), append after the last S-item:

```markdown
- S50 (S30 applied): `lode gate check` validates the trailer's form and the closed `none` list offline against the repo config and the diff. Resolving the cited clause is the reconciler's job on the server. A well-formed trailer naming a clause that does not exist passes CI and is counted as `unknown_target` on the server.
- S51 (S4 applied): the gate is the third writer of governing links, `source = 'gate'`. It writes only when the task carries no `source = 'plan'` link, it is idempotent, and nothing removes a gate link automatically.
- S52 (S30 transition): section refs `WL-SPEC-4 sec-5` are accepted everywhere until the per-project settings of the plan lifecycle increment land; the switch is then the key `gate_trailer_sections`.
- S53 (S29 applied): the `[gate]` table is decoded by the `internal/gate` package with the TOML library the module already carries. The key=value parser in `internal/cli` skips TOML tables so every other `lode` command works in a gated repository.
- S54 (S29 applied): CI runs `lode gate check` on `pull_request` events only, and the job exits 0 when the repository has no `[gate]` table. Worklode's own configuration enables the gate separately, once the trailer habit exists.
```

- [ ] **Step 5: Style check and commit**

Run: `git diff -U0 docs/specs2 | grep '^+' | grep -nE '—|, not |rather than|instead of'`
Expected: no output.

```bash
git add docs/specs2/03-tasks-and-execution.md docs/specs2/09-cli-and-skills.md docs/specs2/11-design-authority-gate.md docs/specs2/12-spec-refactoring-design-tree.md
git commit -m "Record the gate trailer, lode gate check and the spec-reconciler in specs 03, 09, 11 and 12"
```

---

## Self-review

Spec coverage: S29 is Task 1 (config) and Task 3 (doctor line, without the PR-requirement warning, deferred in the preface). S30 is Task 2 (grammar, both clause and section forms, the closed `none` list, `none fix` refused) and Task 3 (the CI command). S4 and S18 are Task 6 (the subscriber behind `SubscriberLock` through `eventbus.Run`, writing `governedBy` for planless tasks) with Task 4 (the `gate` source) and Task 5 (resolution). 11 §3 and §4 are made true by Tasks 1 to 3 and 6 and described by Task 8. Task 7 wires CI.

Type consistency: `gate.Config`, `gate.Load`, `gate.Check(cfg, Input) (Verdict, error)` in Tasks 1 to 3; `gate.Find(key, text) (Declaration, error)` and `Declaration{Clause *designdoc.ClauseRef; Section *designdoc.SectionRef; None; Qualifier}` in Tasks 2 and 6; `store.HasPlanGovernance(tx, taskID)` and `store.ClauseAtSection(tx, designdoc.SectionRef)` in Tasks 5 and 6; `store.Govern(tx, taskID, clauseID, "gate")` matches the increment 1 signature; the event external id `spec-reconciler-<id>` is the same in Task 6's code and Task 8's prose; the metric name and outcome list are the same in Task 6 and Task 8.

Placeholders: none. Every step carries its code or its exact edit.
