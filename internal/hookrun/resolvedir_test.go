package hookrun

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveDirTrustsProcessCwdOverStalePWD is WL-68: a git hook (post-merge,
// post-commit, ...) sends no cwd in its payload, so resolveDir falls back to
// an environment signal. `git -C /other/repo merge` chdir()s the hook
// process into /other/repo, but $PWD is a shell-maintained variable no
// subprocess updates — it still names the shell's own directory. Preferring
// $PWD there means the handler probes the wrong repository (spec 008 §5.1
// says a hook resolves from its own cwd for exactly this reason).
func TestResolveDirTrustsProcessCwdOverStalePWD(t *testing.T) {
	shellDir := t.TempDir()   // where the invoking shell sits ($PWD, stale)
	actedOnDir := t.TempDir() // the repo `git -C` pointed at (process cwd)

	// t.Chdir also sets $PWD to actedOnDir for the test's duration (matching
	// what a real chdir(2) does — it does not touch PWD, but Go's testing
	// helper mirrors a shell's behavior). Set the stale value afterwards to
	// simulate the real gap: git's chdir() into the target repo, with the
	// invoking shell's $PWD left pointing elsewhere.
	t.Chdir(actedOnDir)
	t.Setenv("PWD", shellDir)

	want, err := filepath.EvalSymlinks(actedOnDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", actedOnDir, err)
	}

	got := resolveDir(Payload{})
	resolvedGot, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", got, err)
	}
	if resolvedGot != want {
		t.Fatalf("resolveDir() = %q, want the process cwd %q (not stale $PWD %q)", got, want, shellDir)
	}
}

// TestResolveDirStillPrefersPayloadCwd: a caller that legitimately supplies a
// cwd (every harness payload today) still wins over both the process cwd and
// $PWD — this is not a blanket "ignore $PWD", only a hook-with-no-payload
// fix.
func TestResolveDirStillPrefersPayloadCwd(t *testing.T) {
	t.Setenv("PWD", t.TempDir())
	t.Chdir(t.TempDir())

	want := t.TempDir()
	got := resolveDir(Payload{Cwd: want})
	if got != want {
		t.Fatalf("resolveDir() = %q, want payload cwd %q", got, want)
	}
}

// TestResolveDirFallsBackToPWDWhenGetwdFails: os.Getwd() can fail (e.g. the
// cwd was removed out from under the process); $PWD stays a reasonable
// last-resort fallback rather than returning an empty string.
func TestResolveDirFallsBackToPWDWhenGetwdFails(t *testing.T) {
	removed := t.TempDir()
	t.Chdir(removed)
	if err := os.RemoveAll(removed); err != nil {
		t.Fatalf("RemoveAll(%s): %v", removed, err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("os.Getwd() still succeeds after removing the cwd on this platform")
	}

	fallback := t.TempDir()
	t.Setenv("PWD", fallback)

	if got := resolveDir(Payload{}); got != fallback {
		t.Fatalf("resolveDir() = %q, want $PWD fallback %q when Getwd fails", got, fallback)
	}
}
