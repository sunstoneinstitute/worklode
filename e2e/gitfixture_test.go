//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// e2eGit runs one git command in dir and returns its trimmed output, failing
// the test on a non-zero exit. The identity and commit.gpgsign=false come
// from here rather than the developer's global config: a temp-repo commit
// must not depend on the machine it runs on, and signing would block it.
func e2eGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir, "-c", "commit.gpgsign=false"}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// e2eCommit writes a file in dir and commits it.
func e2eCommit(t *testing.T, dir, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	e2eGit(t, dir, "add", name)
	e2eGit(t, dir, "commit", "-m", msg)
}

// initE2EGitRepo creates a fresh git repo with one commit on main and returns
// its path resolved to git's own notion of the toplevel (see the identical
// comment in internal/cmd/lifecycle_test.go for why: macOS resolves the
// TempDir's /var symlink through git but not through the raw string).
//
// The default branch is pinned rather than inherited from init.defaultBranch:
// callers that merge into it need to name it, and the local merge reporter
// resolves it from the repo itself.
func initE2EGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	e2eGit(t, dir, "-c", "init.defaultBranch="+defaultBranch, "init")
	e2eCommit(t, dir, "README.md", "test\n", "initial commit")

	root, ok := worktree.Root(dir)
	if !ok {
		t.Fatalf("worktree.Root(%s): ok = false", dir)
	}
	return root
}

// defaultBranch is the branch initE2EGitRepo makes the repo's default.
const defaultBranch = "main"

// initE2ECloneOf builds what a developer's checkout looks like to the local
// merge reporter: a repo with an origin remote naming the GitHub repo the
// fixture project maps. The URL is the SSH form on purpose — the server
// normalizes whatever remote form the clone happens to carry, and the
// stored owner/name is what it must resolve to.
func initE2ECloneOf(t *testing.T, ghRepo string) string {
	t.Helper()
	root := initE2EGitRepo(t)
	e2eGit(t, root, "remote", "add", "origin", "git@github.com:"+ghRepo+".git")
	return root
}
