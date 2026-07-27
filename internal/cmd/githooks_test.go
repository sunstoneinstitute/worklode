package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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

// buildLodeBinary builds the lode CLI (cmd/lode) into a fresh temp dir and
// returns the path to the binary.
func buildLodeBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "lode")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lode")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build lode: %v\n%s", err, out)
	}
	return bin
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

// --- fresh install -----------------------------------------------------

func TestInstallGitHooksFreshInstall(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chainedTo, err := installGitHooks(root)
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

	hooksDir, chainedTo1, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("first installGitHooks: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	first := readFile(t, preCommitPath)

	_, chainedTo2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
	second := readFile(t, preCommitPath)

	if first != second {
		t.Fatalf("pre-commit changed across re-runs:\nfirst:  %q\nsecond: %q", first, second)
	}
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

	_, chainedTo, err := installGitHooks(root)
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
	_, chainedTo2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
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

	hooksDir, chainedTo, err := installGitHooks(root)
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
	_, chainedTo2, err := installGitHooks(root)
	if err != nil {
		t.Fatalf("second installGitHooks: %v", err)
	}
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

	commit := exec.Command("git", "-C", root, "commit", "-m", "add file.txt")
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
		c := exec.Command("git", args...)
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

	_, chainedTo, err := installGitHooks(root)
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
	commit := exec.Command("git", "-C", root, "commit", "-m", "add file.txt")
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
