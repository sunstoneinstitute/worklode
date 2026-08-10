package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func TestNewLayoutRejects(t *testing.T) {
	for _, base := range []string{"/abs/path", "../escape", "a/../..", ".", "./"} {
		if _, err := worktree.NewLayout(base); err == nil {
			t.Errorf("NewLayout(%q) = nil error, want rejection", base)
		}
	}
}

// TestNewLayoutRejectsWhitespaceOnly pins that a whitespace-only base is
// trimmed and then rejected as empty, not accepted verbatim as a directory
// named "  ".
func TestNewLayoutRejectsWhitespaceOnly(t *testing.T) {
	if _, err := worktree.NewLayout("   "); err == nil {
		t.Fatal("NewLayout(\"   \") = nil error, want rejection")
	}
}

// TestNewLayoutRejectsDoubledSlash pins that an interior doubled slash is
// rejected via its own "empty segment" message, distinct from the "." / ".."
// message a doubled slash would otherwise be misreported under.
func TestNewLayoutRejectsDoubledSlash(t *testing.T) {
	_, err := worktree.NewLayout("a//b")
	if err == nil {
		t.Fatal(`NewLayout("a//b") = nil error, want rejection`)
	}
	if !strings.Contains(err.Error(), "empty segment") {
		t.Errorf(`NewLayout("a//b") error = %q, want it to describe an empty segment, not "." or ".."`, err.Error())
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
	// The layout is flat: a "/" from a namespaced template is flattened to
	// "-" rather than nesting a directory (spec 030 §3.1).
	if got, want := l.Dir("/repo", "team/WL-7-x"), "/repo/.worktrees/team-WL-7-x"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
	if got, want := l.Dir("/repo", "a/b/WL-7-x"), "/repo/.worktrees/a-b-WL-7-x"; got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

// TestLayoutDirRoundTripsParseDir is the property that keeps the two halves
// honest: whatever branch the server hands out, the directory Dir puts it in
// must clear the guard and yield the id back — including the flattened ones,
// which is what the flat layout could plausibly break.
func TestLayoutDirRoundTripsParseDir(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ branch, wantID string }{
		{"WL-7-fix-the-thing", "WL-7"},
		{"WL-7", "WL-7"},
		{"team/WL-7-fix-the-thing", "WL-7"},
		{"worklode/SW-1234-a-longer-slug", "SW-1234"},
	}
	for _, c := range cases {
		dir := l.Dir("/repo", c.branch)
		gotID, ok := l.ParseDir(dir)
		if !ok || gotID != c.wantID {
			t.Errorf("ParseDir(Dir(%q)) = (%q, %v), want (%q, true) [dir %q]", c.branch, gotID, ok, c.wantID, dir)
		}
	}
}

// TestLayoutDirZeroValuePanics pins that Dir refuses to silently compute a
// worktree path without the base directory (dropping it would put worktrees
// at the repo root) — the zero value must be loud, not quietly wrong.
func TestLayoutDirZeroValuePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Dir on zero Layout: want panic, got none")
		}
	}()
	var zero worktree.Layout
	zero.Dir("/repo", "WL-7-x")
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
		// The layout is flat (spec 030 §3.1): only a directory immediately
		// below the base is a worktree root. Anything deeper is a path INSIDE
		// one, which the hook guards reach only via worktree.Root — never as a
		// worktree root itself.
		{"nested is not a worktree root", def, "/repo/.worktrees/team/SW-12-slug", "", false},
		{"path inside a worktree", def, "/repo/.worktrees/WL-7-x/internal/store", "", false},
		{"flattened namespace", def, "/repo/.worktrees/team-SW-12-slug", "SW-12", true},
		{"id not at segment start", def, "/repo/.worktrees/worklode-WL-7-x", "WL-7", true},
		{"deep repo path", def, "/a/b/c/.worktrees/WL-1-x", "WL-1", true},
		{"trailing slash", def, "/repo/.worktrees/WL-7-x/", "WL-7", true},
		// The base segment appears twice with a different id below each: the
		// *last* occurrence must win (lastIndexOf's contract), so this must
		// yield WL-7, not the WL-9 sitting below the first occurrence.
		{"base repeated, last wins", def, "/repo/.worktrees/WL-9-a/.worktrees/WL-7-b", "WL-7", true},
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

// initGitRepo creates a fresh git repo in a temp dir, with one initial commit
// so HEAD is born (`git worktree add -b` requires this on git < 2.42, which
// doesn't auto-infer --orphan), and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// commit.gpgsign=false: the developer's global config may enable signing,
	// which a temp-repo test commit must not depend on.
	if out, err := exec.Command("git", "-C", dir, "-c", "commit.gpgsign=false",
		"-c", "user.email=test@example.com", "-c", "user.name=test",
		"commit", "--allow-empty", "-m", "init").CombinedOutput(); err != nil {
		t.Fatalf("git commit --allow-empty: %v\n%s", err, out)
	}
	return dir
}

func TestIdentity(t *testing.T) {
	dir := initGitRepo(t)

	got, err := worktree.Identity(dir)
	if err != nil {
		t.Fatalf("Identity: %v", err)
	}

	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	// Compare against git's own notion of the toplevel, not the raw TempDir
	// path: on macOS /tmp is a symlink and git resolves it.
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	want := host + ":" + strings.TrimSpace(string(out))
	if got != want {
		t.Fatalf("Identity = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, host+":") || !strings.Contains(got, string(os.PathSeparator)) {
		t.Fatalf("Identity = %q, want <hostname>:<abs path>", got)
	}
}

func TestIdentitySubdirectory(t *testing.T) {
	dir := initGitRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fromRoot, err := worktree.Identity(dir)
	if err != nil {
		t.Fatalf("Identity(root): %v", err)
	}
	fromSub, err := worktree.Identity(sub)
	if err != nil {
		t.Fatalf("Identity(subdir): %v", err)
	}
	if fromSub != fromRoot {
		t.Fatalf("Identity from subdir = %q, want the worktree root identity %q", fromSub, fromRoot)
	}
}

func TestIdentityOutsideGit(t *testing.T) {
	if _, err := worktree.Identity(t.TempDir()); err == nil {
		t.Fatalf("Identity outside a git worktree: err = nil, want error")
	}
}

func TestRoot(t *testing.T) {
	dir := initGitRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	want := strings.TrimSpace(string(out))

	rootFromRoot, ok := worktree.Root(dir)
	if !ok {
		t.Fatalf("Root(root): ok = false, want true")
	}
	if rootFromRoot != want {
		t.Fatalf("Root(root) = %q, want %q", rootFromRoot, want)
	}
	rootFromSub, ok := worktree.Root(sub)
	if !ok {
		t.Fatalf("Root(subdir): ok = false, want true")
	}
	if rootFromSub != want {
		t.Fatalf("Root(subdir) = %q, want %q", rootFromSub, want)
	}
}

func TestRootOutsideGit(t *testing.T) {
	if _, ok := worktree.Root(t.TempDir()); ok {
		t.Fatalf("Root outside a git worktree: ok = true, want false")
	}
}

func TestGitDir(t *testing.T) {
	dir := initGitRepo(t)
	root, ok := worktree.Root(dir)
	if !ok {
		t.Fatalf("Root: ok = false, want true")
	}
	gitDir, err := worktree.GitDir(root)
	if err != nil {
		t.Fatalf("GitDir: %v", err)
	}
	if !filepath.IsAbs(gitDir) {
		t.Fatalf("GitDir = %q, want an absolute path", gitDir)
	}
	if info, err := os.Stat(gitDir); err != nil || !info.IsDir() {
		t.Fatalf("GitDir = %q, want an existing directory (stat err %v)", gitDir, err)
	}
}

func TestEnableWorktreeConfigExtensionIdempotent(t *testing.T) {
	dir := initGitRepo(t)
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension (second run): %v", err)
	}
	out, err := exec.Command("git", "-C", dir, "config", "--get", "extensions.worktreeConfig").Output()
	if err != nil {
		t.Fatalf("git config --get extensions.worktreeConfig: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "true" {
		t.Fatalf("extensions.worktreeConfig = %q, want true", got)
	}
}

// TestEnableWorktreeConfigExtensionRefusesBareRepo covers the bare-clone +
// linked-worktree layout. Enabling extensions.worktreeConfig there without
// git's own core.bare/core.worktree migration breaks every linked worktree,
// so the function must refuse instead of writing the key.
func TestEnableWorktreeConfigExtensionRefusesBareRepo(t *testing.T) {
	src := initGitRepo(t)
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	if out, err := exec.Command("git", "clone", "--bare", src, bare).CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
	linked := filepath.Join(base, "ctl")
	if out, err := exec.Command("git", "-C", bare, "worktree", "add", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", linked, "rev-parse", "--show-toplevel").CombinedOutput(); err != nil {
		t.Fatalf("linked worktree broken before the test even started: %v\n%s", err, out)
	}

	if err := worktree.EnableWorktreeConfigExtension(bare); err == nil {
		t.Fatal("EnableWorktreeConfigExtension on a bare repo: err = nil, want a refusal")
	}

	// The guard is only worth anything if the repo is untouched afterwards.
	if out, err := exec.Command("git", "-C", bare, "config", "--get", "extensions.worktreeConfig").CombinedOutput(); err == nil {
		t.Fatalf("extensions.worktreeConfig = %q, want unset after a refusal", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("git", "-C", linked, "rev-parse", "--show-toplevel").CombinedOutput(); err != nil {
		t.Fatalf("linked worktree broken after the refusal: %v\n%s", err, out)
	}

	// Prove the hazard is real rather than hypothetical: writing the key the
	// way the unguarded version did breaks the very same linked worktree. If
	// this ever stops failing, git changed and the guard should be revisited.
	if out, err := exec.Command("git", "-C", bare, "config", "extensions.worktreeConfig", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config extensions.worktreeConfig true: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", linked, "rev-parse", "--show-toplevel").CombinedOutput(); err == nil {
		t.Fatalf("setting extensions.worktreeConfig on the bare repo left the linked worktree working (%s) — "+
			"the guard may no longer be needed on this git version", strings.TrimSpace(string(out)))
	}
}

func TestEnableWorktreeConfigExtensionRefusesCoreWorktreeSet(t *testing.T) {
	dir := initGitRepo(t)
	if out, err := exec.Command("git", "-C", dir, "config", "core.worktree", dir).CombinedOutput(); err != nil {
		t.Fatalf("git config core.worktree: %v\n%s", err, out)
	}
	if err := worktree.EnableWorktreeConfigExtension(dir); err == nil {
		t.Fatal("EnableWorktreeConfigExtension with core.worktree set: err = nil, want a refusal")
	}
	if out, err := exec.Command("git", "-C", dir, "config", "--get", "extensions.worktreeConfig").CombinedOutput(); err == nil {
		t.Fatalf("extensions.worktreeConfig = %q, want unset after a refusal", strings.TrimSpace(string(out)))
	}
}

// mustLayout builds the default (.worktrees) layout for a test.
func mustLayout(t *testing.T) worktree.Layout {
	t.Helper()
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	return l
}

// addWorktreeUnderBase creates <root>/.worktrees/<name> as a real linked
// worktree, which is what a stamped worklode.task-id needs: `git config
// --worktree` writes into the worktree's own config, and a bare directory has
// none.
func addWorktreeUnderBase(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, worktree.DefaultBase, name)
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-b", name, dir).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", dir, err, out)
	}
	return dir
}

func TestSetTaskIDAndTaskID(t *testing.T) {
	dir := initGitRepo(t)
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	wt := addWorktreeUnderBase(t, dir, "WL-9-fix-thing")
	if err := worktree.SetTaskID(wt, "WL-9"); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}
	gotID, ok := mustLayout(t).TaskID(wt)
	if !ok || gotID != "WL-9" {
		t.Fatalf("TaskID = (%q, %v), want (\"WL-9\", true)", gotID, ok)
	}
}

func TestTaskIDFallsBackToDirName(t *testing.T) {
	dir := initGitRepo(t)
	wt := filepath.Join(dir, worktree.DefaultBase, "WL-3-fix-thing")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gotID, ok := mustLayout(t).TaskID(wt)
	if !ok || gotID != "WL-3" {
		t.Fatalf("TaskID = (%q, %v), want (\"WL-3\", true) from the directory-name fallback", gotID, ok)
	}
}

// TestTaskIDExplicitWinsOverMismatchedDirName is the case the explicit field
// exists for: a worktree renamed after creation to something the id pattern
// no longer matches still resolves, because the stamp travels with the
// worktree rather than with its name.
func TestTaskIDExplicitWinsOverMismatchedDirName(t *testing.T) {
	dir := initGitRepo(t)
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	wt := addWorktreeUnderBase(t, dir, "WL-3-fix-thing")
	if err := worktree.SetTaskID(wt, "WL-99"); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}
	gotID, ok := mustLayout(t).TaskID(wt)
	if !ok || gotID != "WL-99" {
		t.Fatalf("TaskID = (%q, %v), want (\"WL-99\", true) — explicit config must win over the WL-3 directory name", gotID, ok)
	}
}

func TestTaskIDNeitherPresent(t *testing.T) {
	dir := initGitRepo(t)
	wt := filepath.Join(dir, worktree.DefaultBase, "no-id-here")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := mustLayout(t).TaskID(wt); ok {
		t.Fatalf("TaskID on an unstamped directory with no id in its name: ok = true, want false")
	}
}

// TestTaskIDKeepsTheGuardPure pins the §3.2 split: a path that fails the
// pure-string guard resolves to nothing even when it carries a stamped
// worklode.task-id. The guard decides whether Worklode acts at all; only after it
// passes may id resolution consult git config.
func TestTaskIDIgnoresStampOutsideTheBase(t *testing.T) {
	dir := initGitRepo(t)
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	outside := filepath.Join(dir, "elsewhere", "WL-7-fix-thing")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "WL-7-fix-thing", outside).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := worktree.SetTaskID(outside, "WL-7"); err != nil {
		t.Fatalf("SetTaskID: %v", err)
	}
	if id, ok := mustLayout(t).TaskID(outside); ok {
		t.Fatalf("TaskID(%s) = (%q, true), want ok=false: the path is not one level below %s", outside, id, worktree.DefaultBase)
	}
}

func TestSetTaskIDFailsOnSecondWorktreeWithoutExtension(t *testing.T) {
	dir := initGitRepo(t)
	wt := filepath.Join(dir, worktree.DefaultBase, "WL-1-fix-thing")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-b", "WL-1-fix-thing", wt).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	if err := worktree.SetTaskID(wt, "WL-1"); err == nil {
		t.Fatalf("SetTaskID on a second worktree without extensions.worktreeConfig: err = nil, want error")
	}
}

func TestSetTaskIDIsolatedAcrossWorktreesAfterEnablingExtension(t *testing.T) {
	dir := initGitRepo(t)
	if err := worktree.EnableWorktreeConfigExtension(dir); err != nil {
		t.Fatalf("EnableWorktreeConfigExtension: %v", err)
	}
	l := mustLayout(t)
	one := addWorktreeUnderBase(t, dir, "WL-1-first")
	two := addWorktreeUnderBase(t, dir, "WL-2-second")
	if err := worktree.SetTaskID(one, "WL-1"); err != nil {
		t.Fatalf("SetTaskID(one): %v", err)
	}
	if err := worktree.SetTaskID(two, "WL-2"); err != nil {
		t.Fatalf("SetTaskID(two): %v", err)
	}
	// Each worktree reads back its own id, not the other's and not a shared
	// write into .git/config — that isolation is the whole point of
	// extensions.worktreeConfig.
	if gotID, ok := l.TaskID(one); !ok || gotID != "WL-1" {
		t.Fatalf("TaskID(one) = (%q, %v), want (\"WL-1\", true)", gotID, ok)
	}
	if gotID, ok := l.TaskID(two); !ok || gotID != "WL-2" {
		t.Fatalf("TaskID(two) = (%q, %v), want (\"WL-2\", true)", gotID, ok)
	}
}

func TestCurrentBranchAndIsClean(t *testing.T) {
	dir := initGitRepo(t)

	branch, err := worktree.CurrentBranch(dir)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	// initGitRepo commits on git's default init branch; whatever it is
	// called locally, it must be non-empty and match git's own answer.
	out, _ := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if want := strings.TrimSpace(string(out)); branch != want || branch == "" {
		t.Errorf("CurrentBranch = %q, want %q", branch, want)
	}

	clean, err := worktree.IsClean(dir)
	if err != nil || !clean {
		t.Fatalf("IsClean(fresh) = %v, %v; want true", clean, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err = worktree.IsClean(dir)
	if err != nil || clean {
		t.Fatalf("IsClean(dirty) = %v, %v; want false", clean, err)
	}
}

func TestDefaultBranch(t *testing.T) {
	dir := initGitRepo(t)

	// No origin/HEAD recorded: a named error telling the user how to fix it.
	if _, err := worktree.DefaultBranch(dir); err == nil ||
		!strings.Contains(err.Error(), "git remote set-head") {
		t.Fatalf("DefaultBranch without origin/HEAD: err = %v, want set-head hint", err)
	}

	// Record origin/HEAD the way `git remote set-head origin --auto` would;
	// no network needed.
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", dir).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", dir, "symbolic-ref",
		"refs/remotes/origin/HEAD", "refs/remotes/origin/main").CombinedOutput(); err != nil {
		t.Fatalf("set origin/HEAD: %v\n%s", err, out)
	}
	got, err := worktree.DefaultBranch(dir)
	if err != nil || got != "main" {
		t.Fatalf("DefaultBranch = %q, %v; want main", got, err)
	}
}
