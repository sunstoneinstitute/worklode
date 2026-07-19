package cli_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/api"
	"github.com/sunstoneinstitute/work-tracker/internal/cli"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// newTestServer opens a store in a temp dir, creates actor "alice" with a
// token, and starts a real HTTP server (httptest.NewServer, a live listener
// — not httptest.NewRecorder — since cli.Client makes real net/http calls).
// Returns the store (for out-of-band setup like seeding an inbox issue), a
// Client pointed at the server and authenticated as alice, and the server's
// base URL (for tests that need a second client with a different token).
func newTestServer(t *testing.T) (*store.Store, *cli.Client, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	if err := st.CreateActor(ctx, "alice", "human", "Alice"); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	h, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	c := cli.NewClient(cli.Config{ServerURL: ts.URL, Token: token})
	return st, c, ts.URL
}

// moveToReview transitions a task from in_progress to in_review directly via
// the store, simulating the PR-open transition the CLI itself has no
// command for.
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

func TestClientProjectsAndRepos(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()

	p, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", DeployGated: true})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "proj" || p.Name != "Project" || !p.DeployGated {
		t.Fatalf("CreateProject result = %+v", p)
	}

	if _, err := c.AddRepo(ctx, "proj", "acme/widgets"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	list, _, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list.Projects) != 1 || len(list.Projects[0].Repos) != 1 || list.Projects[0].Repos[0] != "acme/widgets" {
		t.Fatalf("ListProjects result = %+v", list.Projects)
	}
}

func TestClientActorsAndTokens(t *testing.T) {
	st, c, baseURL := newTestServer(t)
	ctx := context.Background()

	a, _, err := c.CreateActor(ctx, cli.CreateActorInput{ID: "bob", Kind: "agent", DisplayName: "Bob"})
	if err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	if a.ID != "bob" || a.Kind != "agent" {
		t.Fatalf("CreateActor result = %+v", a)
	}

	tok, _, err := c.CreateToken(ctx, "bob", "bob's token", nil)
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if !strings.HasPrefix(tok.Token, "wt_") {
		t.Fatalf("token = %q, want wt_ prefix", tok.Token)
	}
	// The freshly minted token actually authenticates.
	bobClient := cli.NewClient(cli.Config{ServerURL: baseURL, Token: tok.Token})
	if _, _, err := bobClient.ListTasks(ctx, cli.TaskListFilter{}); err != nil {
		t.Fatalf("list tasks as bob: %v", err)
	}

	// Revoke a token minted directly via the store, exercising the client's
	// revoke path independent of its own create path.
	tok2, err := st.CreateToken(ctx, "bob", "second token", nil)
	if err != nil {
		t.Fatalf("store.CreateToken: %v", err)
	}
	if _, err := c.RevokeToken(ctx, tok2); err != nil {
		t.Fatalf("RevokeToken: %v", err)
	}
	revokedClient := cli.NewClient(cli.Config{ServerURL: baseURL, Token: tok2})
	if _, _, err := revokedClient.ListTasks(ctx, cli.TaskListFilter{}); err == nil {
		t.Fatalf("list tasks with revoked token succeeded, want error")
	}
}

func TestClientTaskLifecycle(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.ID != "WT-1" || created.State != "ready" {
		t.Fatalf("CreateTask result = %+v", created)
	}

	list, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "proj"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "WT-1" {
		t.Fatalf("ListTasks result = %+v", list.Tasks)
	}

	detail, _, err := c.GetTask(ctx, "WT-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Blocked || detail.Lease != nil {
		t.Fatalf("GetTask before claim = %+v", detail)
	}

	claim, _, err := c.ClaimTask(ctx, "WT-1", "sess-1", 0)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !strings.HasPrefix(claim.Branch, "wt/WT-1-") {
		t.Fatalf("claim branch = %q", claim.Branch)
	}
	if claim.Lease.ActorID != "alice" {
		t.Fatalf("claim lease = %+v", claim.Lease)
	}

	detail, _, err = c.GetTask(ctx, "WT-1")
	if err != nil {
		t.Fatalf("GetTask after claim: %v", err)
	}
	if detail.Lease == nil || detail.Lease.ActorID != "alice" {
		t.Fatalf("GetTask lease after claim = %+v", detail.Lease)
	}

	renewed, _, err := c.RenewLease(ctx, "WT-1", time.Hour)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if renewed.ExpiresAt.IsZero() {
		t.Fatalf("renewed lease expires_at is zero")
	}

	timeline, _, err := c.Timeline(ctx, "WT-1")
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}
	if len(timeline.Timeline) == 0 {
		t.Fatalf("timeline is empty")
	}

	board, _, err := c.Board(ctx, "proj")
	if err != nil {
		t.Fatalf("Board (project scoped): %v", err)
	}
	if len(board.Projects) != 1 || len(board.Projects[0].InProgress) != 1 {
		t.Fatalf("board = %+v", board.Projects)
	}
	if board.Projects[0].InProgress[0].Holder == nil || board.Projects[0].InProgress[0].Holder.ActorID != "alice" {
		t.Fatalf("board holder = %+v", board.Projects[0].InProgress[0].Holder)
	}
	if board.RecentFailures != nil {
		t.Fatalf("board recent_failures with project filter = %v, want nil", board.RecentFailures)
	}

	boardAll, _, err := c.Board(ctx, "")
	if err != nil {
		t.Fatalf("Board (unscoped): %v", err)
	}
	if boardAll.RecentFailures == nil {
		t.Fatalf("board recent_failures without project filter = nil, want present")
	}

	if _, err := c.ReleaseLease(ctx, "WT-1"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	detail, _, err = c.GetTask(ctx, "WT-1")
	if err != nil {
		t.Fatalf("GetTask after release: %v", err)
	}
	if detail.State != "ready" || detail.Lease != nil {
		t.Fatalf("GetTask after release = %+v", detail)
	}

	// Done: claim, move to in_review out of band (no CLI command for the PR
	// flow that normally does this), then mark done.
	if _, _, err := c.ClaimTask(ctx, "WT-1", "", 0); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	moveToReview(t, st, "WT-1")
	done, _, err := c.DoneTask(ctx, "WT-1")
	if err != nil {
		t.Fatalf("DoneTask: %v", err)
	}
	if done.State != "done" {
		t.Fatalf("DoneTask result = %+v", done)
	}

	// Abandon a fresh task straight from ready.
	abandonTarget, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: "Nope", Priority: "low", Kind: "chore"})
	if err != nil {
		t.Fatalf("CreateTask (abandon target): %v", err)
	}
	abandoned, _, err := c.AbandonTask(ctx, abandonTarget.ID)
	if err != nil {
		t.Fatalf("AbandonTask: %v", err)
	}
	if abandoned.State != "abandoned" {
		t.Fatalf("AbandonTask result = %+v", abandoned)
	}
}

func TestClientBlockUnblock(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	blocker, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: "Blocker", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask blocker: %v", err)
	}
	blockee, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: "Blockee", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask blockee: %v", err)
	}

	if _, err := c.Block(ctx, blockee.ID, blocker.ID); err != nil {
		t.Fatalf("Block: %v", err)
	}
	detail, _, err := c.GetTask(ctx, blockee.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !detail.Blocked {
		t.Fatalf("blockee.Blocked = false, want true")
	}

	if _, err := c.Unblock(ctx, blockee.ID, blocker.ID); err != nil {
		t.Fatalf("Unblock: %v", err)
	}
	detail, _, err = c.GetTask(ctx, blockee.ID)
	if err != nil {
		t.Fatalf("GetTask after unblock: %v", err)
	}
	if detail.Blocked {
		t.Fatalf("blockee.Blocked = true after unblock, want false")
	}
}

func TestClientInboxFlow(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := c.AddRepo(ctx, "proj", "acme/widgets"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	// Seed two inbox issues the way a GitHub webhook delivery would: via
	// UpsertIssue wrapped in RecordEvent.
	seedIssue := func(number int64, title string) {
		t.Helper()
		_, _, err := st.RecordEvent(ctx, "github", "issue-open-"+title, "issues.opened", nil,
			func(tx *sql.Tx, _ int64) error {
				return store.UpsertIssue(tx, store.Issue{
					Repo: "acme/widgets", Number: number, Title: title, State: "open",
					URL: "https://github.com/acme/widgets/issues/1",
				})
			})
		if err != nil {
			t.Fatalf("seed issue %q: %v", title, err)
		}
	}
	seedIssue(1, "Frobnicator is broken")
	seedIssue(2, "Not worth doing")

	list, _, err := c.ListIssues(ctx, "new")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list.Issues) != 2 {
		t.Fatalf("ListIssues result = %+v", list.Issues)
	}

	task, _, err := c.PromoteIssue(ctx, cli.PromoteInput{
		Repo: "acme/widgets", Number: 1, Priority: "high", Kind: "bug",
		AppliesToVersions: []string{"v1.2"},
	})
	if err != nil {
		t.Fatalf("PromoteIssue: %v", err)
	}
	if task.Title != "Frobnicator is broken" {
		t.Fatalf("promoted task title = %q, want issue title as default", task.Title)
	}

	if _, err := c.DismissIssue(ctx, "acme/widgets", 2); err != nil {
		t.Fatalf("DismissIssue: %v", err)
	}

	list, _, err = c.ListIssues(ctx, "new")
	if err != nil {
		t.Fatalf("ListIssues after triage: %v", err)
	}
	if len(list.Issues) != 0 {
		t.Fatalf("ListIssues after triage = %+v, want none left new", list.Issues)
	}
}

func TestClientErrorRendering(t *testing.T) {
	err := &cli.ClientError{Status: 404, Msg: "task WT-9 not found"}
	want := "server error (404): task WT-9 not found"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	_, c, _ := newTestServer(t)
	_, _, err2 := c.GetTask(context.Background(), "WT-99")
	if err2 == nil {
		t.Fatalf("GetTask unknown id: err = nil, want ClientError")
	}
	var ce *cli.ClientError
	if !errors.As(err2, &ce) {
		t.Fatalf("GetTask unknown id err = %v (%T), want *cli.ClientError", err2, err2)
	}
	if ce.Status != 404 {
		t.Fatalf("ClientError.Status = %d, want 404", ce.Status)
	}
	if !strings.HasPrefix(ce.Error(), "server error (404): ") {
		t.Fatalf("ClientError.Error() = %q", ce.Error())
	}
}

func TestLoadConfigFileAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WT_SERVER", "")
	t.Setenv("WT_TOKEN", "")

	cfgDir := filepath.Join(dir, ".config", "wt")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgFile := filepath.Join(cfgDir, "config.toml")
	content := "# a comment\nserver = \"https://file.example.com\"\ntoken = \"wt_filetoken\"\n\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (file only): %v", err)
	}
	if cfg.ServerURL != "https://file.example.com" || cfg.Token != "wt_filetoken" {
		t.Fatalf("LoadConfig (file only) = %+v", cfg)
	}

	t.Setenv("WT_SERVER", "https://env.example.com")
	t.Setenv("WT_TOKEN", "wt_envtoken")
	cfg, err = cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (env override): %v", err)
	}
	if cfg.ServerURL != "https://env.example.com" || cfg.Token != "wt_envtoken" {
		t.Fatalf("LoadConfig (env override) = %+v, want env values to win", cfg)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("WT_SERVER", "https://env-only.example.com")
	t.Setenv("WT_TOKEN", "")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig with no file: %v", err)
	}
	if cfg.ServerURL != "https://env-only.example.com" {
		t.Fatalf("LoadConfig with no file = %+v", cfg)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	for name, content := range map[string]string{
		"missing equals": "not a key value pair\n",
		"unknown key":    "bogus = \"value\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("WT_SERVER", "")
			t.Setenv("WT_TOKEN", "")
			cfgDir := filepath.Join(dir, ".config", "wt")
			if err := os.MkdirAll(cfgDir, 0o755); err != nil {
				t.Fatalf("mkdir config dir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(content), 0o600); err != nil {
				t.Fatalf("write config file: %v", err)
			}
			if _, err := cli.LoadConfig(); err == nil {
				t.Fatalf("LoadConfig with malformed file: err = nil, want error")
			}
		})
	}
}
