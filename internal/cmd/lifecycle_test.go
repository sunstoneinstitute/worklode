package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
	"github.com/sunstoneinstitute/worklode/internal/worktree"
)

// lifecycleTestServer opens a fresh store, creates admin actor "alice" with a
// token, starts a real HTTP server, and points LODE_SERVER/LODE_TOKEN at it
// (the lifecycle commands build their own client via newAPIClient(), reading
// those env vars). Returns the store, for out-of-band setup, and a Client
// authenticated as alice for direct API calls the test needs outside the CLI.
func lifecycleTestServer(t *testing.T) (*store.Store, *cli.Client) {
	t.Helper()
	st := store.OpenTestStore(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	h, _, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	t.Setenv("LODE_SERVER", ts.URL)
	t.Setenv("LODE_TOKEN", token)

	return st, cli.NewClient(cli.Config{ServerURL: ts.URL, Token: token})
}

// runLode executes rootCmd with args and returns its captured stdout. rootCmd
// is a package-level singleton built once in init(), so every flag it owns
// keeps its value and Changed state between calls; resetFlags scrubs both so a
// prior test's `--json` or `--body` does not leak into a later Execute().
func runLode(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(t, rootCmd)
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// resetFlags restores every flag in cmd's tree to its declared default and
// clears Changed, so commands that branch on Flags().Changed(...) see only the
// flags of the current invocation.
func resetFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	reset := func(f *pflag.Flag) {
		f.Changed = false
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			if err := sv.Replace(nil); err != nil {
				t.Fatalf("reset --%s: %v", f.Name, err)
			}
			return
		}
		if err := f.Value.Set(f.DefValue); err != nil {
			t.Fatalf("reset --%s: %v", f.Name, err)
		}
	}
	cmd.Flags().VisitAll(reset)
	cmd.PersistentFlags().VisitAll(reset)
	for _, sub := range cmd.Commands() {
		resetFlags(t, sub)
	}
}

// initGitRepo creates a fresh git repo with one commit (so `git worktree add
// -b` has a commit to branch from) and returns its path, resolved to git's
// own notion of the toplevel: on macOS t.TempDir() lives under a symlink
// (/var -> /private/var), and `git rev-parse --show-toplevel` (which
// worktree.Root uses) resolves it, so comparisons against the raw TempDir
// path would spuriously fail.
func initGitRepo(t *testing.T) string {
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

// moveToReview transitions a task from in_progress to in_review directly via
// the store, simulating the PR-open transition the CLI has no command for
// (mirrors the identical helper in internal/cli/client_test.go).
func moveToReview(t *testing.T, st *store.Store, taskID string) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github", "to-review-"+taskID, "task.review", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, st.Now(), taskID, "in_progress", "in_review", eventID)
		})
	if err != nil {
		t.Fatalf("move %s to in_review: %v", taskID, err)
	}
}

func createTestTask(t *testing.T, c *cli.Client, title string) cli.Task {
	t.Helper()
	ctx := context.Background()
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: title, Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}

func setupProject(t *testing.T, c *cli.Client) {
	t.Helper()
	if _, _, err := c.CreateProject(context.Background(), cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
}

// --- lode next --------------------------------------------------------

func TestSlugFromBranch(t *testing.T) {
	cases := []struct{ branch, id, want string }{
		{"lode/WL-7-fix-thing", "WL-7", "fix-thing"},
		{"team/WL-7-fix-thing", "WL-7", "fix-thing"},
		{"wl/WL-7-fix-thing", "WL-7", "fix-thing"},
		// A slug repeating the task id must stay intact: the prefix-adjacent
		// occurrence is the separator, not the last one.
		{"lode/WL-7-fix-WL-7-bug", "WL-7", "fix-WL-7-bug"},
		{"main", "WL-7", "main"}, // no id: caller keeps the branch as-is
	}
	for _, c := range cases {
		if got := slugFromBranch(c.branch, c.id); got != c.want {
			t.Errorf("slugFromBranch(%q, %q) = %q, want %q", c.branch, c.id, got, c.want)
		}
	}
}

func TestNextClaimsSpecificTaskAndSetsUpWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Fix the thing")

	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}

	var result struct {
		Claimed  bool            `json:"claimed"`
		Worktree string          `json:"worktree"`
		Branch   string          `json:"branch"`
		Brief    json.RawMessage `json:"brief"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if !result.Claimed {
		t.Fatalf("claimed = false, want true (output %s)", out)
	}
	wantBranch := "lode/" + task.ID + "-fix-the-thing"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", result.Branch, wantBranch)
	}
	wantDir := filepath.Join(root, "wt", task.ID+"-fix-the-thing")
	if result.Worktree != wantDir {
		t.Fatalf("worktree = %q, want %q", result.Worktree, wantDir)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir %s does not exist: %v", wantDir, err)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "in_progress" {
		t.Fatalf("task state = %q, want in_progress", detail.State)
	}
	if detail.Lease == nil || detail.Lease.Worktree == "" {
		t.Fatalf("task lease = %+v, want a bound worktree", detail.Lease)
	}
	if _, statErr := os.Stat(detail.Lease.Worktree); statErr == nil {
		t.Fatalf("lease.Worktree = %q looks like a bare filesystem path, want <hostname>:<path>", detail.Lease.Worktree)
	}
}

func TestNextClaimsTopRankedWithNoID(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Only ready task")

	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", "--json")
	if err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}
	var result struct {
		Claimed bool   `json:"claimed"`
		Branch  string `json:"branch"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if !result.Claimed {
		t.Fatalf("claimed = false, want true")
	}
	wantBranch := "lode/" + task.ID + "-only-ready-task"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", result.Branch, wantBranch)
	}
}

func TestNextNoReadyTask(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)

	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", "--json")
	if err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}
	var result struct {
		Claimed bool   `json:"claimed"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if result.Claimed || result.Reason != "no-ready-task" {
		t.Fatalf("result = %+v, want claimed=false reason=no-ready-task", result)
	}
}

func TestNextRefusesInsideExistingWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Already claimed")

	root := initGitRepo(t)
	t.Chdir(root)
	if out, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next (setup): %v\noutput: %s", err, out)
	}

	dir := filepath.Join(root, "wt", task.ID+"-already-claimed")
	t.Chdir(dir)

	if _, err := runLode(t, "next", "--json"); err == nil {
		t.Fatalf("lode next from inside a worktree: err = nil, want error")
	}
}

func TestNextRollsBackOnWorktreeAddFailure(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Blocked worktree")

	root := initGitRepo(t)
	// Pre-occupy the deterministic worktree path with a non-empty directory
	// that isn't a git worktree, so `git worktree add` (both the -b attempt
	// and the attach-existing-branch retry) fails.
	dir := filepath.Join(root, "wt", task.ID+"-blocked-worktree")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray file: %v", err)
	}
	t.Chdir(root)

	if _, err := runLode(t, "next", task.ID, "--json"); err == nil {
		t.Fatalf("lode next with a pre-occupied worktree path: err = nil, want error")
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "ready" {
		t.Fatalf("task state after rollback = %q, want ready (lease should have been released)", detail.State)
	}
	if detail.Lease != nil {
		t.Fatalf("task lease after rollback = %+v, want nil", detail.Lease)
	}
}

// --- lode resume --------------------------------------------------------

func TestResumeRenewsHeldLease(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Resume me")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	before, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	dir := filepath.Join(root, "wt", task.ID+"-resume-me")
	t.Chdir(dir)

	// Renewal only bumps renewed_at/expires_at meaningfully once time has
	// moved on; assert it succeeds and keeps the same worktree binding.
	if _, err := runLode(t, "resume", "--json"); err != nil {
		t.Fatalf("lode resume: %v", err)
	}
	after, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task after resume: %v", err)
	}
	if after.Lease == nil || after.Lease.Worktree != before.Lease.Worktree {
		t.Fatalf("lease after resume = %+v, want same worktree as %+v", after.Lease, before.Lease)
	}
	if after.State != "in_progress" {
		t.Fatalf("task state after resume = %q, want in_progress", after.State)
	}
}

func TestResumeReclaimsAfterSweeperExpiry(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Expired lease")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(root, "wt", task.ID+"-expired-lease")

	// Force the sweeper to reclaim the lease: everything expires "now+3h".
	if _, err := st.ExpireLeases(context.Background(), time.Now().Add(3*time.Hour)); err != nil {
		t.Fatalf("expire leases: %v", err)
	}
	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "ready" || detail.Lease != nil {
		t.Fatalf("task after sweep = state %q lease %+v, want ready/nil (sweeper should have reclaimed it)", detail.State, detail.Lease)
	}

	t.Chdir(dir)
	if _, err := runLode(t, "resume", "--json"); err != nil {
		t.Fatalf("lode resume after sweep: %v", err)
	}
	detail, _, err = c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task after resume: %v", err)
	}
	if detail.State != "in_progress" || detail.Lease == nil {
		t.Fatalf("task after resume = state %q lease %+v, want in_progress/non-nil", detail.State, detail.Lease)
	}
}

func TestResumeErrorsWhenLeasedElsewhere(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Held elsewhere")

	if _, _, err := c.ClaimTask(context.Background(), task.ID, "otherhost:/other/path", 0); err != nil {
		t.Fatalf("claim from elsewhere: %v", err)
	}

	root := initGitRepo(t)
	dir := filepath.Join(root, "wt", task.ID+"-held-elsewhere")
	branch := "lode/" + task.ID + "-held-elsewhere"
	c2 := exec.Command("git", "-C", root, "worktree", "add", dir, "-b", branch)
	if out, err := c2.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	t.Chdir(dir)

	if _, err := runLode(t, "resume", "--json"); err == nil {
		t.Fatalf("lode resume on a lease held elsewhere: err = nil, want error")
	}
}

func TestResumeRefusesOutsideWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	_ = createTestTask(t, c, "Not in a worktree")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "resume", "--json"); err == nil {
		t.Fatalf("lode resume outside a worktree: err = nil, want error")
	}
}

// --- lode done ----------------------------------------------------------

func TestDoneCompletesTaskAndReleasesLease(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Finish this")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(root, "wt", task.ID+"-finish-this")
	moveToReview(t, st, task.ID)

	t.Chdir(dir)
	out, err := runLode(t, "done", "--json")
	if err != nil {
		t.Fatalf("lode done: %v\noutput: %s", err, out)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "merged" {
		t.Fatalf("task state = %q, want merged", detail.State)
	}
	if detail.Lease != nil {
		t.Fatalf("task lease after done = %+v, want nil", detail.Lease)
	}
}

func TestDoneRefusesOutsideWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	_ = createTestTask(t, c, "Not in a worktree")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "done", "--json"); err == nil {
		t.Fatalf("lode done outside a worktree: err = nil, want error")
	}
}

// --- lode block ----------------------------------------------------------

func TestBlockRecordsEdgeAndReleasesLease(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Needs a blocker")
	blocker := createTestTask(t, c, "The blocker")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(root, "wt", task.ID+"-needs-a-blocker")
	t.Chdir(dir)

	out, err := runLode(t, "block", "--on", blocker.ID, "--json")
	if err != nil {
		t.Fatalf("lode block: %v\noutput: %s", err, out)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !detail.Blocked {
		t.Fatalf("task.Blocked = false, want true")
	}
	if detail.Lease != nil {
		t.Fatalf("task lease after block = %+v, want nil", detail.Lease)
	}
}

func TestBlockRefusesOutsideWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	_ = createTestTask(t, c, "Not in a worktree")
	blocker := createTestTask(t, c, "Blocker")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "block", "--on", blocker.ID, "--json"); err == nil {
		t.Fatalf("lode block outside a worktree: err = nil, want error")
	}
}

// --- lode status ----------------------------------------------------------

func TestStatusReportsStateWithoutMutating(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Just looking")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(root, "wt", task.ID+"-just-looking")
	t.Chdir(dir)

	before, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	out, err := runLode(t, "status", "--json")
	if err != nil {
		t.Fatalf("lode status: %v\noutput: %s", err, out)
	}
	var result statusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if result.Task.ID != task.ID {
		t.Fatalf("status task id = %q, want %q", result.Task.ID, task.ID)
	}
	if result.LeaseState != "held" {
		t.Fatalf("lease_state = %q, want held", result.LeaseState)
	}
	if result.SessionMarker {
		t.Fatalf("session_marker = true, want false (no marker file written)")
	}

	after, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task after status: %v", err)
	}
	if !after.Lease.RenewedAt.Equal(before.Lease.RenewedAt) {
		t.Fatalf("status renewed the lease: renewed_at changed from %v to %v", before.Lease.RenewedAt, after.Lease.RenewedAt)
	}
}

func TestStatusReportsResolvedProject(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	mapProjectRepo(t, c, "proj", "acme/proj")
	task := createTestTask(t, c, "Scoped status")

	t.Setenv("HOME", t.TempDir()) // keep the remote cache out of the real one
	root := initGitRepo(t)
	addOrigin(t, root, "git@github.com:acme/proj.git")
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	t.Chdir(filepath.Join(root, "wt", task.ID+"-scoped-status"))

	out, err := runLode(t, "status", "--json")
	if err != nil {
		t.Fatalf("lode status: %v\noutput: %s", err, out)
	}
	var result statusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if result.Project != "proj" {
		t.Fatalf("status project = %q, want proj", result.Project)
	}
	if result.ProjectSource != string(cli.ScopeGitRemote) {
		t.Fatalf("status project_source = %q, want %q", result.ProjectSource, cli.ScopeGitRemote)
	}
}

// addOrigin points a repo's origin at url, so scope resolution has a remote.
func addOrigin(t *testing.T, dir, url string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", url).CombinedOutput(); err != nil {
		t.Fatalf("git remote add origin: %v\n%s", err, out)
	}
}

func TestStatusReportsSessionMarkerPresence(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Has a session")

	root := initGitRepo(t)
	t.Chdir(root)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(root, "wt", task.ID+"-has-a-session")

	gitDirOut, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatalf("git rev-parse --absolute-git-dir: %v", err)
	}
	gitDir := string(bytes.TrimSpace(gitDirOut))
	if err := os.WriteFile(filepath.Join(gitDir, "worklode-session.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("write session marker: %v", err)
	}

	t.Chdir(dir)
	out, err := runLode(t, "status", "--json")
	if err != nil {
		t.Fatalf("lode status: %v\noutput: %s", err, out)
	}
	var result statusResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if !result.SessionMarker {
		t.Fatalf("session_marker = false, want true")
	}
}

func TestStatusRefusesOutsideWorktree(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	_ = createTestTask(t, c, "Not in a worktree")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "status", "--json"); err == nil {
		t.Fatalf("lode status outside a worktree: err = nil, want error")
	}
}

// --- lode task brief -------------------------------------------------------

func TestTaskBriefCmd(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Brief me")

	out, err := runLode(t, "task", "brief", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode task brief: %v\noutput: %s", err, out)
	}
	var b cli.Brief
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if b.Task.ID != task.ID {
		t.Fatalf("brief.Task.ID = %q, want %q", b.Task.ID, task.ID)
	}
	if b.Branch != "lode/"+task.ID+"-brief-me" {
		t.Fatalf("brief.Branch = %q, want lode/%s-brief-me", b.Branch, task.ID)
	}
	if b.Skills.Provider != "none" || len(b.Skills.Pinned) != 0 || len(b.Skills.Matches) != 0 {
		t.Fatalf("brief.Skills = %+v, want empty (no pins, no embedder)", b.Skills)
	}
}

// TestTaskBriefCmdShowsSkills exercises the end-to-end pinned-skill path
// through the CLI: a pin resolves with content, an unknown pin warns, and
// both surface in the non-JSON `lode task brief` rendering.
func TestTaskBriefCmdShowsSkills(t *testing.T) {
	st, c := lifecycleTestServer(t)
	setupProject(t, c)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	task, _, err := c.CreateTask(context.Background(), cli.CreateTaskInput{
		Project: "proj", Title: "Brief with skills", Priority: "high", Kind: "feature",
		Skills: []string{"tdd", "ghost"},
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	out, err := runLode(t, "task", "brief", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode task brief: %v\noutput: %s", err, out)
	}
	var b cli.Brief
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if len(b.Skills.Pinned) != 1 || b.Skills.Pinned[0].Name != "tdd" || b.Skills.Pinned[0].Content == "" {
		t.Fatalf("brief.Skills.Pinned = %+v, want one tdd entry with content", b.Skills.Pinned)
	}
	if len(b.Skills.Warnings) != 1 || b.Skills.Warnings[0] != "pinned skill not found: ghost" {
		t.Fatalf("brief.Skills.Warnings = %+v, want [pinned skill not found: ghost]", b.Skills.Warnings)
	}

	// Non-JSON rendering surfaces the same information via printBrief.
	out, err = runLode(t, "task", "brief", task.ID)
	if err != nil {
		t.Fatalf("lode task brief (table): %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "pinned  tdd") {
		t.Fatalf("brief output = %q, want a pinned tdd line", out)
	}
	if !strings.Contains(out, "warning: pinned skill not found: ghost") {
		t.Fatalf("brief output = %q, want the missing-skill warning", out)
	}
}

// --- printBrief -------------------------------------------------------

func TestPrintBriefRendersSkillsSection(t *testing.T) {
	b := cli.Brief{
		Task:   cli.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "lode/WL-1-t",
		Skills: cli.SkillRecommendation{
			Pinned:   []cli.PinnedSkill{{Name: "tdd", Description: "Red-green-refactor"}},
			Matches:  []cli.SkillMatch{{Name: "debugging", Description: "Systematic debugging", Score: 0.87}},
			Warnings: []string{"pinned skill not found: ghost"},
			Provider: "openai-compatible",
		},
	}
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	printBrief(cmd, b)

	out := buf.String()
	if !strings.Contains(out, "Skills:") {
		t.Fatalf("output = %q, want a Skills section", out)
	}
	if !strings.Contains(out, "pinned  tdd — Red-green-refactor (content in brief)") {
		t.Fatalf("output = %q, want a rendered pinned line", out)
	}
	if !strings.Contains(out, "0.87    debugging — Systematic debugging") {
		t.Fatalf("output = %q, want a rendered match line", out)
	}
	if !strings.Contains(out, "warning: pinned skill not found: ghost") {
		t.Fatalf("output = %q, want a rendered warning line", out)
	}
}

func TestPrintBriefOmitsSkillsSectionWhenEmpty(t *testing.T) {
	b := cli.Brief{
		Task:   cli.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "lode/WL-1-t",
		Skills: cli.SkillRecommendation{Provider: "none"},
	}
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	printBrief(cmd, b)

	if strings.Contains(buf.String(), "Skills:") {
		t.Fatalf("output = %q, want no Skills section when there is nothing to show", buf.String())
	}
}
