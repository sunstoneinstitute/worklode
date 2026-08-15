package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// repoRoot returns this module's root, derived from this test file's own
// location (internal/cmd/githooks_test.go) so it works regardless of
// `go test`'s working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..")
}

// lodeBinary caches the built CLI for the whole package run. Go caches
// compilation but never the link step, so each build costs seconds — and
// every test here only execs the binary, so one copy serves them all.
var lodeBinary struct {
	once sync.Once
	path string
	err  error
	out  []byte
}

// buildLodeBinary builds the lode CLI (cmd/lode) once per package run and
// returns the path to the binary. Debug symbols are stripped: nothing here
// debugs the child, and stripping cuts the link time several-fold.
func buildLodeBinary(t *testing.T) string {
	t.Helper()
	lodeBinary.once.Do(func() {
		dir, err := os.MkdirTemp("", "lode-bin")
		if err != nil {
			lodeBinary.err = err
			return
		}
		bin := filepath.Join(dir, "lode")
		build := exec.Command("go", "build", "-ldflags=-s -w", "-o", bin, "./cmd/lode")
		build.Dir = repoRoot(t)
		lodeBinary.out, lodeBinary.err = build.CombinedOutput()
		if lodeBinary.err == nil {
			lodeBinary.path = bin
		}
	})
	if lodeBinary.err != nil {
		t.Fatalf("go build lode: %v\n%s", lodeBinary.err, lodeBinary.out)
	}
	return lodeBinary.path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}

// chainFor returns what the named hook chains to in an installGitHooks
// result, failing the test if the hook is missing from it.
func chainFor(t *testing.T, chains []hookChain, hook string) string {
	t.Helper()
	for _, c := range chains {
		if c.Hook == hook {
			return c.ChainedTo
		}
	}
	t.Fatalf("no %s in install result %+v", hook, chains)
	return ""
}

// actionFor is chainFor for an uninstallGitHooks result.
func actionFor(t *testing.T, removals []hookRemoval, hook string) string {
	t.Helper()
	for _, r := range removals {
		if r.Hook == hook {
			return r.Action
		}
	}
	t.Fatalf("no %s in uninstall result %+v", hook, removals)
	return ""
}

// --- fresh install -----------------------------------------------------

func TestInstallGitHooksFreshInstall(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains, err := installGitHooks(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if chainedTo != "" {
		t.Fatalf("chainedTo = %q, want empty (nothing to chain)", chainedTo)
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	content := readFile(t, preCommitPath)
	if !strings.Contains(content, hookMarker) {
		t.Fatalf("pre-commit missing marker %q: %q", hookMarker, content)
	}
	want := "#!/bin/sh\n# worklode-hook v1 — installed by `lode install`; do not edit.\nexec lode hook pre-commit \"$@\"\n"
	if content != want {
		t.Fatalf("pre-commit content = %q, want %q", content, want)
	}

	if mode := fileMode(t, preCommitPath); mode.Perm() != 0o755 {
		t.Fatalf("pre-commit mode = %v, want 0755", mode.Perm())
	}

	if fileExists(filepath.Join(hooksDir, "pre-commit.pre-lode")) {
		t.Fatalf("pre-commit.pre-lode should not exist on a fresh install")
	}
}

// --- idempotent ----------------------------------------------------------

func TestInstallGitHooksIdempotent(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains1, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("first installGitHooks: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	first := readFile(t, preCommitPath)

	_, chains2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
	second := readFile(t, preCommitPath)

	if first != second {
		t.Fatalf("pre-commit changed across re-runs:\nfirst:  %q\nsecond: %q", first, second)
	}
	chainedTo1, chainedTo2 := chainFor(t, chains1, "pre-commit"), chainFor(t, chains2, "pre-commit")
	if chainedTo1 != chainedTo2 {
		t.Fatalf("chainedTo changed across re-runs: %q -> %q", chainedTo1, chainedTo2)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit.pre-lode")) {
		t.Fatalf("re-running a fresh install must not create pre-commit.pre-lode")
	}
}

// --- existing third-party hook preserved ----------------------------------

func TestInstallGitHooksPreservesThirdPartyHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\necho distinctive-third-party-hook\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}

	_, chains, err := installGitHooks(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")
	if chainedTo != preLodePath {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, preLodePath)
	}
	if got := readFile(t, preLodePath); got != thirdParty {
		t.Fatalf("pre-commit.pre-lode = %q, want original third-party content %q", got, thirdParty)
	}
	newContent := readFile(t, preCommitPath)
	wantNext := "--next '" + preLodePath + "' \"$@\""
	if !strings.Contains(newContent, wantNext) {
		t.Fatalf("pre-commit = %q, want it to contain %q", newContent, wantNext)
	}

	// Re-run: must not re-rename or clobber the preserved original.
	_, chains2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
	chainedTo2 := chainFor(t, chains2, "pre-commit")
	if chainedTo2 != preLodePath {
		t.Fatalf("second run chainedTo = %q, want %q", chainedTo2, preLodePath)
	}
	if got := readFile(t, preLodePath); got != thirdParty {
		t.Fatalf("pre-commit.pre-lode after re-run = %q, want unchanged original %q", got, thirdParty)
	}
	if got := readFile(t, preCommitPath); got != newContent {
		t.Fatalf("pre-commit changed across re-runs:\nfirst:  %q\nsecond: %q", newContent, got)
	}
}

// --- .pre-commit-config.yaml present ---------------------------------------

func TestInstallGitHooksChainsToPreCommitFramework(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o644); err != nil {
		t.Fatalf("write .pre-commit-config.yaml: %v", err)
	}

	hooksDir, chains, err := installGitHooks(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if chainedTo != "pre-commit" {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, "pre-commit")
	}
	content := readFile(t, filepath.Join(hooksDir, "pre-commit"))
	if !strings.Contains(content, `--next 'pre-commit' "$@"`) {
		t.Fatalf("pre-commit = %q, want it to chain --next 'pre-commit'", content)
	}

	// Re-run stays converged (still chains to the framework, no accumulation).
	_, chains2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
	chainedTo2 := chainFor(t, chains2, "pre-commit")
	if chainedTo2 != "pre-commit" {
		t.Fatalf("second run chainedTo = %q, want %q", chainedTo2, "pre-commit")
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit.pre-lode")) {
		t.Fatalf("chaining to the pre-commit framework must not create pre-commit.pre-lode")
	}
}

// --- honors core.hooksPath ---------------------------------------------------

func TestInstallGitHooksHonorsCoreHooksPath(t *testing.T) {
	root := initGitRepo(t)
	custom := filepath.Join(root, "custom-hooks")
	if out, err := exec.Command("git", "-C", root, "config", "core.hooksPath", custom).CombinedOutput(); err != nil {
		t.Fatalf("git config core.hooksPath: %v\n%s", err, out)
	}

	hooksDir, _, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if hooksDir != custom {
		t.Fatalf("hooksDir = %q, want %q", hooksDir, custom)
	}
	if !fileExists(filepath.Join(custom, "pre-commit")) {
		t.Fatalf("pre-commit not written into custom hooks dir %s", custom)
	}
}

// --- installed hook + guard NOP: `git commit` succeeds without a lode server -

func TestInstallGitHooksCommitSucceedsWithoutServer(t *testing.T) {
	bin := buildLodeBinary(t)
	root := initGitRepo(t)

	if _, _, err := installGitHooks(root); err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	add := exec.Command("git", "-C", root, "add", "file.txt")
	if out, err := add.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	commit := exec.Command("git", "-C", root, "-c", "commit.gpgsign=false", "commit", "-m", "add file.txt")
	commit.Env = append(os.Environ(),
		"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	// Guarantee no ambient config points the hook at a real backbone.
	commit.Env = append(commit.Env, "LODE_SERVER=", "LODE_TOKEN=")
	var out bytes.Buffer
	commit.Stdout = &out
	commit.Stderr = &out
	if err := commit.Run(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out.String())
	}
}

// initGitRepoInDir inits a git repo with one commit at the given dir and
// returns its path resolved to git's own toplevel (macOS /var symlink). Unlike
// initGitRepo it lets the caller choose the directory, so a test can put the
// repo under a path containing a space.
func initGitRepoInDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	run := func(args ...string) {
		t.Helper()
		// commit.gpgsign=false: the developer's global config may enable
		// signing, which a temp-repo test commit must not depend on.
		c := exec.Command("git", append([]string{"-c", "commit.gpgsign=false"}, args...)...)
		c.Dir = dir
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("rev-parse toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// --- ISSUE 1: chain target with spaces is quoted and still runs --------------

func TestInstallGitHooksQuotesChainTargetWithSpaces(t *testing.T) {
	bin := buildLodeBinary(t)
	// A repo path containing a space (common on macOS).
	root := initGitRepoInDir(t, filepath.Join(t.TempDir(), "My Repo"))

	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	// Seed a distinctive third-party hook that touches a sentinel when it runs.
	sentinel := filepath.Join(t.TempDir(), "third-party-ran")
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\ntouch '" + sentinel + "'\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}

	_, chains, err := installGitHooks(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")
	if chainedTo != preLodePath {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, preLodePath)
	}

	// The generated --next clause must single-quote the space-containing path.
	content := readFile(t, preCommitPath)
	wantLine := "--next '" + preLodePath + "' \"$@\""
	if !strings.Contains(content, wantLine) {
		t.Fatalf("pre-commit = %q, want it to contain %q", content, wantLine)
	}

	// And a real commit must actually run the preserved third-party hook.
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "file.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commit := exec.Command("git", "-C", root, "-c", "commit.gpgsign=false", "commit", "-m", "add file.txt")
	commit.Env = append(os.Environ(),
		"PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"LODE_SERVER=", "LODE_TOKEN=",
	)
	var out bytes.Buffer
	commit.Stdout = &out
	commit.Stderr = &out
	if err := commit.Run(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out.String())
	}
	if !fileExists(sentinel) {
		t.Fatalf("preserved third-party hook did not run (sentinel %s missing) — chain target was word-split", sentinel)
	}
}

// --- ISSUE 2: refuse to clobber an unrecognized hook beside .pre-lode --------

func TestInstallGitHooksRefusesUnrecognizedHookBesidePreLode(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")

	// Establish the preserved-third-party state: lode owns pre-commit, the
	// original hook lives in pre-commit.pre-lode.
	original := "#!/bin/sh\necho original-third-party\n"
	if err := os.WriteFile(preCommitPath, []byte(original), 0o755); err != nil {
		t.Fatalf("write original pre-commit: %v", err)
	}
	if _, _, err := installGitHooks(root); err != nil {
		t.Fatalf("first installGitHooks: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")

	// Another tool overwrites pre-commit with a NEW unrecognized hook.
	newHook := "#!/bin/sh\necho a-different-tool\n"
	if err := os.WriteFile(preCommitPath, []byte(newHook), 0o755); err != nil {
		t.Fatalf("overwrite pre-commit: %v", err)
	}

	// Re-running must refuse rather than silently drop newHook.
	_, _, err = installGitHooks(root)
	if err == nil {
		t.Fatalf("installGitHooks: err = nil, want a refusal error")
	}
	if !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("error = %q, want it to mention refusing to overwrite", err.Error())
	}
	// Neither file may have been touched.
	if got := readFile(t, preCommitPath); got != newHook {
		t.Fatalf("pre-commit was modified: %q, want %q", got, newHook)
	}
	if got := readFile(t, preLodePath); got != original {
		t.Fatalf("pre-commit.pre-lode was modified: %q, want %q", got, original)
	}
}

// --- uninstall ---------------------------------------------------------------

func TestUninstallGitHooksRemovesOurHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, _, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}

	gotDir, removals, err := uninstallGitHooks(root)
	action := actionFor(t, removals, "pre-commit")
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
	_, removals2, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("second uninstallGitHooks: %v", err)
	}
	action2 := actionFor(t, removals2, "pre-commit")
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

	_, removals, err := uninstallGitHooks(root)
	action := actionFor(t, removals, "pre-commit")
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

	_, removals, err := uninstallGitHooks(root)
	action := actionFor(t, removals, "pre-commit")
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

// --- the merge-reporting hooks ------------------------------------------------

// TestInstallGitHooksInstallsMergeHooks: post-merge and post-commit are what
// make a local merge visible to the backbone, so install must write them
// alongside pre-commit, and uninstall must take all three away.
func TestInstallGitHooksInstallsMergeHooks(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	for _, hook := range []string{"pre-commit", "post-merge", "post-commit"} {
		path := filepath.Join(hooksDir, hook)
		content := readFile(t, path)
		if !strings.Contains(content, "exec lode hook "+hook+" \"$@\"") {
			t.Fatalf("%s = %q, want it to invoke `lode hook %s`", hook, content, hook)
		}
		if mode := fileMode(t, path); mode.Perm() != 0o755 {
			t.Fatalf("%s mode = %v, want 0755", hook, mode.Perm())
		}
		if chainFor(t, chains, hook) != "" {
			t.Fatalf("%s chains to something on a fresh install", hook)
		}
	}

	_, removals, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("uninstallGitHooks: %v", err)
	}
	for _, hook := range []string{"pre-commit", "post-merge", "post-commit"} {
		if got := actionFor(t, removals, hook); got != hookActionRemoved {
			t.Fatalf("%s uninstall action = %q, want %q", hook, got, hookActionRemoved)
		}
		if fileExists(filepath.Join(hooksDir, hook)) {
			t.Fatalf("%s still present after uninstall", hook)
		}
	}
}

// TestInstallGitHooksFrameworkChainIsPreCommitOnly: running the pre-commit
// binary bare executes its pre-commit stage, which is the wrong thing to fire
// from post-merge or post-commit.
func TestInstallGitHooksFrameworkChainIsPreCommitOnly(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o644); err != nil {
		t.Fatalf("write .pre-commit-config.yaml: %v", err)
	}

	_, chains, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	if got := chainFor(t, chains, "pre-commit"); got != "pre-commit" {
		t.Fatalf("pre-commit chainedTo = %q, want the framework", got)
	}
	for _, hook := range []string{"post-merge", "post-commit"} {
		if got := chainFor(t, chains, hook); got != "" {
			t.Fatalf("%s chainedTo = %q, want no framework chain", hook, got)
		}
	}
}

// TestInstallGitHooksPreservesThirdPartyPostMerge: the preserve-and-chain
// contract is per hook, not pre-commit's alone.
func TestInstallGitHooksPreservesThirdPartyPostMerge(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := resolveHooksDir(root)
	if err != nil {
		t.Fatalf("resolveHooksDir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	postMergePath := filepath.Join(hooksDir, "post-merge")
	thirdParty := "#!/bin/sh\necho distinctive-post-merge\n"
	if err := os.WriteFile(postMergePath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party post-merge: %v", err)
	}

	_, chains, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("installGitHooks: %v", err)
	}
	preLodePath := postMergePath + ".pre-lode"
	if got := chainFor(t, chains, "post-merge"); got != preLodePath {
		t.Fatalf("post-merge chainedTo = %q, want %q", got, preLodePath)
	}
	if got := readFile(t, preLodePath); got != thirdParty {
		t.Fatalf("post-merge.pre-lode = %q, want the original %q", got, thirdParty)
	}

	// And uninstall puts it back.
	_, removals, err := uninstallGitHooks(root)
	if err != nil {
		t.Fatalf("uninstallGitHooks: %v", err)
	}
	if got := actionFor(t, removals, "post-merge"); got != hookActionRestored {
		t.Fatalf("post-merge uninstall action = %q, want %q", got, hookActionRestored)
	}
	if got := readFile(t, postMergePath); got != thirdParty {
		t.Fatalf("post-merge after uninstall = %q, want the original %q", got, thirdParty)
	}
}
