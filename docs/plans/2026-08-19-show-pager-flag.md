---
status: draft
covers: NO-SPEC
---
# Show Commands --pager Flag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `--pager`/`-p` flag to `lode show` and `lode task show` (which also covers "doc show" — `lode show <spec-id>` — since it's the same dispatcher) that pipes rendered output through an external pager when connected to a terminal.

**Architecture:** A new `cli.Pager(enabled bool) (io.Writer, func())` in `internal/cli` decides whether to page (flag set AND `os.Stdout` is a TTY) and, if so, execs `$PAGER` (falling back to `less -R`), returning the pager subprocess's stdin as the writer to render into plus a cleanup func that closes it and waits for the process to exit. `internal/cmd` gets a thin `withPager(cmd, requested) func()` helper, indirected through a package var (`pagerFn`) so command-level tests can stub it out instead of ever touching a real terminal or spawning a real process. `show.go` and `task.go`'s `show` subcommand each get their own `--pager`/`-p` bool flag and call `withPager` at the top of `RunE`, swapping `cmd`'s output writer for the duration — `runTaskShow`/`runDocShow`/`runProjectShow` are unmodified since they already only ever write through `cmd.OutOrStdout()`.

**Tech Stack:** Go stdlib only (`os/exec`, `io`, `os`, `strings`, `fmt`) — no new dependencies.

## Global Constraints

- `--pager`/`-p` is a silent no-op when `os.Stdout` isn't an interactive terminal (piped/redirected output never spawns a pager).
- Pager selection: `$PAGER`'s fields when non-blank, else `less -R`. If the chosen command can't be found/started, print one line to stderr and fall back to printing directly — never lose output.
- No unit test may exercise the real `cli.Pager` win-path (spawning a real pager attached to the real terminal) — that would hang `go test` run interactively. Command-level tests stub `pagerFn`; `cli` package tests exercise the subprocess plumbing (`startPager`) with a stand-in command (`cat`), never a real pager.
- Follow existing repo conventions: `cmd.Flags().BoolVarP` for the flag (matches `event.go`'s `-f`/`--follow`), doc comments explaining *why* (this codebase's established style), `go build -trimpath` / `make test` before every commit.

---

## File Structure

- **Create** `internal/cli/pager.go` — `Pager`, `pagerArgv`, `startPager`. No cobra dependency (matches `internal/cli`'s current package boundary — it doesn't import cobra anywhere else).
- **Create** `internal/cli/pager_test.go` — unit tests for the three functions above.
- **Create** `internal/cmd/pager.go` — `pagerFn` (indirection var), `withPager`.
- **Create** `internal/cmd/pager_test.go` — unit tests for `withPager`, plus (added in later tasks) command-level flag-wiring tests for `show` and `task show`.
- **Modify** `internal/cmd/show.go` — add `--pager`/`-p` flag to `newShowCmd`, wire `withPager` into its `RunE`.
- **Modify** `internal/cmd/task.go` — add `--pager`/`-p` flag to `newTaskShowCmd`, wire `withPager` into its `RunE`.

---

### Task 1: `cli.Pager` — the pager subprocess plumbing

**Files:**
- Create: `internal/cli/pager.go`
- Create: `internal/cli/pager_test.go`

**Interfaces:**
- Produces: `func Pager(enabled bool) (io.Writer, func())` — the only symbol later tasks call. Returns `(nil, no-op-func)` whenever paging should not happen; otherwise the writer to send content to and a cleanup func to defer.
- Internal (not called outside this file, but named here since Task 1's tests exercise them directly): `func pagerArgv() []string`, `func startPager(argv []string, pagerOut, pagerErr io.Writer) (io.Writer, func(), error)`.

- [ ] **Step 1: Write `internal/cli/pager.go`**

```go
package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Pager starts an external pager when enabled and os.Stdout is an
// interactive terminal, and returns the writer command output should be
// sent to plus a cleanup func to defer once rendering finishes.
//
// Off-TTY (piped, redirected, or otherwise non-interactive) --pager is a
// silent no-op: piping rendered content into `less` when nothing is
// watching the terminal makes no sense, and scripts must never end up
// blocking on a pager waiting for a keypress that will never come.
//
// If stdout IS a terminal but the chosen pager can't actually be started
// (nothing in $PAGER or "less" resolves on $PATH), Pager prints one note to
// stderr and returns (nil, no-op) so the caller falls back to printing
// directly rather than losing the output.
//
// Callers must treat a nil writer as "use my existing output unchanged."
func Pager(enabled bool) (io.Writer, func()) {
	if !enabled {
		return nil, func() {}
	}
	if _, isTTY := terminalFd(os.Stdout); !isTTY {
		return nil, func() {}
	}
	w, cleanup, err := startPager(pagerArgv(), os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lode: pager unavailable (%v); printing directly\n", err)
		return nil, func() {}
	}
	return w, cleanup
}

// pagerArgv is the pager command line: $PAGER's fields when set to a
// non-blank value, else "less -R". -R lets less pass through the ANSI color
// codes cli.Markdown's glamour rendering already emitted, instead of
// showing them as literal escape sequences.
func pagerArgv() []string {
	if p := strings.TrimSpace(os.Getenv("PAGER")); p != "" {
		if fields := strings.Fields(p); len(fields) > 0 {
			return fields
		}
	}
	return []string{"less", "-R"}
}

// startPager execs argv, wiring its stdin to the writer it returns and its
// stdout/stderr to pagerOut/pagerErr. Split out of Pager so tests can drive
// the process plumbing with a stand-in command (e.g. "cat") instead of a
// real pager and a real terminal.
//
// The returned writer is the child's stdin pipe directly (proc.StdinPipe),
// not an io.Pipe wrapping it: an io.Pipe's Write blocks until something
// reads the other end, so if the pager process exits early (the user
// presses 'q' before rendering finishes) a caller still writing to an
// io.Pipe would hang forever with no reader left. proc.StdinPipe is a real
// OS pipe with its own kernel buffer, so a write after the reader is gone
// instead returns EPIPE — and every caller in this codebase already
// discards Write's error return (fmt.Fprintf et al.), so that failure is
// silently swallowed exactly like every other write in these commands.
func startPager(argv []string, pagerOut, pagerErr io.Writer) (io.Writer, func(), error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return nil, nil, err
	}
	proc := exec.Command(path, argv[1:]...)
	proc.Stdout = pagerOut
	proc.Stderr = pagerErr
	stdin, err := proc.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := proc.Start(); err != nil {
		return nil, nil, err
	}
	return stdin, func() {
		stdin.Close()
		_ = proc.Wait()
	}, nil
}
```

- [ ] **Step 2: Write `internal/cli/pager_test.go`**

```go
package cli

import (
	"bytes"
	"testing"
)

func TestPagerArgvDefaultsToLess(t *testing.T) {
	t.Setenv("PAGER", "")
	got := pagerArgv()
	if len(got) != 2 || got[0] != "less" || got[1] != "-R" {
		t.Fatalf("pagerArgv() = %v, want [less -R]", got)
	}
}

func TestPagerArgvUsesPagerEnv(t *testing.T) {
	t.Setenv("PAGER", "moar -x  -f ")
	got := pagerArgv()
	want := []string{"moar", "-x", "-f"}
	if len(got) != len(want) {
		t.Fatalf("pagerArgv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pagerArgv() = %v, want %v", got, want)
		}
	}
}

func TestPagerArgvBlankPagerFallsBack(t *testing.T) {
	t.Setenv("PAGER", "   ")
	got := pagerArgv()
	if len(got) != 2 || got[0] != "less" {
		t.Fatalf("pagerArgv() = %v, want fallback to less", got)
	}
}

func TestPagerDisabledIsNoop(t *testing.T) {
	w, cleanup := Pager(false)
	if w != nil {
		t.Fatalf("Pager(false) writer = %v, want nil", w)
	}
	cleanup() // must not panic
}

func TestStartPagerPipesContentThrough(t *testing.T) {
	var out bytes.Buffer
	w, cleanup, err := startPager([]string{"cat"}, &out, &out)
	if err != nil {
		t.Fatalf("startPager(cat): %v", err)
	}
	if _, err := w.Write([]byte("hello, pager\n")); err != nil {
		t.Fatalf("write to pager stdin: %v", err)
	}
	cleanup()
	if got := out.String(); got != "hello, pager\n" {
		t.Fatalf("pager output = %q, want %q", got, "hello, pager\n")
	}
}

func TestStartPagerUnknownCommandErrors(t *testing.T) {
	_, _, err := startPager([]string{"lode-pager-that-does-not-exist"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("startPager with an unknown command should return an error")
	}
}
```

- [ ] **Step 3: Run the new tests**

Run: `go test -trimpath ./internal/cli/... -run TestPager -v` and `go test -trimpath ./internal/cli/... -run TestStartPager -v`
Expected: all PASS. (`cat` must be on `$PATH`; it is on every dev/CI machine this repo targets — macOS and Linux.)

- [ ] **Step 4: Run the full `cli` package test suite to check for regressions**

Run: `go test -trimpath ./internal/cli/...`
Expected: PASS (no change to existing behavior — `pager.go` is new code, nothing existing was touched).

- [ ] **Step 5: Commit**

```bash
git add internal/cli/pager.go internal/cli/pager_test.go docs/superpowers/plans/2026-08-19-show-pager-flag.md
git commit -m "cli: add external-pager subprocess plumbing"
```

---

### Task 2: `internal/cmd` pager wiring helper

**Files:**
- Create: `internal/cmd/pager.go`
- Create: `internal/cmd/pager_test.go`

**Interfaces:**
- Consumes: `cli.Pager(enabled bool) (io.Writer, func())` (Task 1).
- Produces: `var pagerFn func(bool) (io.Writer, func())` (package var, defaults to `cli.Pager`, reassignable by tests) and `func withPager(cmd *cobra.Command, requested bool) func()` — later tasks call this at the top of `show`'s and `task show`'s `RunE`.

- [ ] **Step 1: Write `internal/cmd/pager.go`**

```go
package cmd

import (
	"github.com/spf13/cobra"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// pagerFn is cli.Pager, indirected through a package var so tests can stub
// it out. cli.Pager touches the real os.Stdout and can exec a real external
// process — a unit test must never do either: run from an interactive
// terminal, `go test` would otherwise block on a real `less` waiting for a
// 'q' keypress that never comes.
var pagerFn = cli.Pager

// withPager wires cmd's output through a pager when requested, returning a
// cleanup func the caller must defer. A no-op — cmd is left completely
// unchanged — whenever pagerFn declines to page (see cli.Pager: not
// requested, stdout isn't a terminal, or the pager couldn't start).
func withPager(cmd *cobra.Command, requested bool) func() {
	w, cleanup := pagerFn(requested)
	if w == nil {
		return func() {}
	}
	original := cmd.OutOrStdout()
	cmd.SetOut(w)
	return func() {
		cmd.SetOut(original)
		cleanup()
	}
}
```

- [ ] **Step 2: Write `internal/cmd/pager_test.go`**

```go
package cmd

import (
	"bytes"
	"io"
	"testing"

	"github.com/spf13/cobra"
)

func TestWithPagerNoopWhenDeclined(t *testing.T) {
	old := pagerFn
	pagerFn = func(bool) (io.Writer, func()) { return nil, func() {} }
	t.Cleanup(func() { pagerFn = old })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	cleanup := withPager(cmd, true)
	defer cleanup()

	if cmd.OutOrStdout() != io.Writer(&buf) {
		t.Fatal("withPager swapped cmd's output when pagerFn declined")
	}
}

func TestWithPagerSwapsAndRestoresOutput(t *testing.T) {
	old := pagerFn
	var pagerBuf bytes.Buffer
	called := false
	pagerFn = func(requested bool) (io.Writer, func()) {
		if !requested {
			t.Fatal("pagerFn called with requested=false")
		}
		return &pagerBuf, func() { called = true }
	}
	t.Cleanup(func() { pagerFn = old })

	var original bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&original)

	cleanup := withPager(cmd, true)
	if _, err := cmd.OutOrStdout().Write([]byte("paged\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	cleanup()

	if pagerBuf.String() != "paged\n" {
		t.Fatalf("pager buffer = %q, want %q", pagerBuf.String(), "paged\n")
	}
	if original.Len() != 0 {
		t.Fatalf("original buffer should be untouched, got %q", original.String())
	}
	if cmd.OutOrStdout() != io.Writer(&original) {
		t.Fatal("withPager did not restore the original output after cleanup")
	}
	if !called {
		t.Fatal("withPager's cleanup did not call the underlying pagerFn cleanup")
	}
}
```

- [ ] **Step 3: Run the new tests**

Run: `go test -trimpath ./internal/cmd/... -run TestWithPager -v`
Expected: both PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/pager.go internal/cmd/pager_test.go
git commit -m "cmd: add withPager helper, stubbable for tests"
```

---

### Task 3: Wire `--pager`/`-p` into `lode show`

**Files:**
- Modify: `internal/cmd/show.go:106-181` (`newShowCmd`)
- Modify: `internal/cmd/pager_test.go` (append)

**Interfaces:**
- Consumes: `withPager(cmd *cobra.Command, requested bool) func()` (Task 2), `pagerFn` package var (Task 2, for test stubbing).

- [ ] **Step 1: Add the flag var and registration to `newShowCmd`**

In `internal/cmd/show.go`, change the var declaration on line 107 from:

```go
	var kind, taskFlag, specFlag, adrFlag, planFlag, milestoneFlag, projectFlag, deliverableFlag, section string
```

to:

```go
	var kind, taskFlag, specFlag, adrFlag, planFlag, milestoneFlag, projectFlag, deliverableFlag, section string
	var pager bool
```

- [ ] **Step 2: Wire `withPager` into `RunE`**

In the same file, the `RunE` function currently starts (line 126):

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			flagValues := map[string]string{
```

Change it to:

```go
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupPager := withPager(cmd, pager)
			defer cleanupPager()

			flagValues := map[string]string{
```

- [ ] **Step 3: Register the flag**

After line 179 (`cmd.Flags().StringVarP(&section, "section", "s", "", ...)`), add:

```go
	cmd.Flags().BoolVarP(&pager, "pager", "p", false, "page output through $PAGER (falls back to less -R) when connected to a terminal")
```

- [ ] **Step 4: Append flag-wiring tests to `internal/cmd/pager_test.go`**

Add these imports to the file's import block (added to the ones from Task 2 — the block becomes `bytes`, `io`, `strings`, `testing`, `github.com/spf13/cobra`):

```go
	"strings"
```

Append:

```go
func TestShowPagerFlagWiresOutputThroughPager(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Paged output")
	setupRepoConfig(t, "proj")

	old := pagerFn
	var pagerBuf bytes.Buffer
	pagerFn = func(requested bool) (io.Writer, func()) {
		if !requested {
			t.Fatal("pagerFn called with requested=false; --pager was passed")
		}
		return &pagerBuf, func() {}
	}
	t.Cleanup(func() { pagerFn = old })

	out, err := runLode(t, "show", task.ID, "--pager")
	if err != nil {
		t.Fatalf("lode show %s --pager: %v\noutput: %s", task.ID, err, out)
	}
	if out != "" {
		t.Fatalf("stdout should be empty once paged, got %q", out)
	}
	if !strings.Contains(pagerBuf.String(), task.ID) {
		t.Fatalf("pager buffer = %q; want it to contain the task id", pagerBuf.String())
	}
}

func TestShowPagerFlagDeclinedLeavesOutputOnStdout(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Unpaged output")
	setupRepoConfig(t, "proj")

	old := pagerFn
	pagerFn = func(bool) (io.Writer, func()) { return nil, func() {} }
	t.Cleanup(func() { pagerFn = old })

	out, err := runLode(t, "show", task.ID, "--pager")
	if err != nil {
		t.Fatalf("lode show %s --pager: %v\noutput: %s", task.ID, err, out)
	}
	if !strings.Contains(out, task.ID) {
		t.Fatalf("stdout = %q; want it to contain the task id when pagerFn declines", out)
	}
}
```

- [ ] **Step 5: Run the new tests**

Run: `go test -trimpath ./internal/cmd/... -run TestShowPagerFlag -v`
Expected: both PASS. (Needs a reachable Postgres per this repo's store-test convention — `TEST_POSTGRES_DSN` or the default local DSN; skips silently otherwise unless `CI` is set.)

- [ ] **Step 6: Run the full `show` test suite to check for regressions**

Run: `go test -trimpath ./internal/cmd/... -run TestShow`
Expected: PASS — no existing `show` behavior changed (only a new flag and an `RunE` prologue that no-ops when `--pager` isn't passed).

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/show.go internal/cmd/pager_test.go
git commit -m "cmd: add --pager/-p to lode show"
```

---

### Task 4: Wire `--pager`/`-p` into `lode task show`

**Files:**
- Modify: `internal/cmd/task.go:202-212` (`newTaskShowCmd`)
- Modify: `internal/cmd/pager_test.go` (append)

**Interfaces:**
- Consumes: `withPager` (Task 2), `pagerFn` (Task 2).

- [ ] **Step 1: Add the flag and wire `withPager` into `newTaskShowCmd`**

In `internal/cmd/task.go`, replace:

```go
func newTaskShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task's details: body, edges, blocked status, and lease holder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTaskShow(cmd, args[0])
		},
	}
	return cmd
}
```

with:

```go
func newTaskShowCmd() *cobra.Command {
	var pager bool
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show a task's details: body, edges, blocked status, and lease holder",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cleanupPager := withPager(cmd, pager)
			defer cleanupPager()
			return runTaskShow(cmd, args[0])
		},
	}
	cmd.Flags().BoolVarP(&pager, "pager", "p", false, "page output through $PAGER (falls back to less -R) when connected to a terminal")
	return cmd
}
```

- [ ] **Step 2: Append a flag-wiring test to `internal/cmd/pager_test.go`**

```go
func TestTaskShowPagerFlagWiresOutputThroughPager(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Paged task show output")
	setupRepoConfig(t, "proj")

	old := pagerFn
	var pagerBuf bytes.Buffer
	pagerFn = func(requested bool) (io.Writer, func()) {
		if !requested {
			t.Fatal("pagerFn called with requested=false; --pager was passed")
		}
		return &pagerBuf, func() {}
	}
	t.Cleanup(func() { pagerFn = old })

	out, err := runLode(t, "task", "show", task.ID, "--pager")
	if err != nil {
		t.Fatalf("lode task show %s --pager: %v\noutput: %s", task.ID, err, out)
	}
	if out != "" {
		t.Fatalf("stdout should be empty once paged, got %q", out)
	}
	if !strings.Contains(pagerBuf.String(), task.ID) {
		t.Fatalf("pager buffer = %q; want it to contain the task id", pagerBuf.String())
	}
}
```

- [ ] **Step 3: Run the new test**

Run: `go test -trimpath ./internal/cmd/... -run TestTaskShowPagerFlag -v`
Expected: PASS.

- [ ] **Step 4: Run the full test suite**

Run: `make test`
Expected: PASS. This is the first full-suite run since Task 1 — it catches any cross-package regression (e.g. `go vet` issues from unused imports) the per-task runs didn't.

- [ ] **Step 5: Manual smoke test (not automatable — needs a real terminal)**

Build and run against a real terminal session to confirm the end-to-end subprocess wiring actually works interactively (the unit tests deliberately never exercise this path — see Global Constraints):

```bash
make build
./bin/lode show <some-task-id> --pager   # confirm it opens in $PAGER/less, shows the task, `q` quits cleanly
./bin/lode task show <some-task-id> -p   # same, short flag
./bin/lode show <some-spec-id> --pager   # doc-show path (lode show <SPEC id>)
./bin/lode show <some-task-id> --pager | cat   # non-TTY stdout: should print directly, no pager spawned
```

Expected: all four page (or print directly for the last, piped case) with no hang, no truncated output, and `q` exits the pager back to the shell promptly.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/task.go internal/cmd/pager_test.go
git commit -m "cmd: add --pager/-p to lode task show"
```
