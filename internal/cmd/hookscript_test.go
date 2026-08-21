package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/githooks"
)

// The hook scripts internal/githooks writes are shell that calls this binary,
// so what they do under a real `git commit` can only be checked where the
// binary is: here. internal/githooks tests the file management itself —
// precedence, preservation, quoting — without needing a build.

// TestInstalledHookCommitSucceedsWithoutServer: the installed pre-commit hook
// must be a NOP, not a failure, when no lode server is configured. Otherwise
// `lode install` breaks committing for anyone offline.
func TestInstalledHookCommitSucceedsWithoutServer(t *testing.T) {
	bin := buildLodeBinary(t)
	root := initGitRepoInDir(t, t.TempDir())

	if _, _, err := githooks.Install(root); err != nil {
		t.Fatalf("githooks.Install: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "file.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commitInRepo(t, root, bin)
}

// TestInstalledHookRunsChainTargetWithSpaces: a chain target path containing a
// space (common on macOS) must survive /bin/sh word splitting, or the
// preserved third-party hook silently stops running. githooks asserts the
// quoting; this asserts the quoted script actually runs the hook.
func TestInstalledHookRunsChainTargetWithSpaces(t *testing.T) {
	bin := buildLodeBinary(t)
	root := initGitRepoInDir(t, filepath.Join(t.TempDir(), "My Repo"))

	hooksDir, err := githooks.Dir(root)
	if err != nil {
		t.Fatalf("githooks.Dir: %v", err)
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatalf("mkdir hooks dir: %v", err)
	}
	// A third-party hook that touches a sentinel when it runs.
	sentinel := filepath.Join(t.TempDir(), "third-party-ran")
	thirdParty := "#!/bin/sh\ntouch '" + sentinel + "'\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre-commit"), []byte(thirdParty), 0o755); err != nil {
		t.Fatalf("write third-party pre-commit: %v", err)
	}

	_, chains, err := githooks.Install(root)
	if err != nil {
		t.Fatalf("githooks.Install: %v", err)
	}
	preLodePath := filepath.Join(hooksDir, "pre-commit.pre-lode")
	if chainedTo := chainFor(t, chains, "pre-commit"); chainedTo != preLodePath {
		t.Fatalf("chainedTo = %q, want %q", chainedTo, preLodePath)
	}

	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write file.txt: %v", err)
	}
	if out, err := exec.Command("git", "-C", root, "add", "file.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commitInRepo(t, root, bin)
	if !fileExists(sentinel) {
		t.Fatalf("preserved third-party hook did not run (sentinel %s missing) — chain target was word-split", sentinel)
	}
}

// commitInRepo commits the staged changes with lodeBin first on PATH and no
// ambient config pointing the hook at a real backbone, failing the test with
// the hook's own output when git rejects the commit.
func commitInRepo(t *testing.T, root, lodeBin string) {
	t.Helper()
	commit := exec.Command("git", "-C", root, "-c", "commit.gpgsign=false", "commit", "-m", "add file.txt")
	commit.Env = append(os.Environ(),
		"PATH="+filepath.Dir(lodeBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		"LODE_SERVER=", "LODE_TOKEN=",
	)
	var out bytes.Buffer
	commit.Stdout, commit.Stderr = &out, &out
	if err := commit.Run(); err != nil {
		t.Fatalf("git commit failed: %v\n%s", err, out.String())
	}
}
