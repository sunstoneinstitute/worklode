---
status: superseded
covers: docs/specs/008-worklode-plugin.md
---
# `lode install` / `lode uninstall` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `lode install-git-hooks` and `lode claude install` / `lode claude uninstall` with two unified top-level commands, `lode install` and `lode uninstall`, each targeting a VCS integration (`--vcs git`) and a coding-agent integration (`--agent claude-code`), with `--no-vcs` / `--no-agent` opt-outs.

**Architecture:** The two hook mechanisms stay exactly as they are — a git `pre-commit` script written into the repo's resolved hooks directory, and `lode hook` bindings written into a Claude Code settings JSON file. What changes is the CLI surface in front of them: a new `internal/cmd/install.go` owns both commands, resolves which integrations to act on, dispatches to `installGitHooks`/`uninstallGitHooks` and `installClaudeHooks`/`uninstallClaudeHooks`, and renders one combined text or JSON report. `installhooks.go` is renamed to `githooks.go` and demoted to pure hook mechanics (it gains the previously missing `uninstallGitHooks`); `claude.go` keeps its settings-file functions and loses its cobra command tree.

**Tech Stack:** Go, `github.com/spf13/cobra`, standard-library testing.

---

## Why `--vcs` for pre-commit

The pre-commit mechanism here *is* git plumbing: it resolves `core.hooksPath`, writes into the repo's hooks directory, and is scoped to a git worktree root. Modeling it as `--vcs git` leaves room for `--vcs jj` later without a breaking flag change. The separate **pre-commit framework** (`.pre-commit-config.yaml`) is not a competing VCS and is never a `--vcs` value — it stays what it is today: one of the chain targets the git installer defers to.

## CLI surface

```
lode install   [--vcs git] [--no-vcs] [--agent claude-code] [--no-agent] [--scope local|project] [--json]
lode uninstall [--vcs git] [--no-vcs] [--agent claude-code] [--no-agent] [--scope local|project] [--json]
```

- `--vcs` defaults to `git`; any other value is rejected with `unsupported --vcs "x" (supported: git)`.
- `--agent` defaults to `claude-code`; any other value is rejected the same way.
- `--no-vcs` / `--no-agent` skip an integration. Passing `--vcs` and `--no-vcs` together (or `--agent` and `--no-agent`) is a contradiction, not a precedence question, so it errors.
- Both skipped errors with `nothing to do: --no-vcs and --no-agent were both given`.
- `--scope` applies only to the agent settings file, unchanged from today's `lode claude install --scope`.

No backwards-compatibility aliases are kept. A repo-wide grep confirmed nothing outside this repo invokes the old command names.

## File structure

| File | Responsibility |
|---|---|
| `internal/cmd/githooks.go` (renamed from `installhooks.go`) | Git pre-commit hook mechanics only: `installGitHooks`, new `uninstallGitHooks`, `resolveHooksDir`, `renderHookScript`. No cobra command. |
| `internal/cmd/githooks_test.go` (renamed from `installhooks_test.go`) | Tests for the above. |
| `internal/cmd/claude.go` | Claude Code settings-file mechanics only: `installClaudeHooks`, `uninstallClaudeHooks`, `settingsPathForScope`, `claudeSettingsPath`, binding table. No cobra command. |
| `internal/cmd/install.go` (new) | The `lode install` and `lode uninstall` cobra commands, shared flag declaration and target resolution, the install/uninstall runners, and text/JSON reporting. |
| `internal/cmd/install_test.go` (new) | Tests for target resolution, the runners, and command output. |

`install` and `uninstall` share their flags, target resolution, and report shape, so they live in one file — files that change together live together.

---

### Task 1: Rename `installhooks.go` → `githooks.go` and drop the old command

The git hook file becomes pure mechanics. The `install-git-hooks` cobra command goes away now; `lode install` replaces it in Task 4. Between these tasks the CLI has no hook-install command — that is an expected transient, and every test calls the internal functions directly so the suite stays green.

**Files:**
- Rename: `internal/cmd/installhooks.go` → `internal/cmd/githooks.go`
- Rename: `internal/cmd/installhooks_test.go` → `internal/cmd/githooks_test.go`
- Modify: `internal/cmd/githooks.go` (remove `init`, `newInstallHooksCmd`; update `renderHookScript` header text)
- Modify: `internal/cmd/githooks_test.go` (update the expected hook body)

- [ ] **Step 1: Rename both files with git so history follows**

```bash
git mv internal/cmd/installhooks.go internal/cmd/githooks.go
git mv internal/cmd/installhooks_test.go internal/cmd/githooks_test.go
```

- [ ] **Step 2: Delete the cobra command from `internal/cmd/githooks.go`**

Remove the whole `init()` function and the whole `newInstallHooksCmd()` function (currently lines 20–65). The file's remaining top-level declarations must be, in order: the `hookMarker` const, `installGitHooks`, `resolveHooksDir`, `preCommitConfigExists`, `fileExists`, `renderHookScript`, `shellSingleQuote`.

Then fix the imports — `encoding/json` and `github.com/spf13/cobra` are now unused. The import block becomes exactly:

```go
import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)
```

- [ ] **Step 3: Update the generated hook's header comment to name the new command**

In `renderHookScript`, change the `header` const:

```go
func renderHookScript(chainedTo string) string {
	const header = "#!/bin/sh\n" + hookMarker + " v1 — installed by `lode install`; do not edit.\n"
	if chainedTo == "" {
		return header + `exec lode hook pre-commit "$@"` + "\n"
	}
	return header + fmt.Sprintf(`exec lode hook pre-commit --next %s "$@"`, shellSingleQuote(chainedTo)) + "\n"
}
```

- [ ] **Step 4: Update the test that asserts the exact hook body**

In `internal/cmd/githooks_test.go`, inside `TestInstallGitHooksFreshInstall`, replace the `want` line:

```go
	want := "#!/bin/sh\n# worklode-hook v1 — installed by `lode install`; do not edit.\nexec lode hook pre-commit \"$@\"\n"
```

- [ ] **Step 5: Run the package tests**

Run: `go test ./internal/cmd/ -run TestInstallGitHooks -v`
Expected: PASS for every `TestInstallGitHooks*` test.

- [ ] **Step 6: Verify the whole build and vet are clean**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output from any of the three.

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/githooks.go internal/cmd/githooks_test.go
git commit -m "Rename installhooks.go to githooks.go and drop the install-git-hooks command"
```

---

### Task 2: Add `uninstallGitHooks`

There is no way to remove Worklode's pre-commit hook today. This adds one that mirrors install's caution: it never touches a `pre-commit` it does not recognize as its own.

Defined behaviour, in order:

1. Resolve the hooks directory. If `pre-commit` is absent, or is present but does **not** contain `hookMarker`, do nothing and report action `none`.
2. Otherwise remove `pre-commit`.
3. If `pre-commit.pre-lode` exists, rename it back to `pre-commit` and report action `restored`. Otherwise report action `removed`.

**Files:**
- Modify: `internal/cmd/githooks.go` (add `uninstallGitHooks` and the action constants after `installGitHooks`)
- Modify: `internal/cmd/githooks_test.go` (add three tests at the end of the file)

- [ ] **Step 1: Write the failing tests**

Append to `internal/cmd/githooks_test.go`:

```go
// --- uninstall ---------------------------------------------------------------

func TestUninstallGitHooksRemovesOurHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, _, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}

	gotDir, action, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("uninstallGitHooks: %v", err)
	}
	if gotDir != hooksDir {
		t.Fatalf("hooksDir = %q, want %q", gotDir, hooksDir)
	}
	if action != hookActionRemoved {
		t.Fatalf("action = %q, want %q", action, hookActionRemoved)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present after uninstall")
	}

	// Re-running on an already-clean repo is a no-op, not an error.
	_, action2, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("second uninstallGitHooks: %v", err)
	}
	if action2 != hookActionNone {
		t.Fatalf("second run action = %q, want %q", action2, hookActionNone)
	}
}

func TestUninstallGitHooksRestoresPreservedHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\necho third-party\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}
	if _, _, err := installGitHooks(root); err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}

	_, action, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("uninstallGitHooks: %v", err)
	}
	if action != hookActionRestored {
		t.Fatalf("action = %q, want %q", action, hookActionRestored)
	}
	if got := readFile(t, preCommitPath); got != thirdParty {
		t.Fatalf("pre-commit after uninstall = %q, want the original third-party hook %q", got, thirdParty)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit.pre-lode")) {
		t.Fatal("pre-commit.pre-lode still present after restore")
	}
	if mode := fileMode(t, preCommitPath); mode.Perm() != 0o755 {
		t.Fatalf("restored pre-commit mode = %v, want 0755", mode.Perm())
	}
}

func TestUninstallGitHooksLeavesForeignHookAlone(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	foreign := "#!/bin/sh\necho not ours\n"
	if err := os.WriteFile(preCommitPath, []byte(foreign), 0o755); err != nil {
		t.Fatalf("write foreign pre-commit: %v", err)
	}

	_, action, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("uninstallGitHooks: %v", err)
	}
	if action != hookActionNone {
		t.Fatalf("action = %q, want %q", action, hookActionNone)
	}
	if got := readFile(t, preCommitPath); got != foreign {
		t.Fatalf("foreign pre-commit was modified: %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cmd/ -run TestUninstallGitHooks -v`
Expected: FAIL to compile with `undefined: uninstallGitHooks`, `undefined: hookActionRemoved`, `undefined: hookActionNone`, `undefined: hookActionRestored`.

- [ ] **Step 3: Implement `uninstallGitHooks`**

In `internal/cmd/githooks.go`, add the action constants immediately after the `hookMarker` const:

```go
// What an uninstall did to the pre-commit hook.
const (
	hookActionNone     = "none"     // nothing of ours was there to remove
	hookActionRemoved  = "removed"  // our hook was removed, nothing to put back
	hookActionRestored = "restored" // our hook was removed and the preserved original put back
)
```

And add `uninstallGitHooks` immediately after `installGitHooks`:

```go
// uninstallGitHooks removes Worklode's pre-commit hook from repoDir's shared
// hooks directory and restores whatever install preserved. It returns the
// resolved hooks directory and one of the hookAction constants.
//
// It only ever removes a pre-commit carrying hookMarker: a hook it does not
// recognize as its own belongs to someone else and is left untouched, mirroring
// install's refusal to clobber third-party hooks. Uninstalling twice, or in a
// repo that never installed, is a no-op rather than an error.
func uninstallGitHooks(repoDir string) (hooksDir, action string, err error) {
	hooksDir, err = resolveHooksDir(repoDir)
	if err != nil {
		return "", "", err
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")

	existing, readErr := os.ReadFile(preCommitPath)
	if os.IsNotExist(readErr) {
		return hooksDir, hookActionNone, nil
	}
	if readErr != nil {
		return "", "", fmt.Errorf("read %s: %w", preCommitPath, readErr)
	}
	if !strings.Contains(string(existing), hookMarker) {
		return hooksDir, hookActionNone, nil
	}

	if err := os.Remove(preCommitPath); err != nil {
		return "", "", fmt.Errorf("remove %s: %w", preCommitPath, err)
	}
	if !fileExists(preLodePath) {
		return hooksDir, hookActionRemoved, nil
	}
	if err := os.Rename(preLodePath, preCommitPath); err != nil {
		return "", "", fmt.Errorf("restore %s: %w", preCommitPath, err)
	}
	return hooksDir, hookActionRestored, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cmd/ -run 'TestUninstallGitHooks|TestInstallGitHooks' -v`
Expected: PASS for all of them.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/githooks.go internal/cmd/githooks_test.go
git commit -m "Add uninstallGitHooks to remove or restore the pre-commit hook"
```

---

### Task 3: Target resolution for the new flags

Pure flag-reading and validation, tested on its own before any command is wired up.

**Files:**
- Create: `internal/cmd/install.go`
- Create: `internal/cmd/install_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/cmd/install_test.go`:

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// targetsFor builds a bare command carrying the shared install/uninstall flags,
// parses args against it, and resolves them — the same path both real commands
// take, without running anything.
func targetsFor(t *testing.T, args ...string) (hookTargets, error) {
	t.Helper()
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	addHookFlags(cmd)
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	return resolveHookTargets(cmd)
}

func TestResolveHookTargetsDefaults(t *testing.T) {
	got, err := targetsFor(t)
	if err != nil {
		t.Fatalf("resolveHookTargets: %v", err)
	}
	if got.vcs != vcsGit || got.agent != agentClaudeCode {
		t.Fatalf("targets = %+v, want vcs=%q agent=%q", got, vcsGit, agentClaudeCode)
	}
}

func TestResolveHookTargetsOptOuts(t *testing.T) {
	got, err := targetsFor(t, "--no-vcs")
	if err != nil {
		t.Fatalf("--no-vcs: %v", err)
	}
	if got.vcs != "" || got.agent != agentClaudeCode {
		t.Fatalf("--no-vcs targets = %+v, want vcs empty, agent %q", got, agentClaudeCode)
	}

	got, err = targetsFor(t, "--no-agent")
	if err != nil {
		t.Fatalf("--no-agent: %v", err)
	}
	if got.vcs != vcsGit || got.agent != "" {
		t.Fatalf("--no-agent targets = %+v, want vcs %q, agent empty", got, vcsGit)
	}
}

func TestResolveHookTargetsRejectsNothingToDo(t *testing.T) {
	_, err := targetsFor(t, "--no-vcs", "--no-agent")
	if err == nil {
		t.Fatal("--no-vcs --no-agent was accepted, want an error")
	}
	if !strings.Contains(err.Error(), "nothing to do") {
		t.Fatalf("error = %v, want it to mention \"nothing to do\"", err)
	}
}

func TestResolveHookTargetsRejectsUnsupportedNames(t *testing.T) {
	if _, err := targetsFor(t, "--vcs", "svn"); err == nil {
		t.Fatal("--vcs svn was accepted, want an error")
	}
	if _, err := targetsFor(t, "--agent", "emacs"); err == nil {
		t.Fatal("--agent emacs was accepted, want an error")
	}
}

func TestResolveHookTargetsRejectsContradictions(t *testing.T) {
	if _, err := targetsFor(t, "--vcs", "git", "--no-vcs"); err == nil {
		t.Fatal("--vcs git --no-vcs was accepted, want an error")
	}
	if _, err := targetsFor(t, "--agent", "claude-code", "--no-agent"); err == nil {
		t.Fatal("--agent claude-code --no-agent was accepted, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cmd/ -run TestResolveHookTargets -v`
Expected: FAIL to compile with `undefined: addHookFlags`, `undefined: resolveHookTargets`, `undefined: hookTargets`, `undefined: vcsGit`, `undefined: agentClaudeCode`.

- [ ] **Step 3: Write the implementation**

Create `internal/cmd/install.go`:

```go
package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// The integrations `lode install`/`lode uninstall` know how to manage. Both
// flags take a name rather than being booleans so a second VCS or agent can be
// added later without changing the CLI shape.
const (
	vcsGit          = "git"
	agentClaudeCode = "claude-code"
)

// hookTargets is the pair of integrations one install or uninstall run acts on.
// An empty field means "skip this one".
type hookTargets struct {
	vcs   string
	agent string
}

// addHookFlags declares the flags shared by `lode install` and `lode uninstall`.
func addHookFlags(cmd *cobra.Command) {
	cmd.Flags().String("vcs", vcsGit, "version control system whose hooks to manage")
	cmd.Flags().String("agent", agentClaudeCode, "coding agent whose hooks to manage")
	cmd.Flags().Bool("no-vcs", false, "skip the version control system hooks")
	cmd.Flags().Bool("no-agent", false, "skip the coding agent hooks")
	cmd.Flags().String("scope", scopeLocal,
		"which agent settings file to write: local (settings.local.json) or project (settings.json)")
}

// resolveHookTargets turns the parsed flags into the set of integrations to act
// on. Naming an integration and opting out of it in the same run is a
// contradiction rather than a precedence question, so it is rejected instead of
// silently picking a winner.
func resolveHookTargets(cmd *cobra.Command) (hookTargets, error) {
	flags := cmd.Flags()
	vcs, _ := flags.GetString("vcs")
	agent, _ := flags.GetString("agent")
	noVCS, _ := flags.GetBool("no-vcs")
	noAgent, _ := flags.GetBool("no-agent")

	switch {
	case noVCS && flags.Changed("vcs"):
		return hookTargets{}, errors.New("--vcs and --no-vcs are mutually exclusive")
	case noAgent && flags.Changed("agent"):
		return hookTargets{}, errors.New("--agent and --no-agent are mutually exclusive")
	}

	if noVCS {
		vcs = ""
	} else if vcs != vcsGit {
		return hookTargets{}, fmt.Errorf("unsupported --vcs %q (supported: %s)", vcs, vcsGit)
	}
	if noAgent {
		agent = ""
	} else if agent != agentClaudeCode {
		return hookTargets{}, fmt.Errorf("unsupported --agent %q (supported: %s)", agent, agentClaudeCode)
	}
	if vcs == "" && agent == "" {
		return hookTargets{}, errors.New("nothing to do: --no-vcs and --no-agent were both given")
	}
	return hookTargets{vcs: vcs, agent: agent}, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cmd/ -run TestResolveHookTargets -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/install.go internal/cmd/install_test.go
git commit -m "Add --vcs/--agent target resolution for lode install"
```

---

### Task 4: Make `settingsPathForScope` take an explicit directory

`settingsPathForScope` calls `os.Getwd()` internally, which makes the install runners untestable without `os.Chdir`. Threading the directory in from the caller removes that.

**Files:**
- Modify: `internal/cmd/claude.go:144-156` (`settingsPathForScope`)
- Modify: `internal/cmd/claude.go:87` and `:113` (the two call sites in the cobra commands)

- [ ] **Step 1: Change the signature**

Replace `settingsPathForScope` in `internal/cmd/claude.go` with:

```go
// settingsPathForScope resolves the settings file for scope, relative to the
// git worktree root containing dir.
func settingsPathForScope(dir, scope string) (string, error) {
	root, ok := worktree.Root(dir)
	if !ok {
		return "", fmt.Errorf("not inside a git repository: %s", dir)
	}
	return claudeSettingsPath(root, scope)
}
```

- [ ] **Step 2: Update the two existing call sites**

Both `newClaudeInstallCmd` and `newClaudeUninstallCmd` currently start their `RunE` with `path, err := settingsPathForScope(scope)`. In each, replace that single line with:

```go
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			path, err := settingsPathForScope(cwd, scope)
```

(These commands are deleted in Task 5; this keeps the tree compiling in between.)

- [ ] **Step 3: Verify the package builds and every test still passes**

Run: `go build ./... && go test ./internal/cmd/`
Expected: build silent, tests `ok`.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/claude.go
git commit -m "Pass the working directory into settingsPathForScope"
```

---

### Task 5: Wire up `lode install` and `lode uninstall`

Both commands land here, and `lode claude` is deleted in the same commit so there is no interval with dead code or two competing entry points.

**Files:**
- Modify: `internal/cmd/install.go` (add result types, runners, reporters, commands, `init`)
- Modify: `internal/cmd/install_test.go` (add runner tests)
- Modify: `internal/cmd/claude.go` (delete `init`, `newClaudeCmd`, `newClaudeInstallCmd`, `newClaudeUninstallCmd`, `reportClaudeCmd`)

- [ ] **Step 1: Write the failing tests**

Append to `internal/cmd/install_test.go`:

```go
func TestInstallHooksBothIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(root, hookTargets{vcs: vcsGit, agent: agentClaudeCode}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	if res.VCS == nil {
		t.Fatal("VCS result missing")
	}
	if !fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatalf("pre-commit not written into %s", res.VCS.HooksDir)
	}
	if res.Agent == nil {
		t.Fatal("agent result missing")
	}
	if want := filepath.Join(root, ".claude", "settings.local.json"); res.Agent.Path != want {
		t.Fatalf("agent path = %q, want %q", res.Agent.Path, want)
	}
	settings := readSettings(t, res.Agent.Path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 1 || got[0] != "lode hook session-start" {
		t.Fatalf("SessionStart commands: %v", got)
	}
}

func TestInstallHooksSkipsOptedOutIntegrations(t *testing.T) {
	root := initGitRepo(t)

	res, err := installHooks(root, hookTargets{vcs: vcsGit}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-agent: %v", err)
	}
	if res.Agent != nil {
		t.Fatalf("agent result = %+v, want nil when the agent is skipped", res.Agent)
	}
	if fileExists(filepath.Join(root, ".claude", "settings.local.json")) {
		t.Fatal("--no-agent wrote a settings file")
	}

	root2 := initGitRepo(t)
	res2, err := installHooks(root2, hookTargets{agent: agentClaudeCode}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks --no-vcs: %v", err)
	}
	if res2.VCS != nil {
		t.Fatalf("VCS result = %+v, want nil when the VCS is skipped", res2.VCS)
	}
	hooksDir, err := resolveHooksDir(root2)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("--no-vcs wrote a pre-commit hook")
	}
}

func TestUninstallHooksUndoesInstall(t *testing.T) {
	root := initGitRepo(t)
	targets := hookTargets{vcs: vcsGit, agent: agentClaudeCode}
	if _, err := installHooks(root, targets, scopeLocal); err != nil {
		t.Fatalf("installHooks: %v", err)
	}

	res, err := uninstallHooks(root, targets, scopeLocal)
	if err != nil {
		t.Fatalf("uninstallHooks: %v", err)
	}
	if res.VCS == nil || res.VCS.Action != hookActionRemoved {
		t.Fatalf("VCS result = %+v, want action %q", res.VCS, hookActionRemoved)
	}
	if fileExists(filepath.Join(res.VCS.HooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present after uninstall")
	}
	if res.Agent == nil {
		t.Fatal("agent result missing")
	}
	settings := readSettings(t, res.Agent.Path)
	if got := commandsFor(t, settings, "SessionStart"); len(got) != 0 {
		t.Fatalf("SessionStart after uninstall: %v, want none", got)
	}
}

func TestInstallCmdJSONOmitsSkippedIntegration(t *testing.T) {
	root := initGitRepo(t)
	res, err := installHooks(root, hookTargets{vcs: vcsGit}, scopeLocal)
	if err != nil {
		t.Fatalf("installHooks: %v", err)
	}
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := decoded["vcs"]; !ok {
		t.Fatalf("JSON missing the vcs key: %s", b)
	}
	if _, ok := decoded["agent"]; ok {
		t.Fatalf("JSON has an agent key for a skipped integration: %s", b)
	}
}
```

Extend the test file's import block to:

```go
import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cmd/ -run 'TestInstallHooks|TestUninstallHooks|TestInstallCmdJSON' -v`
Expected: FAIL to compile with `undefined: installHooks`, `undefined: uninstallHooks`.

- [ ] **Step 3: Add the result types, runners, reporters, and commands**

Append to `internal/cmd/install.go`:

```go
// installResult is what one `lode install` run did. A nil field means that
// integration was skipped, and is omitted from the JSON entirely.
type installResult struct {
	VCS   *vcsInstall   `json:"vcs,omitempty"`
	Agent *agentInstall `json:"agent,omitempty"`
}

type vcsInstall struct {
	VCS       string `json:"vcs"`
	HooksDir  string `json:"hooks_dir"`
	ChainedTo string `json:"chained_to"`
}

type agentInstall struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// uninstallResult is the same, for `lode uninstall`.
type uninstallResult struct {
	VCS   *vcsUninstall   `json:"vcs,omitempty"`
	Agent *agentUninstall `json:"agent,omitempty"`
}

type vcsUninstall struct {
	VCS      string `json:"vcs"`
	HooksDir string `json:"hooks_dir"`
	Action   string `json:"action"`
}

type agentUninstall struct {
	Agent string `json:"agent"`
	Path  string `json:"path"`
}

// installHooks installs every selected integration for the repo containing dir.
func installHooks(dir string, targets hookTargets, scope string) (installResult, error) {
	var res installResult
	if targets.vcs != "" {
		hooksDir, chainedTo, err := installGitHooks(dir)
		if err != nil {
			return installResult{}, err
		}
		res.VCS = &vcsInstall{VCS: targets.vcs, HooksDir: hooksDir, ChainedTo: chainedTo}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return installResult{}, err
		}
		if err := installClaudeHooks(path); err != nil {
			return installResult{}, err
		}
		res.Agent = &agentInstall{Agent: targets.agent, Path: path}
	}
	return res, nil
}

// uninstallHooks removes every selected integration from the repo containing dir.
func uninstallHooks(dir string, targets hookTargets, scope string) (uninstallResult, error) {
	var res uninstallResult
	if targets.vcs != "" {
		hooksDir, action, err := uninstallGitHooks(dir)
		if err != nil {
			return uninstallResult{}, err
		}
		res.VCS = &vcsUninstall{VCS: targets.vcs, HooksDir: hooksDir, Action: action}
	}
	if targets.agent != "" {
		path, err := settingsPathForScope(dir, scope)
		if err != nil {
			return uninstallResult{}, err
		}
		if err := uninstallClaudeHooks(path); err != nil {
			return uninstallResult{}, err
		}
		res.Agent = &agentUninstall{Agent: targets.agent, Path: path}
	}
	return res, nil
}

func newInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install Worklode's hooks for this repo's VCS and coding agent",
		Long: "Installs two integrations. The VCS side writes a pre-commit hook (into the repo's " +
			"shared hooks directory, honoring core.hooksPath) that invokes `lode hook pre-commit`, " +
			"chaining any pre-commit hook already present or the pre-commit framework. The agent " +
			"side writes Worklode's lifecycle hook bindings (session start/end, heartbeat, worktree " +
			"enter) into the repo's Claude Code settings file. Use --no-vcs or --no-agent to skip " +
			"either. Safe to re-run: both converge rather than accumulate.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			res, err := installHooks(cwd, targets, scope)
			if err != nil {
				return err
			}
			return reportInstall(cmd, res)
		},
	}
	addHookFlags(cmd)
	return cmd
}

func newUninstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Worklode's hooks from this repo's VCS and coding agent",
		Long: "Removes what `lode install` added. The VCS side removes Worklode's pre-commit hook " +
			"and restores whatever it preserved, leaving a third-party pre-commit hook it does not " +
			"recognize untouched. The agent side removes every `lode hook` binding from the repo's " +
			"Claude Code settings file, leaving all other settings — including third-party hooks on " +
			"the same events — in place. Use --no-vcs or --no-agent to skip either. A repo with " +
			"nothing installed is not an error.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			targets, err := resolveHookTargets(cmd)
			if err != nil {
				return err
			}
			scope, _ := cmd.Flags().GetString("scope")
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine working directory: %w", err)
			}
			res, err := uninstallHooks(cwd, targets, scope)
			if err != nil {
				return err
			}
			return reportUninstall(cmd, res)
		},
	}
	addHookFlags(cmd)
	return cmd
}

// reportInstall prints one line per integration, or the whole result as JSON.
func reportInstall(cmd *cobra.Command, res installResult) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	out := cmd.OutOrStdout()
	if res.VCS != nil {
		fmt.Fprintf(out, "%s: installed pre-commit hook in %s\n", res.VCS.VCS, res.VCS.HooksDir)
		if res.VCS.ChainedTo != "" {
			fmt.Fprintf(out, "%s: chains to %s\n", res.VCS.VCS, res.VCS.ChainedTo)
		}
	}
	if res.Agent != nil {
		fmt.Fprintf(out, "%s: installed hooks in %s\n", res.Agent.Agent, res.Agent.Path)
	}
	return nil
}

// reportUninstall is reportInstall for the removal side.
func reportUninstall(cmd *cobra.Command, res uninstallResult) error {
	if jsonOut(cmd) {
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		printRaw(cmd, b)
		return nil
	}
	out := cmd.OutOrStdout()
	if res.VCS != nil {
		switch res.VCS.Action {
		case hookActionRestored:
			fmt.Fprintf(out, "%s: removed pre-commit hook from %s and restored the previous one\n",
				res.VCS.VCS, res.VCS.HooksDir)
		case hookActionRemoved:
			fmt.Fprintf(out, "%s: removed pre-commit hook from %s\n", res.VCS.VCS, res.VCS.HooksDir)
		default:
			fmt.Fprintf(out, "%s: no Worklode pre-commit hook in %s\n", res.VCS.VCS, res.VCS.HooksDir)
		}
	}
	if res.Agent != nil {
		fmt.Fprintf(out, "%s: removed hooks from %s\n", res.Agent.Agent, res.Agent.Path)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(newInstallCmd(), newUninstallCmd())
}
```

Extend the import block at the top of `internal/cmd/install.go` to:

```go
import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)
```

- [ ] **Step 4: Delete the `claude` command tree from `internal/cmd/claude.go`**

Remove these five declarations entirely: `init()`, `newClaudeCmd()`, `newClaudeInstallCmd()`, `newClaudeUninstallCmd()`, `reportClaudeCmd()` (currently lines 63–142). Everything else in the file stays.

The remaining top-level declarations must be, in order: the `scopeLocal`/`scopeProject` consts, the `lodeHookPrefix` const, `claudeBinding`, `claudeBindings`, `settingsPathForScope`, `claudeSettingsPath`, `readSettingsFile`, `writeSettingsFile`, `installClaudeHooks`, `uninstallClaudeHooks`, `settingsHooks`, `appendBinding`, `stripLodeHooks`, `isLodeHookEntry`.

`github.com/spf13/cobra` is now unused there. The import block becomes exactly:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)
```

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./internal/cmd/ -v`
Expected: PASS. Every pre-existing test still passes alongside the new ones.

- [ ] **Step 6: Verify the CLI surface by hand**

Run:
```bash
go run ./cmd/lode --help
```
Expected: the command list contains `install` and `uninstall`, and no longer contains `claude` or `install-git-hooks`.

Run:
```bash
go run ./cmd/lode install --help
```
Expected: the flag list shows `--vcs`, `--agent`, `--no-vcs`, `--no-agent`, `--scope`.

Run:
```bash
go run ./cmd/lode install --no-vcs --no-agent
```
Expected: exits non-zero printing `nothing to do: --no-vcs and --no-agent were both given`.

- [ ] **Step 7: Verify build, vet, and formatting**

Run: `go build ./... && go vet ./... && gofmt -l .`
Expected: no output from any of the three.

- [ ] **Step 8: Commit**

```bash
git add internal/cmd/install.go internal/cmd/install_test.go internal/cmd/claude.go
git commit -m "Replace install-git-hooks and lode claude with lode install/uninstall"
```

---

### Task 6: Update the documentation

Living docs only. Dated files under `docs/plans/` are execution records of past work and are deliberately left as-is.

**Files:**
- Modify: `README.md:216-262` (the "Worklode plugin (Claude Code)" section)
- Modify: `docs/specs/008-worklode-plugin.md:104`, `:207`, `:225`
- Modify: `docs/specs/013-reconciliation.md:55`
- Modify: `docs/specs/006-knowledge-graph.md:185`

- [ ] **Step 1: Replace the README's git-hook paragraph**

In `README.md`, replace the paragraph that currently begins "Run `lode install-git-hooks` inside a repo" (lines 221–223) with:

```markdown
Run `lode install` inside a repo to install both integrations at once: a
pre-commit heartbeat hook (it renews the current task's lease on every commit,
chaining any pre-commit hook already installed) and the Claude Code hook
bindings described below. It is idempotent — safe to re-run.

```bash
lode install                     # git pre-commit hook + Claude Code bindings
lode install --no-agent          # git pre-commit hook only
lode install --no-vcs            # Claude Code bindings only
```

`--vcs` defaults to `git` and `--agent` to `claude-code`; those are the only
supported values today, and the flags exist so another VCS or agent can be added
without changing the CLI shape.
```

- [ ] **Step 2: Replace the README's install/uninstall block**

In `README.md`, replace the fenced block and the paragraph that follow "Install these bindings into a repo with:" (lines 253–262) with:

```markdown
Install these bindings into a repo with:

```
lode install                           # .claude/settings.local.json
lode install --scope project           # .claude/settings.json
```

`lode uninstall` (same flags) removes both integrations again: it restores
whatever pre-commit hook Worklode preserved and strips every `lode hook` entry
from the settings file. Both commands are idempotent, the VCS side never touches
a pre-commit hook it does not recognize as its own, and the agent side only
touches entries whose command starts with `lode hook`, so third-party hooks on
the same events are left alone.
```

- [ ] **Step 3: Update the three spec references**

In each of these lines, replace the literal `lode install-git-hooks` with `lode install` and `install-git-hooks` with `install`, leaving the surrounding sentence intact:

- `docs/specs/008-worklode-plugin.md:104` — "**`lode install-git-hooks`** wires the commit-cadence heartbeat…"
- `docs/specs/008-worklode-plugin.md:207` — "…get the `install-git-hooks` path"
- `docs/specs/008-worklode-plugin.md:225` — "4. `lode install-git-hooks` in a repo with an existing `pre-commit` hook…"
- `docs/specs/013-reconciliation.md:55` — "5. Git hooks installed in this repo (`lode install-git-hooks`)."
- `docs/specs/006-knowledge-graph.md:185` — "`lode install-git-hooks` wires the…"

- [ ] **Step 4: Verify no living doc still names the old commands**

Run: `grep -rn "install-git-hooks\|lode claude" README.md docs/specs/`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add README.md docs/specs/
git commit -m "Document lode install/uninstall"
```

---

### Task 7: Final verification

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: `ok` for every package, no failures.

- [ ] **Step 2: Run the lint gates CI runs**

Run: `gofmt -l . && go vet ./...`
Expected: no output from either.

- [ ] **Step 3: Confirm the old command names are gone from the binary**

Run:
```bash
go run ./cmd/lode install-git-hooks 2>&1 | head -3
go run ./cmd/lode claude install 2>&1 | head -3
```
Expected: both fail with an unknown-command error.
