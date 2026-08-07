package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

func TestDirName(t *testing.T) {
	if got, want := worktree.DirName("WL-7", "fix-the-thing"), "wt/WL-7-fix-the-thing"; got != want {
		t.Fatalf("DirName = %q, want %q", got, want)
	}
}

func TestBranchNameFallback(t *testing.T) {
	if got, want := worktree.BranchName("WL-7", "fix-the-thing"), "WL-7-fix-the-thing"; got != want {
		t.Fatalf("BranchName = %q, want %q", got, want)
	}
}

func TestParseDir(t *testing.T) {
	cases := []struct {
		path   string
		taskID string
		ok     bool
	}{
		{"/repo/wt/WL-7-fix-the-thing", "WL-7", true},
		{"/repo", "", false},
		{"/repo/wt/nope", "", false},
		{"/repo/wt/WL-42", "WL-42", true},       // bare id, no slug
		{"/repo/other/WL-7-fix", "", false},     // second-to-last segment must be wt
		{"wt/WL-7-fix-the-thing", "WL-7", true}, // relative path
		{"/repo/wt/WL-7-Fix-Thing", "", false},  // uppercase not allowed in slug
	}
	for _, c := range cases {
		gotID, gotOK := worktree.ParseDir(c.path)
		if gotID != c.taskID || gotOK != c.ok {
			t.Errorf("ParseDir(%q) = (%q, %v), want (%q, %v)", c.path, gotID, gotOK, c.taskID, c.ok)
		}
	}
}

func TestParseDirGeneralPrefix(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"/x/wt/SW-3-fix-footer", "SW-3"},
		{"/x/wt/SW-3", "SW-3"},
		{"/x/wt/AB12-7-thing", "AB12-7"},
		{"/x/wt/wl-3-nope", ""}, // lowercase prefix still rejected
	} {
		got, ok := worktree.ParseDir(tc.path)
		if tc.want == "" && ok {
			t.Errorf("ParseDir(%q) = (%q, true), want ok=false", tc.path, got)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("ParseDir(%q) = %q, want %q", tc.path, got, tc.want)
		}
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
