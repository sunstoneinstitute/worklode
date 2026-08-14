---
status: accepted
covers:
  - docs/specs/008-worklode-plugin.md#sec-17.3
requires:
  - 2026-08-03-multi-harness-1-adapter-core.md
---
# Multi-harness 2/3: skill delivery — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Series:** Part 2 of 3 — see `2026-08-03-multi-harness-1-adapter-core.md`
for the series map. This part holds 4 tasks (task numbers restart at 1 per
plan file; the cross-part dependency is the `requires:` frontmatter edge on
part 1, which must be merged first — the `internal/harness` registry and
the reshaped `lode install`). Part 3 is independent of this part.

**Goal:** One store, many doorways: move the content-addressed store out of
the linked skills directory, publish `~/.worklode/skills` into
`~/.agents/skills` (four harnesses) and `~/.claude/skills/<name>` (Claude
Code), and give briefs project-scope links so a sandbox that never ran
`lode install` still reads its task's skills (spec 024 acceptance 2).

**Architecture:** `internal/skillstore` splits its one root into a links dir
(`~/.worklode/skills`, name symlinks only) and a store dir
(`~/.worklode/store/<hash>`, canonical and immutable), self-migrating the
legacy `.store/` layout on first touch — necessary because every harness in
spec 024 Table 1 walks the linked directory and would list hash dirs as
duplicate skills. A new `publish.go` links the store into harness skill
targets, per-directory or per-skill, with a copy fallback where symlinks
fail. `lode install --skills` publishes for every registered adapter's
targets; `lode skills install --link` does the same for one skill; and
`internal/hookrun`'s existing lazy fetch additionally links
`<worktree>/.agents/skills/<name>` for each brief-listed skill, excluded
from git via `info/exclude`. The 016 registry, archives, hashes and brief
integration are untouched.

**Tech Stack:** Go 1.26, stdlib only. No server-side changes in this part —
no new endpoints, loops, or store operations, so no new `worklode_*`
metrics are due.

**Spec:** `docs/specs/008-worklode-plugin.md` §3.3, §4.

---

## What exists vs. what this builds

- Local store: `internal/skillstore/skillstore.go` — `Root()`
  (`$LODE_SKILLS_DIR` or `~/.worklode/skills`, `skillstore.go:38`),
  `Ensure(root, name, hash, fetch)` extracting into `<root>/.store/<hash>`
  and repointing the `<root>/<name>` symlink to the relative target
  `.store/<hash>` (`skillstore.go:78-104`). Tests:
  `internal/skillstore/skillstore_test.go` (560 lines of extract/link
  cases — they all keep passing with only path expectations updated).
- Consumers of `Ensure`: `internal/cmd/skills.go:133-140`
  (`lode skills install`) and `internal/hookrun/hookrun.go:379-425`
  (`ensureSkills`, the session-start lazy fetch). The brief carries the
  path `Ensure` returns, so the move is invisible to it (spec §3.3).
- Worktree git plumbing: `internal/worktree/worktree.go` — `GitDir`
  exists; there is no `info/exclude` helper yet.
- Harness skill targets: part 1's `SkillTarget{Dir, PerSkill}` on each
  adapter (`~/.agents/skills` for codex/copilot/amp, `~/.claude/skills`
  per-skill for claude-code).
- `docs/follow-ups.md`: nothing there touches skill delivery.

**Plan-level decisions (deliberate, under the spec's open questions):**

1. **Q024.2 (store relocation): migrate silently, by rename.** `Ensure`
   migrates a legacy `<links>/.store/` on first touch — renames each hash
   dir into the new store, repoints name symlinks, removes `.store/`.
   Renames are cheap and content-addressed dirs are immutable, so this is
   strictly better than the re-fetch alternative Q024.2 offers; a rename
   failure falls back to re-fetch naturally (the store dir simply is not
   there).
2. **The store dir is the sibling `store` of the links dir's parent**:
   `~/.worklode/skills` → `~/.worklode/store`, and under
   `LODE_SKILLS_DIR=/x/skills` → `/x/store`. One env var keeps configuring
   both, and every existing test that sets `LODE_SKILLS_DIR` keeps working.
3. **`lode install --skills` publishes for every registered adapter**, not
   only detected ones: acceptance 2 requires all five doorways from one
   command, the links are inert for an absent harness, and a harness
   installed later then works without re-running.
4. **Symlink-fallback copies are v1's whole Windows story** (spec §4 row 5).
   `internal/hookrun` already uses `syscall.Kill`, so Worklode does not run
   on Windows today; the fallback is implemented and tested because it is
   cheap, not because Windows is supported.

**Out of scope:** everything in part 1 and part 3; the v2 usage loop; any
change to the 016 sync/recommend/brief server surface.

---

## File structure

| Path | Responsibility |
|---|---|
| `internal/skillstore/skillstore.go` | `Dirs{Links, Store}`, `DefaultDirs()`, `Ensure` against the new layout, legacy migration |
| `internal/skillstore/skillstore_test.go` | path expectations updated; migration cases added |
| `internal/skillstore/publish.go` (new) | `PublishDirLink`, `PublishPerSkill`, copy fallback |
| `internal/skillstore/publish_test.go` (new) | link modes, foreign-dir degradation, copy fallback, idempotence |
| `internal/cmd/install.go` | `--skills` flag → publish over adapter skill targets |
| `internal/cmd/install_test.go` | `--skills` report test |
| `internal/cmd/skills.go` | `lode skills install --link <harness>\|all`; `Ensure` call sites on `Dirs` |
| `internal/cmd/skills_test.go` | `--link` tests |
| `internal/hookrun/hookrun.go` | worktree `.agents/skills/<name>` links + `info/exclude` in `ensureSkills` |
| `internal/hookrun/hookrun_test.go` | worktree-link and exclude assertions |
| `internal/worktree/worktree.go` | `ExcludeFile(root)` helper |
| `internal/worktree/worktree_test.go` | `ExcludeFile` test |

**Test commands:** `go test ./internal/skillstore/ ./internal/cmd/
./internal/hookrun/ ./internal/worktree/` — no Postgres needed anywhere in
this part. Commit after every task, imperative mood, no trailers.

---

## Tasks

### Task 1 — Move the store out of the linked directory

```yaml
kind: chore
priority: high
skills:
  - superpowers:test-driven-development
blockedBy: [ ]
```

**Files:**
- Modify: `internal/skillstore/skillstore.go`, `internal/skillstore/skillstore_test.go`, `internal/cmd/skills.go`, `internal/hookrun/hookrun.go`

- [ ] **Step 1: Write the failing tests**

In `internal/skillstore/skillstore_test.go`, add:

```go
func TestDefaultDirs(t *testing.T) {
	t.Setenv("LODE_SKILLS_DIR", "/x/skills")
	d, err := DefaultDirs()
	if err != nil || d.Links != "/x/skills" || d.Store != "/x/store" {
		t.Fatalf("dirs = %+v, %v", d, err)
	}
}

func TestEnsurePlacesVersionsInStoreDir(t *testing.T) {
	// Existing Ensure fixtures, but assert: the returned path is
	// <store>/<hash>, the name symlink target is the *relative* path from
	// links to store ("../store/<hash>"), and — the point of the move —
	// the links dir contains ONLY name symlinks: no entry named ".store",
	// no hash-named dirs (spec 024 acceptance 2's second sentence).
}

func TestEnsureMigratesLegacyStore(t *testing.T) {
	base := t.TempDir()
	links := filepath.Join(base, "skills")
	// Build the legacy layout by hand: links/.store/<hash>/SKILL.md plus
	// links/<name> -> .store/<hash>.
	// Then run Ensure for a DIFFERENT skill and assert:
	//  - links/.store is gone,
	//  - the legacy hash dir now lives at base/store/<hash> with its
	//    content intact,
	//  - the legacy name symlink resolves (target rewritten to
	//    ../store/<hash>),
	//  - a second Ensure run finds nothing left to migrate (idempotent).
}
```

Run: `go test ./internal/skillstore/` — FAIL (`DefaultDirs` undefined).

- [ ] **Step 2: Implement**

In `skillstore.go`:

```go
// Dirs locates the two halves of the local skill cache. Links holds one
// symlink per skill name and nothing else — harnesses walk it, so a hash
// dir here would surface every version as a duplicate skill (spec 024
// §3.3). Store holds the immutable content-addressed version dirs.
type Dirs struct {
	Links string // ~/.worklode/skills
	Store string // ~/.worklode/store
}

// DefaultDirs resolves the cache location: $LODE_SKILLS_DIR (links; the
// store is its parent's "store" sibling) or ~/.worklode/{skills,store}.
func DefaultDirs() (Dirs, error) {
	links, err := Root()
	if err != nil {
		return Dirs{}, err
	}
	return Dirs{Links: links, Store: filepath.Join(filepath.Dir(links), "store")}, nil
}
```

`Root()` keeps its exact behaviour (other code paths still call it for the
links dir). Change `Ensure` to
`Ensure(dirs Dirs, name, hash string, fetch func() ([]byte, error)) (string, error)`:
`dst := filepath.Join(dirs.Store, hash)`; the symlink target becomes
`relTarget(dirs, hash)`:

```go
// relTarget is the symlink target from the links dir to a store version,
// relative so the ~/.worklode tree stays relocatable as a unit.
func relTarget(dirs Dirs, hash string) string {
	rel, err := filepath.Rel(dirs.Links, filepath.Join(dirs.Store, hash))
	if err != nil {
		return filepath.Join(dirs.Store, hash) // disjoint roots: absolute
	}
	return rel
}
```

Add the migration, called at the top of `Ensure`:

```go
// migrateLegacyStore moves a pre-spec-024 <links>/.store/ into dirs.Store
// (Q024.2: silent, by rename — content-addressed dirs are immutable, so a
// rename either fully succeeds or leaves the version to be re-fetched) and
// repoints name symlinks that still target ".store/<hash>". Best-effort:
// any failure leaves Ensure to fetch as if the version were absent.
func migrateLegacyStore(dirs Dirs) {
	legacy := filepath.Join(dirs.Links, ".store")
	entries, err := os.ReadDir(legacy)
	if err != nil {
		return // no legacy store — the common case, one cheap ReadDir
	}
	_ = os.MkdirAll(dirs.Store, 0o755)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dst := filepath.Join(dirs.Store, e.Name())
		if _, err := os.Stat(dst); err == nil {
			_ = os.RemoveAll(filepath.Join(legacy, e.Name()))
			continue
		}
		_ = os.Rename(filepath.Join(legacy, e.Name()), dst)
	}
	links, err := os.ReadDir(dirs.Links)
	if err != nil {
		return
	}
	for _, e := range links {
		p := filepath.Join(dirs.Links, e.Name())
		target, err := os.Readlink(p)
		if err != nil || !strings.HasPrefix(target, ".store"+string(filepath.Separator)) {
			continue
		}
		_ = swapSymlink(relTarget(dirs, filepath.Base(target)), p)
	}
	_ = os.RemoveAll(legacy)
}
```

Update the two `Ensure` call sites to build `Dirs` via `DefaultDirs()`
(replacing their `skillstore.Root()` calls): `internal/cmd/skills.go:133`
and `internal/hookrun/hookrun.go:380`. Update the package doc comment
(`skillstore.go:1-10`) to describe the split layout.

- [ ] **Step 3: Verify**

```bash
go test ./internal/skillstore/ ./internal/cmd/ ./internal/hookrun/ -count=1
```

Every pre-existing extract/symlink test passes with the new paths; the
migration tests pass; `TestSkillsInstallIdempotent` in
`internal/cmd/skills_test.go` still passes (it goes through `Ensure`).

- [ ] **Step 4: Commit**

```bash
git add internal/skillstore internal/cmd internal/hookrun
git commit -m "Move the skill store out of the linked skills directory"
```

---

### Task 2 — Publish: directory link, per-skill links, copy fallback

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Create: `internal/skillstore/publish.go`, `internal/skillstore/publish_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/skillstore/publish_test.go`:

```go
func TestPublishDirLinkCreatesSymlink(t *testing.T) {
	dirs := testDirs(t) // helper: Dirs under t.TempDir() with one installed skill
	agents := filepath.Join(t.TempDir(), ".agents", "skills")
	res, err := PublishDirLink(dirs, agents)
	if err != nil || res.Action != "linked" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	target, _ := os.Readlink(agents)
	if target != dirs.Links {
		t.Fatalf("target = %s; want %s", target, dirs.Links)
	}
	// Re-run: "unchanged". A symlink already pointing elsewhere: "skipped"
	// and untouched — never repoint a link Worklode did not create.
}

func TestPublishDirLinkDegradesToPerSkillInsideRealDir(t *testing.T) {
	// agents dir exists as a REAL directory with a foreign skill in it:
	// spec 008 §18 row 4 — link per-skill inside it, delete nothing.
	dirs := testDirs(t)
	agents := filepath.Join(t.TempDir(), "skills")
	os.MkdirAll(filepath.Join(agents, "their-skill"), 0o755)
	res, err := PublishDirLink(dirs, agents)
	if err != nil || res.Action != "per-skill" {
		t.Fatalf("publish: %+v %v", res, err)
	}
	// their-skill untouched; our skill linked as agents/<name> -> version dir.
}

func TestPublishPerSkill(t *testing.T) {
	// Target dir gets one symlink per name in dirs.Links, each resolving
	// to the version dir (the RESOLVED store path, not the name link, so
	// the harness never double-resolves through ~/.worklode). Foreign
	// entries in the target dir survive. Re-run converges. A target entry
	// that is a real dir (user's own skill with the same name) is skipped
	// and reported, never replaced.
}

func TestPublishCopyFallback(t *testing.T) {
	// Inject symlink failure (see below) and assert the version dir is
	// copied file-for-file instead, with res naming the copy so a stale
	// copy is diagnosable (spec 008 §18 row 5).
}
```

For the fallback test, make the symlink call injectable:
`var symlink = os.Symlink` in `publish.go`; the test swaps it for one that
returns an error (same pattern as `skillstore`'s `maxExtracted` var).

Run — FAIL.

- [ ] **Step 2: Implement `publish.go`**

```go
// PublishResult reports one publication step for install's report.
type PublishResult struct {
	Path   string   `json:"path"`
	Action string   `json:"action"` // linked | per-skill | copied | unchanged | skipped
	Skips  []string `json:"skips,omitempty"`
}

// PublishDirLink makes target a symlink to the whole links dir — one link
// serving every harness that reads target (spec 008 §17.3). An existing
// real directory degrades to per-skill links inside it; an existing
// foreign symlink is skipped untouched.
func PublishDirLink(dirs Dirs, target string) (PublishResult, error)

// PublishPerSkill links <target>/<name> to the resolved version dir for
// every name symlink in dirs.Links. For directories users own and
// populate themselves (~/.claude/skills), where replacing the directory
// would strip their own skills.
func PublishPerSkill(dirs Dirs, target string) (PublishResult, error)
```

Implementation notes that decide behaviour:

- `PublishDirLink`: `os.Lstat(target)` — absent ⇒ `MkdirAll(parent)` +
  `symlink(dirs.Links, target)`; symlink to `dirs.Links` ⇒ `unchanged`;
  symlink elsewhere ⇒ `skipped`; real dir ⇒ delegate to `PublishPerSkill`
  with action `per-skill`.
- `PublishPerSkill`: iterate `os.ReadDir(dirs.Links)`, skip non-symlinks;
  resolve each name link (`filepath.EvalSymlinks`) to its version dir and
  link `<target>/<name>` → that absolute path. Existing entry: symlink to
  the same target ⇒ skip silently; symlink elsewhere or a real
  dir ⇒ append to `Skips` (never delete a path Worklode did not create,
  spec §4 row 4). Use `swapSymlink` for atomic replace of our own stale
  links.
- Copy fallback: when `symlink` errors, `copyDir(versionDir, linkPath)`
  (walk + copy regular files, preserve the exec bit — same mode policy as
  `extract`, `skillstore.go:166-178`) and set action `copied`.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/skillstore/ -count=1
git add internal/skillstore
git commit -m "Publish the skill store into harness skill directories"
```

---

### Task 3 — Wire `lode install --skills` and `lode skills install --link`

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [2]
```

**Files:**
- Modify: `internal/cmd/install.go`, `internal/cmd/install_test.go`, `internal/cmd/skills.go`, `internal/cmd/skills_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/cmd/install_test.go`:

```go
func TestInstallSkillsPublishesAllDoorways(t *testing.T) {
	// LODE_SKILLS_DIR + HOME under t.TempDir(); one skill pre-installed via
	// skillstore.Ensure. Run installHooks with skills enabled and assert
	// (acceptance 2):
	//  - $HOME/.agents/skills is a symlink to the links dir,
	//  - $HOME/.claude/skills/<name> resolves to the version dir,
	//  - the JSON report carries a "skills" list with one entry per target,
	//  - neither path lists a "store" or ".store" entry,
	// and that running it twice reports unchanged/skips rather than erroring.
}
```

Append to `internal/cmd/skills_test.go` (the `skillsTestServer` +
`seedInstallableSkill` fixtures already cover the fetch side):

```go
func TestSkillsInstallLink(t *testing.T) {
	// lode skills install <name> --link claude-code: after Ensure, the
	// per-skill link exists under $HOME/.claude/skills. --link all touches
	// every registered adapter's personal targets. --link nonsense errors
	// naming the registered ids and "all".
}
```

Run — FAIL.

- [ ] **Step 2: Implement**

`internal/cmd/install.go`:

1. `addHookFlags` gains `cmd.Flags().Bool("skills", false, "publish the
   Worklode skill store into every harness's skill directories")` — a flag
   rather than a default because it writes outside the hook config
   (spec §3.2).
2. `installHooks` (or a sibling `installSkills(res *installResult)` it
   calls when the flag is set): collect `SkillTarget`s from **every**
   registered adapter (`harness.IDs()`, decision 3), dedupe by `Dir`, then
   `PublishPerSkill` for `PerSkill` targets and `PublishDirLink`
   otherwise, appending each `PublishResult` to
   `installResult.Skills []skillstore.PublishResult \`json:"skills,omitempty"\``.
   A publish error on one target is recorded and the loop continues —
   mirrors the install-is-not-atomic reporting stance
   (`install.go:181-190`).
3. `reportInstall` prints one line per publish result
   (`skills: linked ~/.agents/skills -> ~/.worklode/skills`,
   `skills: skipped ~/.claude/skills/foo (exists, not ours)`).
4. Uninstall does **not** remove skill links in v1: they are inert data,
   `--skills` was an explicit opt-in, and removing `~/.claude/skills`
   entries risks user content. Document that in the uninstall long help.

`internal/cmd/skills.go`: `newSkillsInstallCmd` gains
`--link <harness>|all` (string flag, empty = no publication). After
`Ensure` returns `p`, resolve the adapter set (`all` ⇒ `harness.IDs()`),
collect their `SkillTargets("", harness.ScopeLocal)`, and for each:
`PerSkill` targets get a single-name variant — add
`PublishOneSkill(dirs Dirs, target, name string) (PublishResult, error)` to
`publish.go` (the per-skill loop body factored out; `PublishPerSkill`
calls it) — dir targets get `PublishDirLink`. Print each result line to
stdout after the existing path line.

- [ ] **Step 3: Verify and commit**

```bash
go test ./internal/cmd/ ./internal/skillstore/ -count=1
git add internal/cmd internal/skillstore
git commit -m "Publish skills from lode install --skills and skills install --link"
```

---

### Task 4 — Link brief skills into the worktree

```yaml
kind: feature
priority: medium
skills:
  - superpowers:test-driven-development
blockedBy: [1]
```

**Files:**
- Modify: `internal/hookrun/hookrun.go`, `internal/hookrun/hookrun_test.go`, `internal/worktree/worktree.go`, `internal/worktree/worktree_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/worktree/worktree_test.go`:

```go
func TestExcludeFile(t *testing.T) {
	// In a fixture repo (the file's existing initRepo-style helper),
	// ExcludeFile(root) returns <git-common-dir>/info/exclude and the
	// parent dir exists after the call.
}
```

`internal/hookrun/hookrun_test.go` — extend the existing session-start
skill-fetch test (the one driving `ensureSkills` through a fake client and
`LODE_SKILLS_DIR`) to assert, after a session-start in a `wt/<id>-<slug>`
fixture worktree whose brief pins one skill:

```go
	// Project-scope delivery (spec 008 §17.3): the worktree now carries
	// .agents/skills/<name> resolving to the store version dir…
	link := filepath.Join(wt, ".agents", "skills", "tdd")
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || !strings.HasPrefix(resolved, storeDir) {
		t.Fatalf("worktree link = %s (%v)", resolved, err)
	}
	// …and .agents/ is excluded via info/exclude, not .gitignore — the
	// links are machine-local and must never become a commit.
	excl, _ := os.ReadFile(excludePath)
	if !strings.Contains(string(excl), ".agents/") {
		t.Fatalf("info/exclude missing .agents/: %s", excl)
	}
	// gitignore untouched:
	if _, err := os.Stat(filepath.Join(wt, ".gitignore")); err == nil {
		t.Fatal("a .gitignore appeared")
	}
```

Also assert idempotence: a second session-start appends nothing to
`info/exclude` (exactly one `.agents/` line).

Run — FAIL.

- [ ] **Step 2: Implement**

`internal/worktree/worktree.go`:

```go
// ExcludeFile returns the repo's info/exclude path (creating its parent),
// via `git rev-parse --git-path info/exclude` — which resolves to the
// common dir for linked worktrees, exactly where per-machine excludes
// belong.
func ExcludeFile(root string) (string, error) {
	out, err := exec.Command("git", "-C", root, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git worktree: %w", root, err)
	}
	p := strings.TrimSpace(string(out))
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, nil
}
```

`internal/hookrun/hookrun.go` — `ensureSkills` currently takes `(ctx, opts,
c, brief)`; give it the worktree root (its caller `handleSessionStart` has
`root` in scope, `hookrun.go:368`). Inside the per-skill `ensure` closure,
after a successful `skillstore.Ensure` (where `paths[name] = p` is set,
`hookrun.go:413`), add the worktree link:

```go
				linkWorktreeSkill(opts, root, name, p)
```

with, in the same file:

```go
// linkWorktreeSkill links <root>/.agents/skills/<name> to the store
// version dir, so any harness opened in this worktree reads exactly the
// skills its brief named — a sandbox needs no lode install (spec 024
// §3.3). Failures are warnings; the brief's inline content still stands.
func linkWorktreeSkill(opts Options, root, name, versionDir string) {
	dir := filepath.Join(root, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		warn(opts, "worktree skill link %s: %v", name, err)
		return
	}
	link := filepath.Join(dir, name)
	if cur, err := os.Readlink(link); err == nil && cur == versionDir {
		return
	}
	_ = os.Remove(link) // stale version link; ours by construction
	if err := os.Symlink(versionDir, link); err != nil {
		warn(opts, "worktree skill link %s: %v", name, err)
		return
	}
	ensureExcluded(opts, root)
}

// ensureExcluded appends ".agents/" to the repo's info/exclude once —
// never .gitignore: the links are machine-local (spec 008 §17.3).
func ensureExcluded(opts Options, root string) {
	p, err := worktree.ExcludeFile(root)
	if err != nil {
		warn(opts, "git exclude: %v", err)
		return
	}
	data, _ := os.ReadFile(p)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == ".agents/" {
			return
		}
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		warn(opts, "git exclude: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintln(f, ".agents/")
}
```

(`ensureExcluded` is called under `ensureSkills`'s mutex-protected section
or moved after `g.Wait()` and called once when any link was made — pick the
latter: collect a `linked bool` and call once, keeping concurrent appends
impossible.)

- [ ] **Step 3: Verify**

```bash
go test ./internal/worktree/ ./internal/hookrun/ -count=1
go test ./... -count=1
```

- [ ] **Step 4: Commit**

```bash
git add internal/worktree internal/hookrun
git commit -m "Link brief skills into the worktree's .agents/skills"
```

---

## Done when (part 2)

1. With one skill installed once, Codex/Copilot/pi/opencode read it through
   `~/.agents/skills` and Claude Code through `~/.claude/skills/<name>`,
   all resolving into a single `~/.worklode/store/<hash>` copy
   (acceptance 2).
2. No harness walking any linked directory encounters a `.store`/`store`
   hash dir (acceptance 2, second sentence).
3. A legacy `~/.worklode/skills/.store` layout migrates on the next
   `Ensure` with no re-download and no broken name links.
4. A session-start in a Worklode worktree leaves
   `.agents/skills/<name>` links for the brief's skills, `git status` shows
   nothing, and `.gitignore` is untouched.
5. Publication never deletes or repoints anything it did not create; every
   skip is named in the report.
