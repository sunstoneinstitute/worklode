//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/cmd"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// runLodeCLI executes the real lode CLI entry point (internal/cmd.Execute,
// the same function main.go calls) with args, as a real process boundary
// would: it swaps os.Args and captures os.Stdout for the duration of the
// call. Callers must already have set LODE_SERVER/LODE_TOKEN and t.Chdir'd
// into the directory the command should run from.
func runLodeCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	oldArgs := os.Args
	os.Args = append([]string{"lode"}, args...)
	defer func() { os.Args = oldArgs }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()

	runErr := cmd.Execute()

	w.Close()
	os.Stdout = oldStdout
	return <-done, runErr
}

// initE2EGitRepo creates a fresh git repo with one commit and returns its
// path resolved to git's own notion of the toplevel (see the identical
// comment in internal/cmd/lifecycle_test.go for why: macOS resolves the
// TempDir's /var symlink through git but not through the raw string).
func initE2EGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
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

	root, ok := worktree.Root(dir)
	if !ok {
		t.Fatalf("worktree.Root(%s): ok = false", dir)
	}
	return root
}

// TestNextEndToEnd drives `lode next <id>` through the real CLI entry point
// against a real temp git repo and an ephemeral store: the worktree is
// actually created via `git worktree add`, the lease is rebound to its real
// worktree.Identity path, and the resulting brief is printed. It then forces
// the rebind step to collide with another actively-leased worktree and
// verifies `lode next` rolls back cleanly (lease released, worktree removed).
func TestNextEndToEnd(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{ID: "nx", Name: "Next", Key: "NX"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{ID: "agent-1", Kind: "agent", DisplayName: "Agent One"}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e next", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", tok.Token)
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	task, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "nx", Title: "Wire up the widget", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	root := initE2EGitRepo(t)
	t.Chdir(root)

	out, err := runLodeCLI(t, "next", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}

	wantDir := filepath.Join(root, worktree.DefaultBase, task.ID+"-wire-up-the-widget")
	if info, statErr := os.Stat(wantDir); statErr != nil || !info.IsDir() {
		t.Fatalf("worktree dir %s not created: %v", wantDir, statErr)
	}
	// The directory must actually be a registered git worktree, not just a
	// directory that happens to exist at that path.
	listOut, err := exec.Command("git", "-C", root, "worktree", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, listOut)
	}
	if !bytes.Contains(listOut, []byte(wantDir)) {
		t.Fatalf("git worktree list does not mention %s:\n%s", wantDir, listOut)
	}

	wantIdentity, err := worktree.Identity(wantDir)
	if err != nil {
		t.Fatalf("worktree.Identity(%s): %v", wantDir, err)
	}
	detail, _, err := agent.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "in_progress" {
		t.Fatalf("task state = %q, want in_progress", detail.State)
	}
	if detail.Lease == nil || detail.Lease.Worktree != wantIdentity {
		t.Fatalf("task lease = %+v, want worktree %s", detail.Lease, wantIdentity)
	}

	// --- forced rebind failure: another task's lease already occupies the
	// exact worktree identity a second claim would rebind to. `lode next`
	// must claim, fail at the rebind step, then roll back: release its
	// lease and best-effort remove the worktree it just created.
	task2, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "nx", Title: "Second task", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}
	task2Dir := filepath.Join(root, worktree.DefaultBase, task2.ID+"-second-task")

	decoy, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "nx", Title: "Decoy holder", Priority: "low", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create decoy task: %v", err)
	}
	// The store never validates that a lease's worktree string is a real
	// path, so the decoy can occupy task2's future identity before task2's
	// worktree exists at all.
	decoyIdentity, err := computeIdentityForFuturePath(t, root, task2Dir)
	if err != nil {
		t.Fatalf("compute future identity: %v", err)
	}
	if _, _, err := agent.ClaimTask(ctx, decoy.ID, decoyIdentity, 0); err != nil {
		t.Fatalf("claim decoy onto %s: %v", decoyIdentity, err)
	}

	if _, err := runLodeCLI(t, "next", task2.ID, "--json"); err == nil {
		t.Fatalf("lode next task2: err = nil, want a rebind-conflict error")
	}

	if _, statErr := os.Stat(task2Dir); !os.IsNotExist(statErr) {
		t.Fatalf("worktree dir %s should have been rolled back, stat err = %v", task2Dir, statErr)
	}
	detail2, _, err := agent.GetTask(ctx, task2.ID)
	if err != nil {
		t.Fatalf("get task2: %v", err)
	}
	if detail2.State != "ready" || detail2.Lease != nil {
		t.Fatalf("task2 after rollback = state %q lease %+v, want ready/nil", detail2.State, detail2.Lease)
	}
}

// computeIdentityForFuturePath returns the worktree.Identity string a path
// would have once it becomes a real git worktree at root, without leaving
// that worktree registered: it creates a throwaway worktree there just long
// enough to ask git for its canonical toplevel, then removes it again.
func computeIdentityForFuturePath(t *testing.T, root, path string) (string, error) {
	t.Helper()
	branch := "throwaway-" + filepath.Base(path)
	if out, err := exec.Command("git", "-C", root, "worktree", "add", path, "-b", branch).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add %s: %v\n%s", path, err, out)
	}
	identity, err := worktree.Identity(path)
	if err != nil {
		return "", err
	}
	if out, err := exec.Command("git", "-C", root, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
		t.Fatalf("git worktree remove %s: %v\n%s", path, err, out)
	}
	if out, err := exec.Command("git", "-C", root, "branch", "-D", branch).CombinedOutput(); err != nil {
		t.Fatalf("git branch -D %s: %v\n%s", branch, err, out)
	}
	return identity, nil
}
