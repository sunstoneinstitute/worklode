//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestChangeReviewLoop drives one change review through in_review and back over
// the HTTP API (WL-REQ-1324): the lease holder opens it with a reason, the
// reviewer's change_request on a line sends the task back to in_progress with
// the lease kept, the author round re-anchors the thread as obsolete, the
// reviewer approves, and the merge closes the review. The git fixture supplies
// real shas and blobs; the server never reads it.
func TestChangeReviewLoop(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{ID: "chg", Name: "Chg", Key: "CH"}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	clients := map[string]*cli.Client{}
	for id, kind := range map[string]string{"kim": "human", "agent": "agent"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{ID: id, Kind: kind, DisplayName: id}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
		tok, _, err := admin.CreateToken(ctx, id, "e2e change review", nil)
		if err != nil {
			t.Fatalf("create token for %s: %v", id, err)
		}
		clients[id] = cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})
	}
	kim, agent := clients["kim"], clients["agent"]

	// The task's creator is a human, so the review defaults to kim (WL-REQ-1779).
	task, _, err := kim.CreateTask(ctx, model.CreateTaskInput{
		Project: "chg", Title: "Change under review", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, _, err := agent.ClaimTask(ctx, task.ID, "e2e-change-wt", 0); err != nil {
		t.Fatalf("claim: %v", err)
	}
	taskState := func() model.TaskDetail {
		t.Helper()
		d, _, err := agent.GetTask(ctx, task.ID)
		if err != nil {
			t.Fatalf("get task: %v", err)
		}
		return d
	}

	// 1. Two commits; the review opens at the first and kim comments on its line 2.
	root := initE2EGitRepo(t)
	e2eCommit(t, root, "widget.txt", "one\ntwo\nthree\n", "add widget")
	sha1 := e2eGit(t, root, "rev-parse", "HEAD")
	blob1 := e2eGit(t, root, "rev-parse", "HEAD:widget.txt")

	rev, _, err := agent.OpenReview(ctx, model.OpenReviewInput{Task: task.ID, BaseRef: sha1, Reason: "touches the claim path"})
	if err != nil {
		t.Fatalf("open change review: %v", err)
	}
	if rev.Subject != "change" || rev.State != "open" || rev.BaseRef != sha1 ||
		len(rev.Reviewers) != 1 || rev.Reviewers[0] != "kim" || rev.Reason != "touches the claim path" {
		t.Fatalf("opened review = %+v, want an open change review at %s for kim", rev, sha1)
	}
	if d := taskState(); d.State != "in_review" {
		t.Fatalf("task after open = %q, want in_review", d.State)
	}

	th, _, err := kim.OpenReviewThread(ctx, rev.ID, model.OpenThreadInput{
		Anchor: model.ReviewAnchor{Repo: "sunstoneinstitute/worklode", Path: "widget.txt", Side: "additions",
			Line: 2, AnchorText: "two", BlobSHA: blob1},
		Intent: "change_request", Body: "Drop this line.",
	})
	if err != nil {
		t.Fatalf("change_request on a line: %v", err)
	}

	// 2. The lease holder cannot approve its own change (WL-RULE-1321).
	_, _, err = agent.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{Verdict: "approved", Client: "cli"})
	if err == nil || clientErrStatus(t, err) != http.StatusForbidden {
		t.Fatalf("lease holder approves: err = %v, want 403", err)
	}

	// 3. changes_requested sends the task back with the lease still held.
	if _, _, err := kim.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{Verdict: "changes_requested", Client: "cli"}); err != nil {
		t.Fatalf("changes_requested: %v", err)
	}
	if d := taskState(); d.State != "in_progress" || d.Lease == nil || d.Lease.ActorID != "agent" {
		t.Fatalf("task after changes_requested = state %q lease %+v, want in_progress with agent's lease", d.State, d.Lease)
	}

	// 4. The author drops line 2 and submits a round; the thread is obsolete.
	e2eCommit(t, root, "widget.txt", "one\nthree\n", "drop line two")
	sha2 := e2eGit(t, root, "rev-parse", "HEAD")
	round, _, err := agent.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{
		BaseRef: sha2, Client: "cli",
		Reanchors: []model.ThreadReanchor{{Thread: th.ID, Status: "obsolete"}},
	})
	if err != nil {
		t.Fatalf("author round: %v", err)
	}
	if round.BaseRef != sha2 {
		t.Fatalf("review base_ref after author round = %q, want %s", round.BaseRef, sha2)
	}
	if d := taskState(); d.State != "in_review" {
		t.Fatalf("task after author round = %q, want in_review", d.State)
	}
	got, _, err := kim.GetReview(ctx, rev.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if len(got.Threads) != 1 || got.Threads[0].Status != "obsolete" {
		t.Fatalf("threads after author round = %+v, want one obsolete", got.Threads)
	}

	// 5. kim approves, the task merges, the review closes.
	if _, _, err := kim.SubmitReviewRound(ctx, rev.ID, model.ReviewRoundInput{Verdict: "approved", Client: "cli"}); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, err := admin.SetTaskState(ctx, task.ID, "merged"); err != nil {
		t.Fatalf("set state merged: %v", err)
	}
	closed, _, err := kim.GetReview(ctx, rev.ID)
	if err != nil {
		t.Fatalf("get closed review: %v", err)
	}
	if closed.State != "closed" || closed.ClosedAt == nil {
		t.Fatalf("review after merge = state %q closed_at %v, want closed", closed.State, closed.ClosedAt)
	}
}
