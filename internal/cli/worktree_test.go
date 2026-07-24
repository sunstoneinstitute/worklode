package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/cli"
)

// initGitRepo creates a fresh git repo in a temp dir and returns its path.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestWorktreeIdentity(t *testing.T) {
	dir := initGitRepo(t)

	got, err := cli.WorktreeIdentity(dir)
	if err != nil {
		t.Fatalf("WorktreeIdentity: %v", err)
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
		t.Fatalf("WorktreeIdentity = %q, want %q", got, want)
	}
	if !strings.HasPrefix(got, host+":") || !strings.Contains(got, string(os.PathSeparator)) {
		t.Fatalf("WorktreeIdentity = %q, want <hostname>:<abs path>", got)
	}
}

func TestWorktreeIdentitySubdirectory(t *testing.T) {
	dir := initGitRepo(t)
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	fromRoot, err := cli.WorktreeIdentity(dir)
	if err != nil {
		t.Fatalf("WorktreeIdentity(root): %v", err)
	}
	fromSub, err := cli.WorktreeIdentity(sub)
	if err != nil {
		t.Fatalf("WorktreeIdentity(subdir): %v", err)
	}
	if fromSub != fromRoot {
		t.Fatalf("WorktreeIdentity from subdir = %q, want the worktree root identity %q", fromSub, fromRoot)
	}
}

func TestWorktreeIdentityOutsideGit(t *testing.T) {
	if _, err := cli.WorktreeIdentity(t.TempDir()); err == nil {
		t.Fatalf("WorktreeIdentity outside a git worktree: err = nil, want error")
	}
}
