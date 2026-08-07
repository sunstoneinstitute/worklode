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

// initGitRepo creates a fresh git repo in a temp dir and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
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
