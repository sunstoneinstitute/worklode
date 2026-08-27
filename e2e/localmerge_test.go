//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestLocalMergeReportEndToEnd drives the local merge reporter across the
// process boundary it is otherwise only ever tested up to: a real clone, the
// real `lode-hook post-merge` binary, and a real server with a real store on
// the other end of POST /api/v1/merges.
//
// Both halves are covered on their own — internal/hookrun proves the git
// probe and the request body against a stub server, internal/api proves the
// handler and the store against a hand-built request — so what only this
// test can catch is the two halves disagreeing: a renamed JSON key, a repo
// URL form the server will not normalize, an abbreviated sha the validator
// rejects. Any of those still passes both unit suites and fails in the field.
//
// The unlanded second task is the other half of the claim: the reporter must
// name the branches that landed and only those, or every merge would deliver
// every open task in the clone.
func TestLocalMergeReportEndToEnd(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// The project must map the repo the clone's origin names, or the
	// reporter's candidate query returns nothing to probe.
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "demo", Name: "Demo", Key: "DEMO",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.AddRepo(ctx, "demo", repo, ""); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e local merge", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// The hook resolves its own client from the environment, exactly as the
	// installed git hook does on a developer's machine.
	t.Setenv("LODE_SERVER", srv.URL)
	t.Setenv("LODE_TOKEN", tok.Token)
	dist := t.TempDir()
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dist, "lode-hook"), "./cmd/lode-hook")
	build.Dir = store.ModuleRootForTests()
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lode-hook: %v\n%s", err, out)
	}
	t.Setenv("PATH", dist+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Two claimed tasks, one of whose branches will land in the clone.
	landedID, landedBranch := claimForMerge(t, ctx, agent, "Land the widget", "e2e-merge-wt-1")
	unlandedID, unlandedBranch := claimForMerge(t, ctx, agent, "Still in progress", "e2e-merge-wt-2")

	// A clone with work on both branches, but only one of them merged.
	root := initE2ECloneOf(t, repo)
	commitOnBranch(t, root, landedBranch, "widget.txt", "widget\n")
	commitOnBranch(t, root, unlandedBranch, "wip.txt", "wip\n")
	// --no-ff: a merge commit is what `git merge` leaves for the real
	// post-merge hook to look at, and it keeps HEAD~1 meaningful — the
	// reporter reads it to tell what this event added from what was already
	// there.
	e2eGit(t, root, "merge", "--no-ff", "-m", "Merge "+landedBranch, landedBranch)
	mergeSHA := e2eGit(t, root, "rev-parse", "HEAD")

	hook := exec.Command(filepath.Join(dist, "lode-hook"), "post-merge")
	hook.Dir = root
	if out, err := hook.CombinedOutput(); err != nil {
		t.Fatalf("lode-hook post-merge: %v\noutput: %s", err, out)
	}

	// The merge never reached GitHub and no webhook was delivered, so the
	// task reaching merged can only have come through the hook's report.
	detail, _, err := agent.GetTask(ctx, landedID)
	if err != nil {
		t.Fatalf("get merged task: %v", err)
	}
	if detail.State != "merged" {
		t.Fatalf("task %s state = %q, want merged after the local merge of %s",
			landedID, detail.State, mergeSHA)
	}

	other, _, err := agent.GetTask(ctx, unlandedID)
	if err != nil {
		t.Fatalf("get unlanded task: %v", err)
	}
	if other.State != "in_progress" {
		t.Fatalf("task %s state = %q, want in_progress: its branch never landed",
			unlandedID, other.State)
	}
}

// claimForMerge creates and claims one task, returning its id and branch.
// The branch name is the server's, and the clone must use it verbatim for
// the reporter to recognize the branch as a task's — a claim is also what
// gives a task a branch at all.
func claimForMerge(t *testing.T, ctx context.Context, agent *cli.Client, title, wt string) (id, branch string) {
	t.Helper()
	task, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "demo", Title: title, Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	// A distinct worktree per claim: one actor holds at most one lease per
	// worktree, which is what a real developer's two task worktrees are.
	claim, _, err := agent.ClaimTask(ctx, task.ID, wt, 0)
	if err != nil {
		t.Fatalf("claim task %s: %v", task.ID, err)
	}
	return task.ID, claim.Branch
}

// commitOnBranch branches off the default branch, commits one file, and
// leaves the clone back on the default branch — where a merge happens and
// where the reporter refuses to do anything otherwise.
func commitOnBranch(t *testing.T, root, branch, name, content string) {
	t.Helper()
	e2eGit(t, root, "checkout", "-b", branch, defaultBranch)
	e2eCommit(t, root, name, content, "work on "+branch)
	e2eGit(t, root, "checkout", defaultBranch)
}
