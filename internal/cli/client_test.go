package cli_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// seedSkill upserts one skill with a deterministic hash ("h-<name>") and a
// non-empty archive, mirroring internal/api's own seedSkill test helper.
func seedSkill(t *testing.T, st *store.Store, name, description string) {
	t.Helper()
	_, _, err := st.UpsertSkill(context.Background(), store.SkillUpsert{
		Name:        name,
		Description: description,
		SourceRepo:  "acme/skills",
		SourcePath:  "skills/" + name,
		GitCommit:   "deadbeef",
		ContentHash: "h-" + name,
		SkillMD:     "# " + name + "\n\n" + description,
		Frontmatter: json.RawMessage(`{}`),
		Archive:     []byte("gzip-archive-" + name),
	})
	if err != nil {
		t.Fatalf("seed skill %s: %v", name, err)
	}
}

// newTestServer opens a store in a temp dir, creates admin actor "alice"
// with a token, and starts a real HTTP server (httptest.NewServer, a live
// listener — not httptest.NewRecorder — since cli.Client makes real net/http
// calls). Returns the store (for out-of-band setup like seeding an inbox
// issue), a Client pointed at the server and authenticated as alice, and the
// server's base URL (for tests that need a second client with a different
// token).
func newTestServer(t *testing.T) (*store.Store, *cli.Client, string) {
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

	p, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if p.ID != "proj" || p.Name != "Project" || p.Key != "WL" {
		t.Fatalf("CreateProject result = %+v", p)
	}

	if _, _, err := c.AddRepo(ctx, "proj", "acme/widgets", ""); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}

	list, _, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	want := cli.RepoMapping{Repo: "acme/widgets", DoneState: "merged"}
	if len(list.Projects) != 1 || len(list.Projects[0].Repos) != 1 || list.Projects[0].Repos[0] != want {
		t.Fatalf("ListProjects result = %+v", list.Projects)
	}

	if _, err := c.SetRepoDoneState(ctx, "acme/widgets", "released"); err != nil {
		t.Fatalf("SetRepoDoneState: %v", err)
	}
	list, _, err = c.ListProjects(ctx)
	if err != nil {
		t.Fatalf("ListProjects after SetRepoDoneState: %v", err)
	}
	if got := list.Projects[0].Repos[0].DoneState; got != "released" {
		t.Fatalf("done_state after SetRepoDoneState = %q, want released", got)
	}

	// Anything that is not two non-empty segments is rejected client-side and
	// never sent — one case per disjunct of the guard.
	for _, repo := range []string{"widgets", "acme/", "/widgets", ""} {
		t.Run("reject "+repo, func(t *testing.T) {
			_, err := c.SetRepoDoneState(ctx, repo, "released")
			if err == nil {
				t.Fatalf("SetRepoDoneState(%q): want error, got nil", repo)
			}
			var clientErr *cli.ClientError
			if errors.As(err, &clientErr) {
				t.Fatalf("SetRepoDoneState(%q) reached the server: %v", repo, err)
			}
			if !strings.Contains(err.Error(), "owner/name") {
				t.Fatalf("SetRepoDoneState(%q): error = %v, want it to mention owner/name", repo, err)
			}
		})
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
	if !strings.HasPrefix(tok.Token, "wl_") {
		t.Fatalf("token = %q, want wl_ prefix", tok.Token)
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
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.ID != "WL-1" || created.State != "ready" {
		t.Fatalf("CreateTask result = %+v", created)
	}

	list, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "proj"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != "WL-1" {
		t.Fatalf("ListTasks result = %+v", list.Tasks)
	}

	detail, _, err := c.GetTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if detail.Blocked || detail.Lease != nil {
		t.Fatalf("GetTask before claim = %+v", detail)
	}

	claim, _, err := c.ClaimTask(ctx, "WL-1", "host:/wt-1", 0)
	if err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if !strings.HasPrefix(claim.Branch, "WL-1-") {
		t.Fatalf("claim branch = %q", claim.Branch)
	}
	if claim.Lease.ActorID != "alice" || claim.Lease.Worktree != "host:/wt-1" {
		t.Fatalf("claim lease = %+v", claim.Lease)
	}

	detail, _, err = c.GetTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("GetTask after claim: %v", err)
	}
	if detail.Lease == nil || detail.Lease.ActorID != "alice" {
		t.Fatalf("GetTask lease after claim = %+v", detail.Lease)
	}

	renewed, _, err := c.RenewLease(ctx, "WL-1", time.Hour)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if renewed.ExpiresAt.IsZero() {
		t.Fatalf("renewed lease expires_at is zero")
	}

	timeline, _, err := c.Timeline(ctx, "WL-1")
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

	if _, err := c.ReleaseLease(ctx, "WL-1"); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	detail, _, err = c.GetTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("GetTask after release: %v", err)
	}
	if detail.State != "ready" || detail.Lease != nil {
		t.Fatalf("GetTask after release = %+v", detail)
	}

	// Done: claim, move to in_review out of band (no CLI command for the PR
	// flow that normally does this), then mark done.
	if _, _, err := c.ClaimTask(ctx, "WL-1", "host:/wt-2", 0); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	moveToReview(t, st, "WL-1")
	done, _, err := c.DoneTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("DoneTask: %v", err)
	}
	if done.State != "merged" {
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

func TestClientReadyAndRework(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Drafted", Priority: "medium", Kind: "feature", Draft: true,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.State != "draft" {
		t.Fatalf("created state = %q, want draft", created.State)
	}

	readied, _, err := c.ReadyTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("ReadyTask: %v", err)
	}
	if readied.State != "ready" {
		t.Fatalf("ReadyTask state = %q, want ready", readied.State)
	}
	// Publishing again is an illegal transition.
	if _, _, err := c.ReadyTask(ctx, created.ID); err == nil {
		t.Fatalf("second ReadyTask succeeded, want error")
	}

	if _, _, err := c.ClaimTask(ctx, created.ID, "host:/wt", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	moveToReview(t, st, created.ID)
	reworked, _, err := c.ReworkTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("ReworkTask: %v", err)
	}
	if reworked.State != "in_progress" {
		t.Fatalf("ReworkTask state = %q, want in_progress", reworked.State)
	}
}

func TestClientReopen(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	abandonTarget, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Abandoned then reopened", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := c.AbandonTask(ctx, abandonTarget.ID); err != nil {
		t.Fatalf("AbandonTask: %v", err)
	}
	reopened, _, err := c.ReopenTask(ctx, abandonTarget.ID)
	if err != nil {
		t.Fatalf("ReopenTask from abandoned: %v", err)
	}
	if reopened.State != "ready" {
		t.Fatalf("ReopenTask state = %q, want ready", reopened.State)
	}

	// Reopening a task that is already ready is an illegal transition.
	if _, _, err := c.ReopenTask(ctx, abandonTarget.ID); err == nil {
		t.Fatalf("ReopenTask from ready succeeded, want error")
	}
}

func TestClientBlockUnblock(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
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

func TestClientBriefAndRebindWorktree(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	// No lease yet: brief.Lease is nil, open_blockers is an empty (non-nil) slice.
	brief, _, err := c.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief (no lease): %v", err)
	}
	if brief.Task.ID != task.ID || brief.Branch != task.ID+"-fix-the-thing" {
		t.Fatalf("Brief = %+v, want task %s branch %s-fix-the-thing", brief, task.ID, task.ID)
	}
	if brief.Lease != nil {
		t.Fatalf("Brief.Lease = %+v, want nil", brief.Lease)
	}
	if brief.OpenBlockers == nil || len(brief.OpenBlockers) != 0 {
		t.Fatalf("Brief.OpenBlockers = %+v, want empty non-nil slice", brief.OpenBlockers)
	}

	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	brief, _, err = c.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief (leased): %v", err)
	}
	if brief.Lease == nil || brief.Lease.Worktree != "host:/wt-1" {
		t.Fatalf("Brief.Lease = %+v, want worktree host:/wt-1", brief.Lease)
	}

	moved, _, err := c.RebindWorktree(ctx, task.ID, "host:/wt-2")
	if err != nil {
		t.Fatalf("RebindWorktree: %v", err)
	}
	if moved.Worktree != "host:/wt-2" || moved.TaskID != task.ID {
		t.Fatalf("RebindWorktree result = %+v, want worktree host:/wt-2 task %s", moved, task.ID)
	}
	brief, _, err = c.Brief(ctx, task.ID)
	if err != nil {
		t.Fatalf("Brief (after rebind): %v", err)
	}
	if brief.Lease == nil || brief.Lease.Worktree != "host:/wt-2" {
		t.Fatalf("Brief.Lease after rebind = %+v, want worktree host:/wt-2", brief.Lease)
	}
}

func TestClientInboxFlow(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, _, err := c.AddRepo(ctx, "proj", "acme/widgets", ""); err != nil {
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

	list, _, err := c.ListIssues(ctx, "new", "")
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

	list, _, err = c.ListIssues(ctx, "new", "")
	if err != nil {
		t.Fatalf("ListIssues after triage: %v", err)
	}
	if len(list.Issues) != 0 {
		t.Fatalf("ListIssues after triage = %+v, want none left new", list.Issues)
	}
}

func TestClientClaimNextClaimed(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug", Concern: "security",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "host:/wt-claim-next"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !resp.Claimed {
		t.Fatalf("ClaimNext.Claimed = false, want true (resp = %+v)", resp)
	}
	if resp.Task == nil || resp.Task.ID != created.ID {
		t.Fatalf("ClaimNext.Task = %+v, want %s", resp.Task, created.ID)
	}
	if resp.Task.Lease == nil || resp.Task.Lease.Worktree != "host:/wt-claim-next" {
		t.Fatalf("ClaimNext.Task.Lease = %+v", resp.Task.Lease)
	}
}

func TestClientClaimNextNoneReady(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "host:/wt-empty"})
	if err != nil {
		t.Fatalf("ClaimNext with no ready tasks: err = %v, want nil (exit-0 contract)", err)
	}
	if resp.Claimed {
		t.Fatalf("ClaimNext.Claimed = true, want false")
	}
	if resp.Reason != "no-ready-task" {
		t.Fatalf("ClaimNext.Reason = %q, want %q", resp.Reason, "no-ready-task")
	}
}

func TestClientClaimNextDryRun(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Dry run me", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, cli.ClaimNextInput{DryRun: true})
	if err != nil {
		t.Fatalf("ClaimNext (dry-run): %v", err)
	}
	if resp.Claimed {
		t.Fatalf("ClaimNext.Claimed = true on dry-run, want false")
	}
	if !resp.DryRun {
		t.Fatalf("ClaimNext.DryRun = false, want true")
	}
	if resp.Task == nil || resp.Task.ID != created.ID {
		t.Fatalf("ClaimNext.Task = %+v, want %s", resp.Task, created.ID)
	}
	if resp.Task.Lease != nil {
		t.Fatalf("ClaimNext.Task.Lease = %+v on dry-run, want nil", resp.Task.Lease)
	}

	// The task must still be claimable: a real claim-next afterward still
	// finds it (no lease was actually taken).
	real, _, err := c.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "host:/wt-after-dry-run"})
	if err != nil {
		t.Fatalf("ClaimNext (real, after dry-run): %v", err)
	}
	if !real.Claimed || real.Task == nil || real.Task.ID != created.ID {
		t.Fatalf("ClaimNext (real, after dry-run) = %+v, want claimed %s", real, created.ID)
	}
}

func TestClientCreateTaskWithConcern(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Concerned", Priority: "medium", Kind: "feature", Concern: "usability",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if created.Concern != "usability" {
		t.Errorf("created task concern = %q, want %q", created.Concern, "usability")
	}
	stored, err := st.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("store.GetTask: %v", err)
	}
	if stored.Concern != "usability" {
		t.Fatalf("stored task concern = %q, want %q", stored.Concern, "usability")
	}
}

func TestClientEditTask(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Editable", Priority: "low", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	concern := "security"
	priority := "critical"
	needsDecomp := true
	edited, _, err := c.EditTask(ctx, created.ID, cli.EditTaskInput{
		Concern: &concern, Priority: &priority, NeedsDecomposition: &needsDecomp,
	})
	if err != nil {
		t.Fatalf("EditTask: %v", err)
	}
	if edited.Priority != "critical" {
		t.Fatalf("edited.Priority = %q, want critical", edited.Priority)
	}
	if edited.Concern != "security" || !edited.NeedsDecomposition {
		t.Errorf("edited task concern/needs_decomposition = %q/%v, want security/true",
			edited.Concern, edited.NeedsDecomposition)
	}
	stored, err := st.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("store.GetTask: %v", err)
	}
	if stored.Concern != "security" || !stored.NeedsDecomposition {
		t.Fatalf("stored task after edit = %+v", stored)
	}

	// Clear the concern with "none".
	none := "none"
	if _, _, err := c.EditTask(ctx, created.ID, cli.EditTaskInput{Concern: &none}); err != nil {
		t.Fatalf("EditTask clear concern: %v", err)
	}
	stored, err = st.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatalf("store.GetTask after clear: %v", err)
	}
	if stored.Concern != "" {
		t.Fatalf("stored task concern after clear = %q, want empty", stored.Concern)
	}
}

func TestClientProjectFocus(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	set, _, err := c.SetProjectFocus(ctx, "proj", []string{"security", "completeness"})
	if err != nil {
		t.Fatalf("SetProjectFocus: %v", err)
	}
	if len(set.Focus) != 2 || set.Focus[0] != "security" || set.Focus[1] != "completeness" {
		t.Fatalf("SetProjectFocus result = %+v", set.Focus)
	}

	got, err := c.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Focus) != 2 || got.Focus[0] != "security" || got.Focus[1] != "completeness" {
		t.Fatalf("GetProject.Focus = %+v, want ordered [security completeness]", got.Focus)
	}

	cleared, _, err := c.SetProjectFocus(ctx, "proj", []string{})
	if err != nil {
		t.Fatalf("SetProjectFocus (clear): %v", err)
	}
	if len(cleared.Focus) != 0 {
		t.Fatalf("SetProjectFocus (clear) result = %+v, want empty", cleared.Focus)
	}

	got, err = c.GetProject(ctx, "proj")
	if err != nil {
		t.Fatalf("GetProject after clear: %v", err)
	}
	if len(got.Focus) != 0 {
		t.Fatalf("GetProject.Focus after clear = %+v, want empty", got.Focus)
	}

	if _, err := c.GetProject(ctx, "nonexistent"); err == nil {
		t.Fatalf("GetProject unknown id: err = nil, want error")
	}
}

func TestClientErrorRendering(t *testing.T) {
	err := &cli.ClientError{Status: 404, Msg: "task WL-9 not found"}
	want := "server error (404): task WL-9 not found"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	_, c, _ := newTestServer(t)
	_, _, err2 := c.GetTask(context.Background(), "WL-99")
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
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_SERVER", "")
	t.Setenv("LODE_TOKEN", "")

	cfgDir := filepath.Join(dir, ".config", "worklode")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	cfgFile := filepath.Join(cfgDir, "config.toml")
	content := "# a comment\nserver = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n\n"
	if err := os.WriteFile(cfgFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (file only): %v", err)
	}
	if cfg.ServerURL != "https://file.example.com" || cfg.Token != "wl_filetoken" {
		t.Fatalf("LoadConfig (file only) = %+v", cfg)
	}

	t.Setenv("LODE_SERVER", "https://env.example.com")
	t.Setenv("LODE_TOKEN", "wl_envtoken")
	cfg, err = cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig (env override): %v", err)
	}
	if cfg.ServerURL != "https://env.example.com" || cfg.Token != "wl_envtoken" {
		t.Fatalf("LoadConfig (env override) = %+v, want env values to win", cfg)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_SERVER", "https://env-only.example.com")
	t.Setenv("LODE_TOKEN", "")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig with no file: %v", err)
	}
	if cfg.ServerURL != "https://env-only.example.com" {
		t.Fatalf("LoadConfig with no file = %+v", cfg)
	}
}

func TestSaveConfigRoundTrip(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_SERVER", "")
	t.Setenv("LODE_TOKEN", "")

	want := cli.Config{ServerURL: "https://wl.example.com", Token: "wl_" + strings.Repeat("ab", 20)}
	if err := cli.SaveConfig(want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	// The file holds only the server; the token no longer touches disk.
	raw, err := cli.ReadRawConfigForTest()
	if err != nil {
		t.Fatalf("read raw config: %v", err)
	}
	if strings.Contains(raw, "token") {
		t.Fatalf("config.toml still has a token line:\n%s", raw)
	}
	// The token round-trips through the (mock) keychain.
	if got, _ := cli.NewKeychainTokenStore().Get(want.ServerURL); got != want.Token {
		t.Fatalf("keychain token = %q; want %q", got, want.Token)
	}

	// LoadConfig reconstructs the full config from file + keychain.
	got, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got.ServerURL != want.ServerURL || got.Token != want.Token {
		t.Fatalf("round-trip = %+v, want %+v", got, want)
	}
}

func TestLoadConfigResolvesTokenFromKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")
	t.Setenv("LODE_SERVER", "")

	// config.toml has only server.
	if err := cli.SaveServerOnly("https://wl.example.com"); err != nil {
		t.Fatalf("save server: %v", err)
	}
	if err := cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc"); err != nil {
		t.Fatalf("seed keychain: %v", err)
	}
	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_kc" {
		t.Fatalf("token = %q; want wl_kc", cfg.Token)
	}
}

func TestEnvTokenBeatsKeychain(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_SERVER", "https://wl.example.com")
	t.Setenv("LODE_TOKEN", "wl_env")
	_ = cli.NewKeychainTokenStore().Set("https://wl.example.com", "wl_kc")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Token != "wl_env" {
		t.Fatalf("token = %q; want wl_env (env overrides keychain)", cfg.Token)
	}
}

func TestSaveConfigWritesKeychainAndStripsLegacyToken(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")
	t.Setenv("LODE_SERVER", "")

	// Simulate a legacy cleartext config.toml with a token line.
	if err := cli.WriteRawConfigForTest("server = \"https://wl.example.com\"\ntoken = \"wl_old\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Keychain now holds the new token.
	if got, _ := cli.NewKeychainTokenStore().Get("https://wl.example.com"); got != "wl_new" {
		t.Fatalf("keychain token = %q; want wl_new", got)
	}
	// File no longer contains a token line.
	raw, _ := cli.ReadRawConfigForTest()
	if strings.Contains(raw, "token") {
		t.Fatalf("config.toml still has a token line:\n%s", raw)
	}
}

// failingTokenStore is a TokenStore whose Set always errors, for the
// keychain-write-failure path.
type failingTokenStore struct{ err error }

func (f failingTokenStore) Get(string) (string, error) { return "", f.err }
func (f failingTokenStore) Set(string, string) error   { return f.err }
func (f failingTokenStore) Delete(string) error        { return f.err }

func TestSaveConfigKeychainWriteFailureLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")
	t.Setenv("LODE_SERVER", "")

	sentinel := errors.New("keychain unavailable")
	restore := cli.SwapTokenStoreForTest(failingTokenStore{err: sentinel})
	t.Cleanup(restore)

	err := cli.SaveConfig(cli.Config{ServerURL: "https://wl.example.com", Token: "wl_new"})
	if !errors.Is(err, sentinel) {
		t.Fatalf("SaveConfig err = %v; want the keychain error", err)
	}
	// No config file must have been written when the keychain write failed.
	if _, err := cli.ReadRawConfigForTest(); !os.IsNotExist(err) {
		t.Fatalf("config file exists after keychain failure (err = %v); want none", err)
	}
}

func TestLoadConfigServerOverrideDropsLegacyFileToken(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("LODE_TOKEN", "")

	// Legacy cleartext file: server + token both point at the file server.
	if err := cli.WriteRawConfigForTest("server = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n"); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	// LODE_SERVER overrides to a different server with no keychain entry.
	t.Setenv("LODE_SERVER", "https://other.example.com")

	cfg, err := cli.LoadConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://other.example.com" {
		t.Fatalf("server = %q; want the override", cfg.ServerURL)
	}
	if cfg.Token != "" {
		t.Fatalf("token = %q; the file's legacy token must not leak onto the overridden server", cfg.Token)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	for name, content := range map[string]string{
		"missing equals": "not a key value pair\n",
		"unknown key":    "bogus = \"value\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			keyring.MockInit()
			dir := t.TempDir()
			t.Setenv("HOME", dir)
			t.Setenv("LODE_SERVER", "")
			t.Setenv("LODE_TOKEN", "")
			cfgDir := filepath.Join(dir, ".config", "worklode")
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

// writeRepoConfig writes content to <dir>/<confDir>/config.toml, creating the
// directory.
func writeRepoConfig(t *testing.T, dir, confDir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, confDir), 0o755); err != nil {
		t.Fatalf("mkdir %s/%s: %v", dir, confDir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, confDir, "config.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s/%s/config.toml: %v", dir, confDir, err)
	}
}

// repoTestHome sets up a fake $HOME with a user config and returns the home
// directory plus a nested repo working directory (<home>/git/proj/sub) to load
// from.
func repoTestHome(t *testing.T, userConfig string) (home, workDir string) {
	t.Helper()
	keyring.MockInit()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LODE_SERVER", "")
	t.Setenv("LODE_TOKEN", "")
	if userConfig != "" {
		if err := cli.WriteRawConfigForTest(userConfig); err != nil {
			t.Fatalf("write user config: %v", err)
		}
	}
	workDir = filepath.Join(home, "git", "proj", "sub")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("mkdir work dir: %v", err)
	}
	return home, workDir
}

func TestLoadConfigCurrentProjectFromUserConfig(t *testing.T) {
	_, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\ncurrent_project = \"user-proj\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "user-proj" {
		t.Fatalf("current project = %q; want user-proj", cfg.CurrentProject)
	}
}

func TestRepoConfigOverridesCurrentProject(t *testing.T) {
	for _, confDir := range []string{".worklode", ".lode"} {
		t.Run(confDir, func(t *testing.T) {
			home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\ncurrent_project = \"user-proj\"\n")
			// The repo config sits two levels above the working directory, so
			// finding it exercises the upward walk.
			writeRepoConfig(t, filepath.Join(home, "git", "proj"), confDir, "current_project = \"repo-proj\"\n")

			cfg, err := cli.LoadConfigFromForTest(workDir)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if cfg.CurrentProject != "repo-proj" {
				t.Fatalf("current project = %q; want repo-proj", cfg.CurrentProject)
			}
			if cfg.ServerURL != "https://wl.example.com" {
				t.Fatalf("server = %q; the repo config must not clear unset keys", cfg.ServerURL)
			}
		})
	}
}

func TestRepoConfigNearestWins(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	writeRepoConfig(t, filepath.Join(home, "git"), ".worklode", "current_project = \"outer\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "current_project = \"inner\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "inner" {
		t.Fatalf("current project = %q; want inner (nearest config wins)", cfg.CurrentProject)
	}
}

func TestRepoConfigWorklodeBeatsLodeAtSameLevel(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "current_project = \"from-worklode\"\n")
	writeRepoConfig(t, repo, ".lode", "current_project = \"from-lode\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "from-worklode" {
		t.Fatalf("current project = %q; want from-worklode", cfg.CurrentProject)
	}
}

func TestRepoConfigWalkStopsAtHome(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	// A .worklode in $HOME itself is not a repo config and must be ignored.
	writeRepoConfig(t, home, ".worklode", "current_project = \"home-proj\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "" {
		t.Fatalf("current project = %q; the walk must stop before $HOME", cfg.CurrentProject)
	}
}

func TestRepoConfigRejectsToken(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "token = \"wl_leaked\"\n")

	_, err := cli.LoadConfigFromForTest(workDir)
	if err == nil {
		t.Fatal("load with a token in the repo config: err = nil, want error")
	}
	if !strings.Contains(err.Error(), "must not set a token") {
		t.Fatalf("err = %v; want it to explain that repo configs may not set a token", err)
	}
}

func TestRepoConfigServerOverrideDropsLegacyFileToken(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://file.example.com\"\ntoken = \"wl_filetoken\"\n")
	writeRepoConfig(t, filepath.Join(home, "git", "proj"), ".worklode", "server = \"https://repo.example.com\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://repo.example.com" {
		t.Fatalf("server = %q; want the repo config's server", cfg.ServerURL)
	}
	if cfg.Token != "" {
		t.Fatalf("token = %q; the user config's legacy token must not leak onto the repo's server", cfg.Token)
	}
}

func TestSaveServerOnlyPreservesCurrentProject(t *testing.T) {
	keyring.MockInit()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LODE_SERVER", "")
	t.Setenv("LODE_TOKEN", "")

	if err := cli.WriteRawConfigForTest("server = \"https://old.example.com\"\ncurrent_project = \"keepme\"\n"); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := cli.SaveServerOnly("https://new.example.com"); err != nil {
		t.Fatalf("save: %v", err)
	}
	cfg, err := cli.LoadConfigFromForTest("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ServerURL != "https://new.example.com" || cfg.CurrentProject != "keepme" {
		t.Fatalf("config after SaveServerOnly = %+v; want the new server and current_project kept", cfg)
	}
}

func TestCurrentProjectPathRecordsSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	userDir := filepath.Join(home, ".config", "worklode")
	if err := os.MkdirAll(userDir, 0o755); err != nil {
		t.Fatalf("mkdir user config: %v", err)
	}
	userPath := filepath.Join(userDir, "config.toml")
	if err := os.WriteFile(userPath, []byte("current_project = \"from-user\"\n"), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	cfg, err := cli.LoadConfigFromForTest(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.CurrentProject != "from-user" || cfg.CurrentProjectPath != userPath {
		t.Fatalf("user config: project=%q path=%q; want from-user, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, userPath)
	}

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".worklode"), 0o755); err != nil {
		t.Fatalf("mkdir repo config: %v", err)
	}
	repoPath := filepath.Join(repo, ".worklode", "config.toml")
	if err := os.WriteFile(repoPath, []byte("current_project = \"from-repo\"\n"), 0o600); err != nil {
		t.Fatalf("write repo config: %v", err)
	}

	cfg, err = cli.LoadConfigFromForTest(repo)
	if err != nil {
		t.Fatalf("load from repo: %v", err)
	}
	if cfg.CurrentProject != "from-repo" || cfg.CurrentProjectPath != repoPath {
		t.Fatalf("repo config: project=%q path=%q; want from-repo, %s",
			cfg.CurrentProject, cfg.CurrentProjectPath, repoPath)
	}
}

// WorktreeDirFrom, not LoadConfig/loadConfigFrom, is the sole reader of
// worktree_dir (spec 030 §4 scopes it to the repo-local config only) — see
// Config.WorktreeDir's doc. These two tests exercise it directly.

func TestWorktreeDirFromRepoConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	if got := cli.WorktreeDirFrom(repo); got != "wtrees" {
		t.Errorf("WorktreeDirFrom = %q, want wtrees", got)
	}
}

func TestWorktreeDirEnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LODE_WORKTREE_DIR", "from-env")
	// LODE_TOKEN set but otherwise irrelevant here: WorktreeDirFrom is
	// deliberately independent of loadConfigFrom (no keychain, no token, no
	// LODE_TOKEN early return) — this pins that the env override applies
	// regardless of unrelated client-config env state, not just in isolation.
	t.Setenv("LODE_TOKEN", "wl_"+strings.Repeat("a", 40))
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"wtrees\"\n")
	if got := cli.WorktreeDirFrom(repo); got != "from-env" {
		t.Errorf("WorktreeDirFrom = %q, want from-env", got)
	}
}

// TestLoadConfigFromNeverPopulatesWorktreeDir pins the invariant that keeps
// a user-level worktree_dir from diverging from what internal/hookrun's guard
// sees: loadConfigFrom (LoadConfig's implementation) must leave
// Config.WorktreeDir empty even when BOTH a user-level and a repo-level
// config set worktree_dir — WorktreeDirFrom, not this merged Config, is the
// sole reader (spec 030 §4; see Config.WorktreeDir's doc). Today this is
// correct only by inspection (cfg.WorktreeDir = "" in loadConfigFrom, and
// merge() never touching it); this test would fail if either of those broke.
func TestLoadConfigFromNeverPopulatesWorktreeDir(t *testing.T) {
	home, workDir := repoTestHome(t, "server = \"https://wl.example.com\"\nworktree_dir = \"user-wtrees\"\n")
	repo := filepath.Join(home, "git", "proj")
	writeRepoConfig(t, repo, ".worklode", "worktree_dir = \"repo-wtrees\"\n")

	cfg, err := cli.LoadConfigFromForTest(workDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.WorktreeDir != "" {
		t.Fatalf("Config.WorktreeDir = %q, want \"\" (worktree_dir must never merge into Config; use WorktreeDirFrom)", cfg.WorktreeDir)
	}
}

func TestResolveRemoteSendsRawURL(t *testing.T) {
	var gotPath, gotRemote string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotRemote = r.URL.Query().Get("remote")
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"worklode","name":"Worklode","key":"WL","repos":[],"focus":[]}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	p, err := c.ResolveRemote(context.Background(), "git@github.com:sunstoneinstitute/worklode.git")
	if err != nil {
		t.Fatalf("ResolveRemote: %v", err)
	}
	if gotPath != "/api/v1/projects/resolve" {
		t.Fatalf("path = %q; want /api/v1/projects/resolve", gotPath)
	}
	if gotRemote != "git@github.com:sunstoneinstitute/worklode.git" {
		t.Fatalf("remote = %q; want the raw URL unmodified", gotRemote)
	}
	if p.ID != "worklode" || p.Key != "WL" {
		t.Fatalf("project = %+v; want worklode/WL", p)
	}
}

func TestResolveRemoteNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"not found"}`)
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "wl_test"})
	if _, err := c.ResolveRemote(context.Background(), "git@github.com:acme/nope.git"); err == nil {
		t.Fatal("ResolveRemote on an unmapped repo returned nil error")
	}
}

func TestClientAgentSession(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()

	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{
		Project: "proj", Title: "Agent session task", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	sess, _, err := c.TouchAgentSession(ctx, task.ID, "claude-code", "2.0.1", "sess-1")
	if err != nil {
		t.Fatalf("TouchAgentSession: %v", err)
	}
	if sess.Agent != "claude-code" || sess.SessionID != "sess-1" || sess.AgentVersion != "2.0.1" {
		t.Fatalf("TouchAgentSession = %+v", sess)
	}
	if sess.LeaseID == 0 {
		t.Fatalf("TouchAgentSession lease id = 0: %+v", sess)
	}
	if sess.EndedAt != nil {
		t.Fatalf("a new session is already ended: %+v", sess)
	}
	if sess.LastSeenAt.Before(sess.StartedAt) {
		t.Fatalf("last_seen_at before started_at: %+v", sess)
	}

	if err := c.EndAgentSession(ctx, task.ID,
		cli.EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"}); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}

	// A session that was never reported cannot be ended.
	err = c.EndAgentSession(ctx, task.ID,
		cli.EndAgentSessionInput{Agent: "claude-code", SessionID: "never-seen"})
	if err == nil {
		t.Fatal("EndAgentSession on an unknown session id succeeded")
	}

	// Ending an already-ended session is also an error (guarded by
	// ended_at IS NULL in the store).
	err = c.EndAgentSession(ctx, task.ID,
		cli.EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"})
	if err == nil {
		t.Fatal("EndAgentSession on an already-ended session succeeded")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) || clientErr.Status != 404 {
		t.Fatalf("EndAgentSession on already-ended session error = %v, want *cli.ClientError with status 404", err)
	}
}

func TestClientSkillsList(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")
	seedSkill(t, st, "debugging", "Systematic debugging loop")

	skills, raw, err := c.Skills(context.Background())
	if err != nil {
		t.Fatalf("Skills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("Skills = %+v, want 2 entries", skills)
	}
	if len(raw) == 0 {
		t.Fatal("Skills: raw body empty")
	}
	names := map[string]cli.Skill{}
	for _, sk := range skills {
		names[sk.Name] = sk
	}
	if names["tdd"].Hash != "h-tdd" || names["tdd"].Description != "Red-green-refactor discipline" {
		t.Fatalf("Skills[tdd] = %+v", names["tdd"])
	}
}

func TestClientSkillGet(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	sk, raw, err := c.Skill(context.Background(), "tdd")
	if err != nil {
		t.Fatalf("Skill: %v", err)
	}
	if sk.Name != "tdd" || sk.Hash != "h-tdd" {
		t.Fatalf("Skill = %+v", sk)
	}
	if len(raw) == 0 {
		t.Fatal("Skill: raw body empty")
	}

	if _, _, err := c.Skill(context.Background(), "nope"); err == nil {
		t.Fatal("Skill(nope): want error, got nil")
	}
}

func TestClientSkillArchive(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	data, err := c.SkillArchive(context.Background(), "tdd", "h-tdd")
	if err != nil {
		t.Fatalf("SkillArchive: %v", err)
	}
	if string(data) != "gzip-archive-tdd" {
		t.Fatalf("SkillArchive = %q, want %q", data, "gzip-archive-tdd")
	}

	if _, err := c.SkillArchive(context.Background(), "tdd", "wrong-hash"); err == nil {
		t.Fatal("SkillArchive(wrong hash): want error, got nil")
	}
}

func TestClientRecommendSkills(t *testing.T) {
	st, c, _ := newTestServer(t)
	seedSkill(t, st, "tdd", "Red-green-refactor discipline")

	rec, raw, err := c.RecommendSkills(context.Background(), "", "write tests first", 5)
	if err != nil {
		t.Fatalf("RecommendSkills: %v", err)
	}
	if rec.Provider != "none" {
		t.Fatalf("RecommendSkills.Provider = %q, want none", rec.Provider)
	}
	if len(raw) == 0 {
		t.Fatal("RecommendSkills: raw body empty")
	}

	// Neither task nor text: the server 422s, surfaced as a ClientError.
	if _, _, err := c.RecommendSkills(context.Background(), "", "", 5); err == nil {
		t.Fatal("RecommendSkills(neither): want error, got nil")
	}
}

func TestClientSyncSkills(t *testing.T) {
	_, c, _ := newTestServer(t)

	// alice (the newTestServer token) is an admin, but api.Config{} has no
	// skill sources configured, so this exercises the path/method wiring
	// and surfaces the server's own 422 rather than a decode error.
	_, err := c.SyncSkills(context.Background())
	if err == nil {
		t.Fatal("SyncSkills with no sources configured: want error, got nil")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) || clientErr.Status != 422 {
		t.Fatalf("SyncSkills error = %v, want *cli.ClientError with status 422", err)
	}
}

func TestClientHierarchyCalls(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotPath, gotMethod, gotBody = r.URL.RequestURI(), r.Method, string(body)
		if r.Method == http.MethodDelete {
			// Matches the real removeEdge: 204, no body.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"epic":{"id":"WL-1","kind":"epic"},"children":[{"id":"WL-2"}]}`))
	}))
	defer srv.Close()
	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"})

	if _, err := c.Parent(context.Background(), "WL-2", "WL-1"); err != nil {
		t.Fatalf("Parent: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-2/edges" {
		t.Fatalf("Parent hit %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"child_of"`) || !strings.Contains(gotBody, `"to":"WL-1"`) {
		t.Fatalf("Parent body = %s", gotBody)
	}

	if _, err := c.Unparent(context.Background(), "WL-2", "WL-1"); err != nil {
		t.Fatalf("Unparent: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/v1/tasks/WL-2/edges" {
		t.Fatalf("Unparent hit %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"to":"WL-1"`) {
		t.Fatalf("Unparent body = %s, want to:WL-1 (not from, which would invert the edge)", gotBody)
	}

	resp, _, err := c.Decompose(context.Background(), "WL-1", []string{"A"})
	if err != nil {
		t.Fatalf("Decompose: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-1/decompose" {
		t.Fatalf("Decompose hit %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(gotBody, `"into":["A"]`) {
		t.Fatalf("Decompose body = %s, want into:[A]", gotBody)
	}
	if resp.Epic.Kind != "epic" || len(resp.Children) != 1 {
		t.Fatalf("Decompose response = %+v", resp)
	}
}

func TestImportInbox(t *testing.T) {
	var gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/inbox/import" {
			t.Errorf("path = %q, want /api/v1/inbox/import", r.URL.Path)
		}
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"repo":   "acme/widgets",
			"issues": map[string]int{"new": 3, "updated": 1},
			"prs":    map[string]int{"new": 0, "updated": 0},
		})
	}))
	defer srv.Close()

	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"})
	got, _, err := c.ImportInbox(context.Background(), cli.ImportInput{
		Repo: "acme/widgets", State: "open", IncludePRs: true, DryRun: true,
	})
	if err != nil {
		t.Fatalf("ImportInbox: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if got.Issues.New != 3 || got.Issues.Updated != 1 {
		t.Fatalf("counts = %+v, want new=3 updated=1", got.Issues)
	}
	if gotBody["state"] != "open" || gotBody["include_prs"] != true || gotBody["dry_run"] != true {
		t.Fatalf("request body = %v, want state/include_prs/dry_run carried through", gotBody)
	}
	if _, ok := gotBody["since"]; ok {
		t.Fatalf("request body = %v, want no since key when Since is nil", gotBody)
	}

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, _, err := c.ImportInbox(context.Background(), cli.ImportInput{
		Repo: "acme/widgets", Since: &since,
	}); err != nil {
		t.Fatalf("ImportInbox with Since: %v", err)
	}
	if gotBody["since"] != "2026-01-01T00:00:00Z" {
		t.Fatalf("request body since = %v, want 2026-01-01T00:00:00Z", gotBody["since"])
	}
}

// TestClientAssignmentCalls asserts the exact method, path, and body each
// assignment-verb client method issues, against a fake server that always
// answers with a fixed task JSON.
func TestClientAssignmentCalls(t *testing.T) {
	var gotMethod, gotPath, gotBody, gotAssigneeParam string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotBody = r.Method, r.URL.Path, string(body)
		// r.URL.Path excludes the query string, so the assignee filter has to
		// be read separately or nothing here would notice it going missing.
		gotAssigneeParam = r.URL.Query().Get("assignee")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"WL-1","assignee":"alice"}`))
	}))
	defer srv.Close()
	c := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: "t"})
	ctx := context.Background()

	t.Run("AssignTask with explicit assignee", func(t *testing.T) {
		task, _, err := c.AssignTask(ctx, "WL-1", "bob")
		if err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-1/assign" {
			t.Fatalf("AssignTask hit %s %s", gotMethod, gotPath)
		}
		if !strings.Contains(gotBody, `"assignee":"bob"`) {
			t.Fatalf("AssignTask body = %s, want assignee=bob", gotBody)
		}
		if task.ID != "WL-1" || task.Assignee != "alice" {
			t.Fatalf("AssignTask decoded task = %+v", task)
		}
	})

	t.Run("AssignTask defaults to self (empty assignee)", func(t *testing.T) {
		if _, _, err := c.AssignTask(ctx, "WL-1", ""); err != nil {
			t.Fatalf("AssignTask: %v", err)
		}
		if !strings.Contains(gotBody, `"assignee":""`) {
			t.Fatalf("AssignTask body = %s, want empty assignee carried through", gotBody)
		}
	})

	t.Run("UnassignTask", func(t *testing.T) {
		if _, _, err := c.UnassignTask(ctx, "WL-1"); err != nil {
			t.Fatalf("UnassignTask: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-1/unassign" {
			t.Fatalf("UnassignTask hit %s %s", gotMethod, gotPath)
		}
	})

	t.Run("StartTask", func(t *testing.T) {
		if _, _, err := c.StartTask(ctx, "WL-1"); err != nil {
			t.Fatalf("StartTask: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-1/start" {
			t.Fatalf("StartTask hit %s %s", gotMethod, gotPath)
		}
	})

	t.Run("StopTask", func(t *testing.T) {
		if _, _, err := c.StopTask(ctx, "WL-1"); err != nil {
			t.Fatalf("StopTask: %v", err)
		}
		if gotMethod != http.MethodPost || gotPath != "/api/v1/tasks/WL-1/stop" {
			t.Fatalf("StopTask hit %s %s", gotMethod, gotPath)
		}
	})

	t.Run("SubmitTask", func(t *testing.T) {
		if _, _, err := c.SubmitTask(ctx, "WL-1"); err != nil {
			t.Fatalf("SubmitTask: %v", err)
		}
		if gotMethod != http.MethodPatch || gotPath != "/api/v1/tasks/WL-1" {
			t.Fatalf("SubmitTask hit %s %s", gotMethod, gotPath)
		}
		if !strings.Contains(gotBody, `"state":"in_review"`) {
			t.Fatalf("SubmitTask body = %s, want state=in_review", gotBody)
		}
	})

	t.Run("ListTasks with Assignee sets the assignee query param", func(t *testing.T) {
		if _, _, err := c.ListTasks(ctx, cli.TaskListFilter{Assignee: "bob"}); err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		if gotMethod != http.MethodGet {
			t.Fatalf("ListTasks method = %s, want GET", gotMethod)
		}
		if gotPath != "/api/v1/tasks" {
			t.Fatalf("ListTasks path = %s, want /api/v1/tasks", gotPath)
		}
		if gotAssigneeParam != "bob" {
			t.Fatalf("ListTasks assignee query param = %q, want bob", gotAssigneeParam)
		}
	})
}

// TestClientAssignmentFlow exercises AssignTask, UnassignTask, StartTask,
// StopTask, and SubmitTask against a real server and store, and checks
// ListTasks' Assignee filter actually narrows results.
func TestClientAssignmentFlow(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if err := st.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	if _, _, err := c.CreateProject(ctx, cli.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, cli.CreateTaskInput{Project: "proj", Title: "Assign me", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if task.Assignee != "" {
		t.Fatalf("new task assignee = %q, want empty", task.Assignee)
	}

	assigned, _, err := c.AssignTask(ctx, task.ID, "bob")
	if err != nil {
		t.Fatalf("AssignTask: %v", err)
	}
	if assigned.Assignee != "bob" {
		t.Fatalf("AssignTask result assignee = %q, want bob", assigned.Assignee)
	}

	list, _, err := c.ListTasks(ctx, cli.TaskListFilter{Project: "proj", Assignee: "bob"})
	if err != nil {
		t.Fatalf("ListTasks with Assignee: %v", err)
	}
	if len(list.Tasks) != 1 || list.Tasks[0].ID != task.ID {
		t.Fatalf("ListTasks(Assignee=bob) = %+v, want just %s", list.Tasks, task.ID)
	}
	list, _, err = c.ListTasks(ctx, cli.TaskListFilter{Project: "proj", Assignee: "someone-else"})
	if err != nil {
		t.Fatalf("ListTasks with non-matching Assignee: %v", err)
	}
	if len(list.Tasks) != 0 {
		t.Fatalf("ListTasks(Assignee=someone-else) = %+v, want none", list.Tasks)
	}

	unassigned, _, err := c.UnassignTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("UnassignTask: %v", err)
	}
	if unassigned.Assignee != "" {
		t.Fatalf("UnassignTask result assignee = %q, want empty", unassigned.Assignee)
	}

	// AssignTask("") assigns to the caller (alice), then start moves it to
	// in_progress without a lease, stop puts it back to ready keeping the
	// assignment, and submit (after a re-start) moves it to in_review.
	if _, _, err := c.AssignTask(ctx, task.ID, ""); err != nil {
		t.Fatalf("AssignTask (self): %v", err)
	}
	started, _, err := c.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if started.State != "in_progress" || started.Assignee != "alice" {
		t.Fatalf("StartTask result = %+v, want in_progress/alice", started)
	}

	stopped, _, err := c.StopTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("StopTask: %v", err)
	}
	if stopped.State != "ready" || stopped.Assignee != "alice" {
		t.Fatalf("StopTask result = %+v, want ready/alice (assignment kept)", stopped)
	}

	if _, _, err := c.StartTask(ctx, task.ID); err != nil {
		t.Fatalf("re-StartTask: %v", err)
	}
	submitted, _, err := c.SubmitTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if submitted.State != "in_review" {
		t.Fatalf("SubmitTask result state = %q, want in_review", submitted.State)
	}
}
