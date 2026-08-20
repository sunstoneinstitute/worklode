package cmd

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
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
	"github.com/sunstoneinstitute/worklode/internal/model"
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
	return testServer(t, api.Config{}, nil)
}

// testServer is lifecycleTestServer's body, parameterised for the fixtures
// that need a non-default api.Config or a handler wrapper (wrap may be nil).
func testServer(t *testing.T, cfg api.Config, wrap func(http.Handler) http.Handler) (*store.Store, *cli.Client) {
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
	h, _, err := api.NewServer(st, cfg)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	var handler http.Handler = h
	if wrap != nil {
		handler = wrap(h)
	}
	ts := httptest.NewServer(handler)
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
// -b` has a commit to branch from) in a temp directory of its own.
func initGitRepo(t *testing.T) string {
	t.Helper()
	return initGitRepoInDir(t, t.TempDir())
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

func createTestTask(t *testing.T, c *cli.Client, title string) model.Task {
	t.Helper()
	ctx := context.Background()
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: title, Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("create task %q: %v", title, err)
	}
	return task
}

func setupProject(t *testing.T, c *cli.Client) {
	t.Helper()
	if _, _, err := c.CreateProject(context.Background(), model.CreateProjectInput{ID: "proj", Name: "Project", Key: "PROJ"}); err != nil {
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

// testLayout is the default (.worktrees) layout, for tests that need to
// resolve a worktree path the way the commands under test do.
func testLayout(t *testing.T) worktree.Layout {
	t.Helper()
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	return l
}

func TestResolveWorktreeTaskRejectsNonWorktree(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	// A real git repo root — worktree.Root succeeds, so this exercises the
	// ParseDir rejection (a plain repo root is not a Worklode worktree), not
	// the earlier worktree.Root failure a non-repo tempdir would hit instead.
	root := initGitRepo(t)
	_, _, err = resolveWorktreeTask(l, root, "lode task done <id>")
	if err == nil {
		t.Fatal("resolveWorktreeTask accepted a non-worktree directory")
	}
	// The failure is about the checkout holding no task, and its way out is
	// naming one — not a lecture on the two resolution rules that just missed.
	for _, want := range []string{"not bound to a Worklode task", "lode task done <id>", "lode next [id]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestResolveWorktreeTaskOmitsTheByNameFormWhenAbsent covers the callers that
// have no explicit-id sibling: they pass "", and the failure must not offer a
// blank command line.
func TestResolveWorktreeTaskOmitsTheByNameFormWhenAbsent(t *testing.T) {
	l, err := worktree.NewLayout("")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolveWorktreeTask(l, initGitRepo(t), "")
	if err == nil {
		t.Fatal("resolveWorktreeTask accepted a non-worktree directory")
	}
	if strings.Contains(err.Error(), "say which task to act on") {
		t.Errorf("error %q offers a by-name form the caller does not have", err)
	}
	if !strings.Contains(err.Error(), "lode next [id]") {
		t.Errorf("error %q drops the claim-a-task suggestion", err)
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
	wantBranch := task.ID + "-fix-the-thing"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", result.Branch, wantBranch)
	}
	wantDir := filepath.Join(root, worktree.DefaultBase, task.ID+"-fix-the-thing")
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

// TestNextHonorsConfiguredWorktreeDir proves `lode next` resolves its layout
// from the repo-scoped worktree_dir (LODE_WORKTREE_DIR here, cheapest to set)
// rather than hardcoding worktree.DefaultBase — the one thing layoutFrom
// exists to do. It fails if layoutFrom ignores the configured value.
func TestNextHonorsConfiguredWorktreeDir(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Custom base")

	t.Setenv("LODE_WORKTREE_DIR", "custom-base")
	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", task.ID, "--json")
	if err != nil {
		t.Fatalf("lode next: %v\noutput: %s", err, out)
	}

	var result struct {
		Claimed  bool   `json:"claimed"`
		Worktree string `json:"worktree"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if !result.Claimed {
		t.Fatalf("claimed = false, want true (output %s)", out)
	}
	wantDir := filepath.Join(root, "custom-base", task.ID+"-custom-base")
	if result.Worktree != wantDir {
		t.Fatalf("worktree = %q, want %q", result.Worktree, wantDir)
	}
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir %s does not exist: %v", wantDir, err)
	}
	if _, err := os.Stat(filepath.Join(root, worktree.DefaultBase)); err == nil {
		t.Fatalf("worktree also created under the default base %s; want only %s", worktree.DefaultBase, "custom-base")
	}
}

// templateTestServer is lifecycleTestServer with a non-default
// LODE_BRANCH_TEMPLATE, for tests that need the server to render nested
// branch names. Restores the process-global branch template on cleanup.
func templateTestServer(t *testing.T, tmpl string) *cli.Client {
	t.Helper()
	t.Cleanup(func() { store.SetBranchTemplate("") })
	_, c := testServer(t, api.Config{BranchTemplate: tmpl}, nil)
	return c
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// TestNextFlattensTemplateWorktree drives the full template → branch →
// Layout.Dir → ParseDir chain end to end (spec 008 §5.1, §5.2), which is
// otherwise only proved by composing separate unit tests: a server configured
// with a "/"-containing LODE_BRANCH_TEMPLATE keeps the "/" in the BRANCH but
// gets a flat directory one level below the base, which the path guard then
// recognises as bound. `lode next` from inside that worktree refusing with
// "already inside a worktree" is the guard proof — layoutFrom(cwd) resolved
// the base dir and ParseDir read the flattened name back to a task id.
func TestNextFlattensTemplateWorktree(t *testing.T) {
	c := templateTestServer(t, "team/{{ .id }}-{{ .slug }}")
	setupProject(t, c)
	task := createTestTask(t, c, "Nested template worktree")

	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", task.ID, "--json")
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
	wantBranch := "team/" + task.ID + "-nested-template-worktree"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", result.Branch, wantBranch)
	}
	// Flat: the "/" is flattened to "-", not turned into a directory level.
	wantDir := filepath.Join(root, worktree.DefaultBase, "team-"+task.ID+"-nested-template-worktree")
	if info, err := os.Stat(wantDir); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir %s does not exist: %v", wantDir, err)
	}
	if nested := filepath.Join(root, worktree.DefaultBase, "team"); dirExists(nested) {
		t.Fatalf("%s exists: the layout must be flat, not nested", nested)
	}

	t.Chdir(wantDir)
	if _, err := runLode(t, "next", "--json"); err == nil {
		t.Fatalf("lode next from inside the worktree: err = nil, want error (guard should have recognised %s)", wantDir)
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
	wantBranch := task.ID + "-only-ready-task"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q", result.Branch, wantBranch)
	}
}

// TestNextKindFilter pins WL-95: --kind narrows the candidate set before
// ranking runs, so a higher-ranked task of a different kind does not shadow
// the top-ranked candidate of the requested kind.
func TestNextKindFilter(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	ctx := context.Background()
	if _, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Critical feature", Priority: "critical", Kind: "feature",
	}); err != nil {
		t.Fatalf("create feature task: %v", err)
	}
	chore, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Low chore", Priority: "low", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create chore task: %v", err)
	}

	root := initGitRepo(t)
	t.Chdir(root)

	out, err := runLode(t, "next", "--kind", "chore", "--json")
	if err != nil {
		t.Fatalf("lode next --kind chore: %v\noutput: %s", err, out)
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
	wantBranch := chore.ID + "-low-chore"
	if result.Branch != wantBranch {
		t.Fatalf("branch = %q, want %q (the chore, not the higher-ranked feature)", result.Branch, wantBranch)
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

	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-already-claimed")
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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-blocked-worktree")
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

func TestNextStampsTaskIDInWorktreeGitConfig(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Stamp task id")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}

	wtDir := filepath.Join(root, worktree.DefaultBase, task.ID+"-stamp-task-id")
	gotID, ok := testLayout(t).TaskID(wtDir)
	if !ok || gotID != task.ID {
		t.Fatalf("Layout.TaskID(%s) = (%q, %v), want (%q, true)", wtDir, gotID, ok, task.ID)
	}

	// Check the raw git config value too, not just TaskID's resolution — this
	// is what proves it is the explicit field and not the directory-name
	// fallback.
	out, err := exec.Command("git", "-C", wtDir, "config", "--worktree", "--get", "worklode.task-id").Output()
	if err != nil {
		t.Fatalf("git config --worktree --get worklode.task-id: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != task.ID {
		t.Fatalf("worklode.task-id = %q, want %q", got, task.ID)
	}
}

// TestNextStampsTaskIDAcrossTwoWorktrees is the multi-worktree case
// extensions.worktreeConfig exists for: a second `lode next` in the same repo
// must stamp its own worktree without disturbing the first, once the repo
// already has a worktree and the extension already enabled.
func TestNextStampsTaskIDAcrossTwoWorktrees(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	first := createTestTask(t, c, "First stamp")
	second := createTestTask(t, c, "Second stamp")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "next", first.ID, "--json"); err != nil {
		t.Fatalf("lode next (first): %v", err)
	}
	if _, err := runLode(t, "next", second.ID, "--json"); err != nil {
		t.Fatalf("lode next (second): %v", err)
	}

	l := testLayout(t)
	for _, tc := range []struct{ dir, want string }{
		{filepath.Join(root, worktree.DefaultBase, first.ID+"-first-stamp"), first.ID},
		{filepath.Join(root, worktree.DefaultBase, second.ID+"-second-stamp"), second.ID},
	} {
		gotID, ok := l.TaskID(tc.dir)
		if !ok || gotID != tc.want {
			t.Errorf("Layout.TaskID(%s) = (%q, %v), want (%q, true)", tc.dir, gotID, ok, tc.want)
		}
		// Read the raw per-worktree config too: TaskID alone would also pass
		// via the directory-name fallback, which proves nothing about
		// isolation.
		out, err := exec.Command("git", "-C", tc.dir, "config", "--worktree", "--get", "worklode.task-id").Output()
		if err != nil {
			t.Errorf("git config --worktree --get worklode.task-id in %s: %v", tc.dir, err)
			continue
		}
		if got := strings.TrimSpace(string(out)); got != tc.want {
			t.Errorf("worklode.task-id in %s = %q, want %q", tc.dir, got, tc.want)
		}
	}
}

// TestNextMirrorsLocalClaudeHooksWhenRootOptedIn proves `lode next` treats a
// developer's own `lode install` (local scope) as a standing choice: every
// worktree it creates afterward gets the same Claude Code bindings, not just
// the main checkout — settings.local.json is gitignored, so a linked
// worktree's checkout would otherwise never see it (WL follow-up: local-scope
// hooks did not propagate to worktrees).
func TestNextMirrorsLocalClaudeHooksWhenRootOptedIn(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Mirror hooks")

	root := initGitRepo(t)
	if err := installClaudeHooks(filepath.Join(root, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("install claude hooks at root: %v", err)
	}
	t.Chdir(root)

	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}

	wtDir := filepath.Join(root, worktree.DefaultBase, task.ID+"-mirror-hooks")
	data, err := os.ReadFile(filepath.Join(wtDir, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("read worktree settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse worktree settings: %v", err)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatalf("worktree settings = %v, want a mirrored SessionStart binding", settings)
	}
}

// TestNextLeavesWorktreeSettingsAloneWhenRootNeverOptedIn is the converse:
// `lode next` must never opt a worktree into Claude Code hooks on its own —
// only mirror a choice already made at root.
func TestNextLeavesWorktreeSettingsAloneWhenRootNeverOptedIn(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "No hooks")

	root := initGitRepo(t)
	t.Chdir(root)

	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}

	wtDir := filepath.Join(root, worktree.DefaultBase, task.ID+"-no-hooks")
	if _, err := os.Stat(filepath.Join(wtDir, ".claude", "settings.local.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no settings.local.json in worktree, stat err = %v", err)
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

	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-resume-me")
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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-expired-lease")

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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-held-elsewhere")
	branch := task.ID + "-held-elsewhere"
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

// writeWorktreeDirConfig writes a repo-local .worklode/config.toml pinning
// worktree_dir to base, so layoutFrom(dir) resolves it for that repo
// regardless of the process cwd.
func writeWorktreeDirConfig(t *testing.T, repo, base string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir .worklode: %v", err)
	}
	content := "worktree_dir = \"" + base + "\"\n"
	if err := os.WriteFile(filepath.Join(repo, ".worklode", "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}
}

// TestResumeResolvesLayoutFromTargetDirNotCwd pins the most serious bug found
// in this plan (fixed in "cmd/cli: resolve worktree layout from the repo,
// not the merged config"): runResume must build its worktree.Layout from the
// resume argument's own repo, not from the invoking process's cwd. The two
// repos here configure DIFFERENT bases, so a cwd-derived layout cannot
// accidentally succeed by coincidentally matching the target's base — it must
// fail closed with "not a Worklode worktree" if the bug regresses.
func TestResumeResolvesLayoutFromTargetDirNotCwd(t *testing.T) {
	_, c := lifecycleTestServer(t)
	setupProject(t, c)
	task := createTestTask(t, c, "Cross repo resume")

	target := initGitRepo(t)
	writeWorktreeDirConfig(t, target, "target-worktrees")
	t.Chdir(target)
	if _, err := runLode(t, "next", task.ID, "--json"); err != nil {
		t.Fatalf("lode next: %v", err)
	}
	dir := filepath.Join(target, "target-worktrees", task.ID+"-cross-repo-resume")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("worktree dir %s does not exist: %v", dir, err)
	}

	// A second, unrelated repo with a different configured base is the
	// invoking cwd for the resume below.
	other := initGitRepo(t)
	writeWorktreeDirConfig(t, other, "other-worktrees")
	t.Chdir(other)

	if out, err := runLode(t, "resume", dir, "--json"); err != nil {
		t.Fatalf("lode resume %s from a different repo (cwd %s): %v\noutput: %s", dir, other, err, out)
	}

	detail, _, err := c.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if detail.State != "in_progress" || detail.Lease == nil {
		t.Fatalf("task after resume = state %q lease %+v, want in_progress/non-nil", detail.State, detail.Lease)
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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-finish-this")
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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-needs-a-blocker")
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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-just-looking")
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
	t.Chdir(filepath.Join(root, worktree.DefaultBase, task.ID+"-scoped-status"))

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
	dir := filepath.Join(root, worktree.DefaultBase, task.ID+"-has-a-session")

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
	var b model.Brief
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("decode output %q: %v", out, err)
	}
	if b.Task.ID != task.ID {
		t.Fatalf("brief.Task.ID = %q, want %q", b.Task.ID, task.ID)
	}
	if b.Branch != task.ID+"-brief-me" {
		t.Fatalf("brief.Branch = %q, want %s-brief-me", b.Branch, task.ID)
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

	task, _, err := c.CreateTask(context.Background(), model.CreateTaskInput{
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
	var b model.Brief
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
	b := model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{
			Pinned:   []model.PinnedSkill{{Name: "tdd", Description: "Red-green-refactor"}},
			Matches:  []model.SkillMatch{{Name: "debugging", Description: "Systematic debugging", Score: 0.87}},
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

// A brief whose only skills content is warnings still prints them: a user who
// misspelled every pin would otherwise see nothing, which is the one case the
// warnings exist for.
func TestPrintBriefRendersWarningsOnlySkillsSection(t *testing.T) {
	b := model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{
			Warnings: []string{"pinned skill not found: ghost"},
			Provider: "openai-compatible",
		},
	}
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	printBrief(cmd, b)

	if out := buf.String(); !strings.Contains(out, "warning: pinned skill not found: ghost") {
		t.Fatalf("output = %q, want the warning rendered", out)
	}
}

func TestPrintBriefOmitsSkillsSectionWhenEmpty(t *testing.T) {
	b := model.Brief{
		Task:   model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch: "WL-1-t",
		Skills: model.SkillRecommendation{Provider: "none"},
	}
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	printBrief(cmd, b)

	if strings.Contains(buf.String(), "Skills:") {
		t.Fatalf("output = %q, want no Skills section when there is nothing to show", buf.String())
	}
}

// TestPrintBriefRendersBlockingPlans: the plans holding a task (025 §9.3) are
// rendered even when they have minted no task to list under "blocked by".
func TestPrintBriefRendersBlockingPlans(t *testing.T) {
	b := model.Brief{
		Task:          model.Task{ID: "WL-1", Title: "T", State: "ready", Priority: "high"},
		Branch:        "WL-1-t",
		BlockingPlans: []model.DocRef{{ID: 7, Slug: "plan-a", Title: "Plan A", Status: "draft"}},
		Skills:        model.SkillRecommendation{Provider: "none"},
	}
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)

	printBrief(cmd, b)

	if out := buf.String(); !strings.Contains(out, "blocked by plans:\n  - plan-a: Plan A (draft)") {
		t.Fatalf("output = %q, want the blocking plan rendered", out)
	}
}
