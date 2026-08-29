package cli_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestClientTaskLifecycle(t *testing.T) {
	st, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
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
	abandonTarget, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Nope", Priority: "low", Kind: "chore"})
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	abandonTarget, _, err := c.CreateTask(ctx, model.CreateTaskInput{
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	blocker, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Blocker", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask blocker: %v", err)
	}
	blockee, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Blockee", Priority: "high", Kind: "feature"})
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

// TestClientFollowUpUnfollow checks FollowUp issues POST
// /api/v1/tasks/WL-2/edges with body {"to":"WL-1","type":"follow_up_to"},
// landing the edge in origin's out-edges, and Unfollow issues the same body
// with DELETE, removing it.
func TestClientFollowUpUnfollow(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	origin, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Origin", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask origin: %v", err)
	}
	followUp, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Follow-up", Priority: "high", Kind: "feature"})
	if err != nil {
		t.Fatalf("CreateTask followUp: %v", err)
	}
	if origin.ID != "WL-1" || followUp.ID != "WL-2" {
		t.Fatalf("ids = %s, %s, want WL-1, WL-2", origin.ID, followUp.ID)
	}

	if _, err := c.FollowUp(ctx, followUp.ID, origin.ID); err != nil {
		t.Fatalf("FollowUp: %v", err)
	}
	detail, _, err := c.GetTask(ctx, followUp.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	found := false
	for _, e := range detail.Edges.Out {
		if e.Type == "follow_up_to" && e.To == origin.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("out edges = %+v, want a follow_up_to edge to %s", detail.Edges.Out, origin.ID)
	}

	if _, err := c.UnfollowUp(ctx, followUp.ID, origin.ID); err != nil {
		t.Fatalf("UnfollowUp: %v", err)
	}
	detail, _, err = c.GetTask(ctx, followUp.ID)
	if err != nil {
		t.Fatalf("GetTask after unfollow: %v", err)
	}
	for _, e := range detail.Edges.Out {
		if e.Type == "follow_up_to" {
			t.Fatalf("out edges = %+v, want no follow_up_to edge after UnfollowUp", detail.Edges.Out)
		}
	}
}

// TestClientDuplicateUnduplicate checks Duplicate issues POST
// /api/v1/tasks/WL-2/edges with body {"to":"WL-1","type":"duplicate_of"} and
// Unduplicate issues the same body with DELETE. It also pins the "no
// absorption" rule of 004 §1.3 at the surface an agent actually calls: the
// canonical task is untouched by the marking.
func TestClientDuplicateUnduplicate(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	canonical, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Canonical", Priority: "high", Kind: "bug"})
	if err != nil {
		t.Fatalf("CreateTask canonical: %v", err)
	}
	dupe, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Duplicate", Priority: "high", Kind: "bug"})
	if err != nil {
		t.Fatalf("CreateTask dupe: %v", err)
	}
	if canonical.ID != "WL-1" || dupe.ID != "WL-2" {
		t.Fatalf("ids = %s, %s, want WL-1, WL-2", canonical.ID, dupe.ID)
	}

	if _, err := c.Duplicate(ctx, dupe.ID, canonical.ID); err != nil {
		t.Fatalf("Duplicate: %v", err)
	}
	detail, _, err := c.GetTask(ctx, dupe.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	found := false
	for _, e := range detail.Edges.Out {
		if e.Type == "duplicate_of" && e.To == canonical.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("out edges = %+v, want a duplicate_of edge to %s", detail.Edges.Out, canonical.ID)
	}

	// No absorption: the canonical task gains an in-edge and nothing else.
	canon, _, err := c.GetTask(ctx, canonical.ID)
	if err != nil {
		t.Fatalf("GetTask canonical: %v", err)
	}
	if canon.Task.State != canonical.State || canon.Task.Priority != canonical.Priority ||
		canon.Task.Body != canonical.Body || len(canon.Task.Skills) != len(canonical.Skills) {
		t.Fatalf("canonical = %+v, want %+v unchanged: duplicate_of absorbs nothing",
			canon.Task, canonical)
	}
	if canon.Hierarchy.Progress.Total != 0 {
		t.Fatalf("canonical progress total = %d, want 0: a duplicate is not a child",
			canon.Hierarchy.Progress.Total)
	}

	if _, err := c.Unduplicate(ctx, dupe.ID, canonical.ID); err != nil {
		t.Fatalf("Unduplicate: %v", err)
	}
	detail, _, err = c.GetTask(ctx, dupe.ID)
	if err != nil {
		t.Fatalf("GetTask after unduplicate: %v", err)
	}
	for _, e := range detail.Edges.Out {
		if e.Type == "duplicate_of" {
			t.Fatalf("out edges = %+v, want no duplicate_of edge after Unduplicate", detail.Edges.Out)
		}
	}
}

func TestClientBriefAndRebindWorktree(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug"})
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

// TestClientTaskCost round-trips TaskCost through a real claim, agent
// session, and priced usage report: --days/--children reach the server as
// from/to/children, and the returned report matches what was billed.
func TestClientTaskCost(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}
	if _, _, err := c.TouchAgentSession(ctx, task.ID, "claude-code", "", "sess-1", nil); err != nil {
		t.Fatalf("TouchAgentSession: %v", err)
	}
	// Priced by migration 0008's seeded rate for claude-sonnet-5 (standard,
	// $2/$10 per MTok before 2026-09-01): 1e6 input + 1e5 output = $3.000000.
	usage := []model.SessionUsageBucket{{
		Day: "2026-07-31", Model: "claude-sonnet-5", InputTokens: 1_000_000, OutputTokens: 100_000,
	}}
	if err := c.EndAgentSession(ctx, task.ID, model.EndAgentSessionInput{
		Agent: "claude-code", SessionID: "sess-1", Usage: usage,
	}); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}

	// Unbounded window: the usage day is inside it.
	tc, _, err := c.TaskCost(ctx, task.ID, false, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost: %v", err)
	}
	if tc.Task != task.ID || tc.IncludesChildren || tc.Sessions != 1 {
		t.Fatalf("TaskCost = %+v, want task %s, includes_children=false, sessions=1", tc, task.ID)
	}
	if len(tc.Cost.Totals) != 1 || tc.Cost.Totals[0].CostAmount != "3.000000" {
		t.Fatalf("TaskCost.Cost.Totals = %+v, want one total of 3.000000", tc.Cost.Totals)
	}

	// children=true reaches the server: no child tasks exist, so the report
	// is unchanged but the flag echoes back.
	tc, _, err = c.TaskCost(ctx, task.ID, true, time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("TaskCost (children): %v", err)
	}
	if !tc.IncludesChildren {
		t.Fatalf("TaskCost.IncludesChildren = false, want true")
	}

	// from/to clip the window past the usage day: an empty report, no error.
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	tc, _, err = c.TaskCost(ctx, task.ID, false, from, from)
	if err != nil {
		t.Fatalf("TaskCost (clipped window): %v", err)
	}
	if len(tc.Cost.Totals) != 0 || tc.Sessions != 0 {
		t.Fatalf("TaskCost (clipped window) = %+v, want empty", tc)
	}

	if _, _, err := c.TaskCost(ctx, "nosuch", false, time.Time{}, time.Time{}); err == nil {
		t.Fatalf("TaskCost unknown id: err = nil, want error")
	}
}

func TestClientClaimNextClaimed(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Fix the thing", Priority: "high", Kind: "bug", Concern: "security",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, model.ClaimNextInput{Worktree: "host:/wt-claim-next"})
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, model.ClaimNextInput{Worktree: "host:/wt-empty"})
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Dry run me", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	resp, _, err := c.ClaimNext(ctx, model.ClaimNextInput{DryRun: true})
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
	real, _, err := c.ClaimNext(ctx, model.ClaimNextInput{Worktree: "host:/wt-after-dry-run"})
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	created, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Editable", Priority: "low", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	concern := "security"
	priority := "critical"
	needsDecomp := true
	edited, _, err := c.EditTask(ctx, created.ID, model.EditTaskInput{
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
	if _, _, err := c.EditTask(ctx, created.ID, model.EditTaskInput{Concern: &none}); err != nil {
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

func TestClientAgentSession(t *testing.T) {
	_, c, _ := newTestServer(t)
	ctx := context.Background()

	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{
		Project: "proj", Title: "Agent session task", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, _, err := c.ClaimTask(ctx, task.ID, "host:/wt-1", 0); err != nil {
		t.Fatalf("ClaimTask: %v", err)
	}

	sess, _, err := c.TouchAgentSession(ctx, task.ID, "claude-code", "2.0.1", "sess-1", nil)
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
		model.EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"}); err != nil {
		t.Fatalf("EndAgentSession: %v", err)
	}

	// A session that was never reported cannot be ended.
	err = c.EndAgentSession(ctx, task.ID,
		model.EndAgentSessionInput{Agent: "claude-code", SessionID: "never-seen"})
	if err == nil {
		t.Fatal("EndAgentSession on an unknown session id succeeded")
	}

	// Ending an already-ended session is also an error (guarded by
	// ended_at IS NULL in the store).
	err = c.EndAgentSession(ctx, task.ID,
		model.EndAgentSessionInput{Agent: "claude-code", SessionID: "sess-1"})
	if err == nil {
		t.Fatal("EndAgentSession on an already-ended session succeeded")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) || clientErr.Status != 404 {
		t.Fatalf("EndAgentSession on already-ended session error = %v, want *cli.ClientError with status 404", err)
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
		w.Write([]byte(`{"parent":{"id":"WL-1","kind":"feature"},"children":[{"id":"WL-2"}]}`))
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
	if resp.Parent.Kind != "feature" || len(resp.Children) != 1 {
		t.Fatalf("Decompose response = %+v", resp)
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
	if _, _, err := c.CreateProject(ctx, model.CreateProjectInput{ID: "proj", Name: "Project", Key: "WL"}); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	store.SeedCrewForTests(t, st, "proj", "alice", "bob")
	task, _, err := c.CreateTask(ctx, model.CreateTaskInput{Project: "proj", Title: "Assign me", Priority: "high", Kind: "feature"})
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
