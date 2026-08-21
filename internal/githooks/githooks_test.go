package githooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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

// chainFor returns what the named hook chains to in an Install
// result, failing the test if the hook is missing from it.
func chainFor(t *testing.T, chains []Chain, hook string) string {
	t.Helper()
	for _, c := range chains {
		if c.Hook == hook {
			return c.ChainedTo
		}
	}
	t.Fatalf("no %s in install result %+v", hook, chains)
	return ""
}

// actionFor is chainFor for an Uninstall result.
func actionFor(t *testing.T, removals []Removal, hook string) string {
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

func TestInstallFreshInstall(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains, err := Install(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if chainedTo != "" {
		t.Fatalf("chainedTo = %q, want empty (nothing to chain)", chainedTo)
	}

	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	content := readFile(t, preCommitPath)
	if !strings.Contains(content, Marker) {
		t.Fatalf("pre-commit missing marker %q: %q", Marker, content)
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

func TestInstallIdempotent(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains1, err := Install(root)
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	first := readFile(t, preCommitPath)

	_, chains2, err := Install(root)
	if err != nil {
		t.Fatalf("second Install: %v", err)
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

func TestInstallPreservesThirdPartyHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\necho distinctive-third-party-hook\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}

	_, chains, err := Install(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("Install: %v", err)
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
	_, chains2, err := Install(root)
	if err != nil {
		t.Fatalf("second Install: %v", err)
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

func TestInstallChainsToPreCommitFramework(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o644); err != nil {
		t.Fatalf("write .pre-commit-config.yaml: %v", err)
	}

	hooksDir, chains, err := Install(root)
	chainedTo := chainFor(t, chains, "pre-commit")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if chainedTo != "pre-commit" {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, "pre-commit")
	}
	content := readFile(t, filepath.Join(hooksDir, "pre-commit"))
	if !strings.Contains(content, `--next 'pre-commit' "$@"`) {
		t.Fatalf("pre-commit = %q, want it to chain --next 'pre-commit'", content)
	}

	// Re-run stays converged (still chains to the framework, no accumulation).
	_, chains2, err := Install(root)
	if err != nil {
		t.Fatalf("second Install: %v", err)
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

func TestInstallHonorsCoreHooksPath(t *testing.T) {
	root := initGitRepo(t)
	custom := filepath.Join(root, "custom-hooks")
	if out, err := exec.Command("git", "-C", root, "config", "core.hooksPath", custom).CombinedOutput(); err != nil {
		t.Fatalf("git config core.hooksPath: %v\n%s", err, out)
	}

	hooksDir, _, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if hooksDir != custom {
		t.Fatalf("hooksDir = %q, want %q", hooksDir, custom)
	}
	if !fileExists(filepath.Join(custom, "pre-commit")) {
		t.Fatalf("pre-commit not written into custom hooks dir %s", custom)
	}
}

// initGitRepo inits a git repo in a temp directory of its own and returns its
// path as git resolves it.
func initGitRepo(t *testing.T) string {
	t.Helper()
	return initGitRepoInDir(t, t.TempDir())
}

// initGitRepoInDir inits a git repo at the given dir and returns its path
// resolved to git's own toplevel (macOS /var symlink). Unlike initGitRepo it
// lets the caller choose the directory, so a test can put the repo under a
// path containing a space. No commit is made: nothing here needs history,
// only a repo git can resolve a hooks directory for.
func initGitRepoInDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("rev-parse toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// --- chain target with spaces is quoted -------------------------------------

// TestInstallQuotesChainTargetWithSpaces: an unquoted target path with a space
// is word-split by /bin/sh before lode sees it, silently dropping the chained
// hook. That the quoted form actually runs under git is covered end to end by
// internal/cmd's hook-script test, which has the built binary to run.
func TestInstallQuotesChainTargetWithSpaces(t *testing.T) {
	// A repo path containing a space (common on macOS).
	root := initGitRepoInDir(t, filepath.Join(t.TempDir(), "My Repo"))

	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\necho third-party\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}

	_, chains, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")
	if chainedTo := chainFor(t, chains, "pre-commit"); chainedTo != preLodePath {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, preLodePath)
	}
	content := readFile(t, preCommitPath)
	wantLine := "--next '" + preLodePath + "' \"$@\""
	if !strings.Contains(content, wantLine) {
		t.Fatalf("pre-commit = %q, want it to contain %q", content, wantLine)
	}
}

// --- ISSUE 2: refuse to clobber an unrecognized hook beside .pre-lode --------

func TestInstallRefusesUnrecognizedHookBesidePreLode(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
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
	if _, _, err := Install(root); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")

	// Another tool overwrites pre-commit with a NEW unrecognized hook.
	newHook := "#!/bin/sh\necho a-different-tool\n"
	if err := os.WriteFile(preCommitPath, []byte(newHook), 0o755); err != nil {
		t.Fatalf("overwrite pre-commit: %v", err)
	}

	// Re-running must refuse rather than silently drop newHook.
	_, _, err = Install(root)
	if err == nil {
		t.Fatalf("Install: err = nil, want a refusal error")
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

func TestUninstallRemovesOurHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, _, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	gotDir, removals, err := Uninstall(root)
	action := actionFor(t, removals, "pre-commit")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if gotDir != hooksDir {
		t.Fatalf("hooksDir = %q, want %q", gotDir, hooksDir)
	}
	if action != ActionRemoved {
		t.Fatalf("action = %q, want %q", action, ActionRemoved)
	}
	if fileExists(filepath.Join(hooksDir, "pre-commit")) {
		t.Fatal("pre-commit still present after uninstall")
	}

	// Re-running on an already-clean repo is a no-op, not an error.
	_, removals2, err := Uninstall(root)
	if err != nil {
		t.Fatalf("second Uninstall: %v", err)
	}
	action2 := actionFor(t, removals2, "pre-commit")
	if action2 != ActionNone {
		t.Fatalf("second run action = %q, want %q", action2, ActionNone)
	}
}

func TestUninstallRestoresPreservedHook(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	thirdParty := "#!/bin/sh\necho third-party\n"
	if err := os.WriteFile(preCommitPath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}
	if _, _, err := Install(root); err != nil {
		t.Fatalf("Install: %v", err)
	}

	_, removals, err := Uninstall(root)
	action := actionFor(t, removals, "pre-commit")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if action != ActionRestored {
		t.Fatalf("action = %q, want %q", action, ActionRestored)
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

func TestUninstallLeavesForeignHookAlone(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	preCommitPath := filepath.Join(hooksDir, "pre-commit")
	foreign := "#!/bin/sh\necho not ours\n"
	if err := os.WriteFile(preCommitPath, []byte(foreign), 0o755); err != nil {
		t.Fatalf("write foreign pre-commit: %v", err)
	}

	_, removals, err := Uninstall(root)
	action := actionFor(t, removals, "pre-commit")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if action != ActionNone {
		t.Fatalf("action = %q, want %q", action, ActionNone)
	}
	if got := readFile(t, preCommitPath); got != foreign {
		t.Fatalf("foreign pre-commit was modified: %q", got)
	}
}

// --- the merge-reporting hooks ------------------------------------------------

// TestInstallInstallsEveryManagedHook: every hook in Managed is a
// signal the backbone would otherwise miss — a lease that stops being renewed,
// a merge nobody reports, a commit with no task on it — so install must write
// all of them and uninstall must take all of them away. Derived from Managed
// rather than restated, so adding one cannot leave this test behind.
func TestInstallInstallsEveryManagedHook(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, chains, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	for _, h := range Managed {
		hook := h.Name
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

	_, removals, err := Uninstall(root)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, h := range Managed {
		hook := h.Name
		if got := actionFor(t, removals, hook); got != ActionRemoved {
			t.Fatalf("%s uninstall action = %q, want %q", hook, got, ActionRemoved)
		}
		if fileExists(filepath.Join(hooksDir, hook)) {
			t.Fatalf("%s still present after uninstall", hook)
		}
	}
}

// TestInstallFrameworkChainIsPreCommitOnly: running the pre-commit
// binary bare executes its pre-commit stage, which is the wrong thing to fire
// from post-merge or post-commit.
func TestInstallFrameworkChainIsPreCommitOnly(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".pre-commit-config.yaml"), []byte("repos: []\n"), 0o644); err != nil {
		t.Fatalf("write .pre-commit-config.yaml: %v", err)
	}

	_, chains, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
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

// TestInstallPreservesThirdPartyPostMerge: the preserve-and-chain
// contract is per hook, not pre-commit's alone.
func TestInstallPreservesThirdPartyPostMerge(t *testing.T) {
	root := initGitRepo(t)
	hooksDir, err := Dir(root)
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	postMergePath := filepath.Join(hooksDir, "post-merge")
	thirdParty := "#!/bin/sh\necho distinctive-post-merge\n"
	if err := os.WriteFile(postMergePath, []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party post-merge: %v", err)
	}

	_, chains, err := Install(root)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	preLodePath := postMergePath + ".pre-lode"
	if got := chainFor(t, chains, "post-merge"); got != preLodePath {
		t.Fatalf("post-merge chainedTo = %q, want %q", got, preLodePath)
	}
	if got := readFile(t, preLodePath); got != thirdParty {
		t.Fatalf("post-merge.pre-lode = %q, want the original %q", got, thirdParty)
	}

	// And uninstall puts it back.
	_, removals, err := Uninstall(root)
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if got := actionFor(t, removals, "post-merge"); got != ActionRestored {
		t.Fatalf("post-merge uninstall action = %q, want %q", got, ActionRestored)
	}
	if got := readFile(t, postMergePath); got != thirdParty {
		t.Fatalf("post-merge after uninstall = %q, want the original %q", got, thirdParty)
	}
}

// TestChainedArgsHookForwardsGitArgsBothWays: `lode hook` reads the words
// between the event and --next as its own and hands everything after --next to
// the chained hook verbatim. An args hook chained to a third-party hook has to
// name "$@" twice, or exactly one of the two sees git's arguments — and for
// commit-msg those arguments are the message file it exists to edit.
func TestChainedArgsHookForwardsGitArgsBothWays(t *testing.T) {
	for _, h := range Managed {
		script := renderScript(h, "/path/to/other-hook")
		got := strings.Count(script, `"$@"`)
		want := 1
		if h.Args {
			want = 2
		}
		if got != want {
			t.Errorf("chained %s script has %d \"$@\" (want %d):\n%s", h.Name, got, want, script)
		}
		// Unchained, the trailing "$@" already reaches the handler, so an args
		// hook needs no second copy.
		if n := strings.Count(renderScript(h, ""), `"$@"`); n != 1 {
			t.Errorf("unchained %s script has %d \"$@\" (want 1)", h.Name, n)
		}
	}
}

// TestCommitMsgHookIsNotFrameworkChained: running the pre-commit binary bare
// executes its pre-commit stage, which is not what commit-msg should fire.
func TestCommitMsgHookIsNotFrameworkChained(t *testing.T) {
	for _, h := range Managed {
		if h.Name == "commit-msg" && h.Framework {
			t.Fatal("commit-msg must not chain to the pre-commit framework")
		}
	}
}

// --- Installed ----------------------------------------------------------------

// TestInstalled: `lode doctor` asks this rather than re-deriving "is our hook
// there" from Marker, so it has to answer all three states — no repo, repo
// without our hooks, repo with them — and keep a foreign pre-commit false.
func TestInstalled(t *testing.T) {
	root := initGitRepo(t)

	hooksDir, installed, err := Installed(root)
	if err != nil {
		t.Fatalf("Installed before install: %v", err)
	}
	if installed {
		t.Fatal("Installed = true before any install")
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	foreign := "#!/bin/sh\necho not ours\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(foreign), 0o755); err != nil {
		t.Fatalf("write foreign pre-commit: %v", err)
	}
	if _, installed, err := Installed(root); err != nil || installed {
		t.Fatalf("Installed with a foreign pre-commit = %v (err %v), want false", installed, err)
	}

	if _, _, err := Install(root); err != nil {
		t.Fatalf("Install: %v", err)
	}
	gotDir, installed, err := Installed(root)
	if err != nil {
		t.Fatalf("Installed after install: %v", err)
	}
	if !installed {
		t.Fatal("Installed = false after a successful install")
	}
	if gotDir != hooksDir {
		t.Fatalf("hooksDir = %q, want %q", gotDir, hooksDir)
	}

	// Outside a git repo it is an error, which is what lets doctor report
	// "not in a git repository" rather than "hook missing".
	if _, _, err := Installed(t.TempDir()); err == nil {
		t.Fatal("Installed outside a git repo: err = nil, want a resolve failure")
	}
}
