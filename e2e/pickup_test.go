//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestPickupLoop exercises the spec-02 ranked pickup loop end-to-end through
// public surfaces only (the cli.Client HTTP client — no direct store writes):
// project focus, the ranking key (priority, then focus-ordered concern, then
// tiebreak), blocking exclusion even for a critical+focused task, and the
// dry-run / real-claim distinction on POST /api/v1/tasks/claim-next.
func TestPickupLoop(t *testing.T) {
	ctx := context.Background()

	// 1. Stack up: fresh store, server, httptest listener.
	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "pick", Name: "Pick",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e pickup", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("create token returned empty plaintext token")
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 2. Focus: security ranks ahead of completeness.
	focused, _, err := admin.SetProjectFocus(ctx, "pick", []string{"security", "completeness"})
	if err != nil {
		t.Fatalf("set project focus: %v", err)
	}
	if !reflect.DeepEqual(focused.Focus, []string{"security", "completeness"}) {
		t.Fatalf("project focus = %+v, want [security completeness]", focused.Focus)
	}

	// 3. Fixture, created in id order A, B, C, D, blocker, E.
	taskA, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "A: critical usability", Priority: "critical", Kind: "feature", Concern: "usability",
	})
	if err != nil {
		t.Fatalf("create task A: %v", err)
	}
	taskB, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "B: high security", Priority: "high", Kind: "feature", Concern: "security",
	})
	if err != nil {
		t.Fatalf("create task B: %v", err)
	}
	taskC, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "C: high completeness", Priority: "high", Kind: "feature", Concern: "completeness",
	})
	if err != nil {
		t.Fatalf("create task C: %v", err)
	}
	taskD, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "D: medium no concern", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task D: %v", err)
	}
	blocker, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "blocker: draft", Priority: "low", Kind: "chore", Draft: true,
	})
	if err != nil {
		t.Fatalf("create blocker task: %v", err)
	}
	taskE, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "pick", Title: "E: critical security but blocked", Priority: "critical", Kind: "feature", Concern: "security",
	})
	if err != nil {
		t.Fatalf("create task E: %v", err)
	}
	if _, err := agent.Block(ctx, taskE.ID, blocker.ID); err != nil {
		t.Fatalf("block E by blocker: %v", err)
	}

	// 4. claim --next #1: A (critical bypasses focus ordering).
	resp1, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "h:/wt/0"})
	if err != nil {
		t.Fatalf("claim-next #1: %v", err)
	}
	if !resp1.Claimed || resp1.Task == nil || resp1.Task.ID != taskA.ID {
		t.Fatalf("claim-next #1 = %+v, want claimed %s", resp1, taskA.ID)
	}
	if resp1.Task.Lease == nil || resp1.Task.Lease.Worktree != "h:/wt/0" {
		t.Fatalf("claim-next #1 lease = %+v, want worktree h:/wt/0", resp1.Task.Lease)
	}
	if resp1.Task.Concern != "usability" {
		t.Fatalf("claim-next #1 concern = %q, want usability", resp1.Task.Concern)
	}
	detailA, _, err := agent.GetTask(ctx, taskA.ID)
	if err != nil {
		t.Fatalf("get task A: %v", err)
	}
	if detailA.State != "in_progress" {
		t.Fatalf("task A state = %q, want in_progress", detailA.State)
	}
	if detailA.Lease == nil || detailA.Lease.Worktree != "h:/wt/0" {
		t.Fatalf("task A lease = %+v, want worktree h:/wt/0", detailA.Lease)
	}

	// 5. claim --next #2: B (security, rank 0), distinct worktree.
	resp2, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "h:/wt/1"})
	if err != nil {
		t.Fatalf("claim-next #2: %v", err)
	}
	if !resp2.Claimed || resp2.Task == nil || resp2.Task.ID != taskB.ID {
		t.Fatalf("claim-next #2 = %+v, want claimed %s", resp2, taskB.ID)
	}
	detailB, _, err := agent.GetTask(ctx, taskB.ID)
	if err != nil {
		t.Fatalf("get task B: %v", err)
	}
	if detailB.State != "in_progress" {
		t.Fatalf("task B state = %q, want in_progress", detailB.State)
	}

	// 6. dry-run: C (completeness, rank 1), no lease taken.
	dry, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{DryRun: true})
	if err != nil {
		t.Fatalf("claim-next dry-run: %v", err)
	}
	if dry.Claimed {
		t.Fatalf("claim-next dry-run Claimed = true, want false")
	}
	if !dry.DryRun {
		t.Fatalf("claim-next dry-run DryRun = false, want true")
	}
	if dry.Task == nil || dry.Task.ID != taskC.ID {
		t.Fatalf("claim-next dry-run task = %+v, want %s", dry.Task, taskC.ID)
	}
	if dry.Task.Lease != nil {
		t.Fatalf("claim-next dry-run lease = %+v, want nil", dry.Task.Lease)
	}
	detailCAfterDry, _, err := agent.GetTask(ctx, taskC.ID)
	if err != nil {
		t.Fatalf("get task C after dry-run: %v", err)
	}
	if detailCAfterDry.State != "ready" {
		t.Fatalf("task C state after dry-run = %q, want ready", detailCAfterDry.State)
	}
	if detailCAfterDry.Lease != nil {
		t.Fatalf("task C lease after dry-run = %+v, want nil", detailCAfterDry.Lease)
	}

	// 7. claim --next #3: C for real, proving the dry-run consumed nothing.
	resp3, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "h:/wt/2"})
	if err != nil {
		t.Fatalf("claim-next #3: %v", err)
	}
	if !resp3.Claimed || resp3.Task == nil || resp3.Task.ID != taskC.ID {
		t.Fatalf("claim-next #3 = %+v, want claimed %s", resp3, taskC.ID)
	}
	detailC, _, err := agent.GetTask(ctx, taskC.ID)
	if err != nil {
		t.Fatalf("get task C: %v", err)
	}
	if detailC.State != "in_progress" {
		t.Fatalf("task C state = %q, want in_progress", detailC.State)
	}

	// 8. claim --next #4: D (no-concern task; E stays blocked/excluded).
	resp4, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "h:/wt/3"})
	if err != nil {
		t.Fatalf("claim-next #4: %v", err)
	}
	if !resp4.Claimed || resp4.Task == nil || resp4.Task.ID != taskD.ID {
		t.Fatalf("claim-next #4 = %+v, want claimed %s", resp4, taskD.ID)
	}

	// 9. claim --next #5: claimable set exhausted (A, B, C, D claimed; E
	// blocked by the still-open draft blocker) — no error, no-ready-task.
	resp5, _, err := agent.ClaimNext(ctx, cli.ClaimNextInput{Worktree: "h:/wt/4"})
	if err != nil {
		t.Fatalf("claim-next #5: err = %v, want nil (empty ready set is normal)", err)
	}
	if resp5.Claimed || resp5.Task != nil {
		t.Fatalf("claim-next #5 = %+v, want unclaimed with no task", resp5)
	}
	if resp5.Reason != "no-ready-task" {
		t.Fatalf("claim-next #5 reason = %q, want no-ready-task", resp5.Reason)
	}
}
