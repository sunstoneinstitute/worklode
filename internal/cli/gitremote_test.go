package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initRepo creates a git repo in a temp dir, optionally with an origin remote.
func initRepo(t *testing.T, origin string) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if origin != "" {
		run("remote", "add", "origin", origin)
	}
	return dir
}

func TestGitRemoteURL(t *testing.T) {
	dir := initRepo(t, "git@github.com:acme/app.git")
	if got := gitRemoteURL(context.Background(), dir); got != "git@github.com:acme/app.git" {
		t.Fatalf("gitRemoteURL = %q; want git@github.com:acme/app.git", got)
	}
}

func TestGitRemoteURLFromSubdirectory(t *testing.T) {
	dir := initRepo(t, "https://github.com/acme/app")
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if got := gitRemoteURL(context.Background(), sub); got != "https://github.com/acme/app" {
		t.Fatalf("gitRemoteURL from subdir = %q; want https://github.com/acme/app", got)
	}
}

func TestGitRemoteURLHonoursContext(t *testing.T) {
	dir := initRepo(t, "git@github.com:acme/app.git")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := gitRemoteURL(ctx, dir); got != "" {
		t.Fatalf("cancelled context = %q; want \"\"", got)
	}
}

func TestGitRemoteURLNoOrigin(t *testing.T) {
	if got := gitRemoteURL(context.Background(), initRepo(t, "")); got != "" {
		t.Fatalf("repo without origin = %q; want \"\"", got)
	}
}

func TestGitRemoteURLNotARepo(t *testing.T) {
	if got := gitRemoteURL(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("non-repo directory = %q; want \"\"", got)
	}
}
