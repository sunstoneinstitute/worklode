//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestHumanAssignLifecycle proves the central claim of the human-assignment
// plan end to end, through public HTTP surfaces only: a human can own a task
// (assign, start, submit, done) without ever holding a worktree lease. The
// board's in_progress row for the task must show assignee set and holder
// genuinely absent — that absence, not merely a populated assignee, is what
// distinguishes assignment from a lease.
func TestHumanAssignLifecycle(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		// This test never delivers webhooks, but NewServer requires secrets.
		GitHubWebhookSecret: githubSecret,
		FluxWebhookSecret:   fluxSecret,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "human", Name: "Human", Key: "HUM",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// Two human actors, each with their own token minted through the public
	// admin API — the negative case needs the second actor's identity to
	// come from a real credential, not a borrowed one.
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "alice", Kind: "human", DisplayName: "Alice",
	}); err != nil {
		t.Fatalf("create actor alice: %v", err)
	}
	aliceTok, _, err := admin.CreateToken(ctx, "alice", "e2e human lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for alice: %v", err)
	}
	alice := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: aliceTok.Token})

	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "bob", Kind: "human", DisplayName: "Bob",
	}); err != nil {
		t.Fatalf("create actor bob: %v", err)
	}
	bobTok, _, err := admin.CreateToken(ctx, "bob", "e2e human lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for bob: %v", err)
	}
	bob := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bobTok.Token})

	// --- Positive case: the full human lifecycle -------------------------

	task, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "human", Title: "Write the onboarding doc", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.State != "ready" {
		t.Fatalf("created task state = %q, want ready", task.State)
	}

	// Explicit assignee body, not the default-to-caller path.
	assigned, _, err := admin.AssignTask(ctx, task.ID, "alice")
	if err != nil {
		t.Fatalf("assign task to alice: %v", err)
	}
	if assigned.Assignee != "alice" {
		t.Fatalf("assignee after explicit assign = %q, want alice", assigned.Assignee)
	}

	started, _, err := alice.StartTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("alice start task: %v", err)
	}
	if started.State != "in_progress" || started.Assignee != "alice" {
		t.Fatalf("started task = %+v, want in_progress assigned to alice", started)
	}

	// The board row for this task must show assignee set AND no holder —
	// the central claim of the whole plan.
	row := boardRowFor(t, ctx, alice, "human", "in_progress", task.ID)
	if row.Assignee != "alice" {
		t.Fatalf("board in_progress row assignee = %q, want alice", row.Assignee)
	}
	if row.Holder != nil {
		t.Fatalf("board in_progress row holder = %+v, want nil (no lease for a human-owned task)", row.Holder)
	}

	reviewed, _, err := alice.SubmitTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("alice submit task: %v", err)
	}
	if reviewed.State != "in_review" {
		t.Fatalf("submitted task state = %q, want in_review", reviewed.State)
	}

	done, _, err := alice.DoneTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("alice done task: %v", err)
	}
	if done.State != "merged" {
		t.Fatalf("done task state = %q, want merged", done.State)
	}

	// --- Negative case: a second actor cannot start someone else's task --

	other, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "human", Title: "Rotate the deploy keys", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create second task: %v", err)
	}
	if _, _, err := admin.AssignTask(ctx, other.ID, "alice"); err != nil {
		t.Fatalf("assign second task to alice: %v", err)
	}

	// Prove the precondition before asserting the 422: the task is really
	// ready and really assigned to alice, not bob.
	precheck, _, err := admin.GetTask(ctx, other.ID)
	if err != nil {
		t.Fatalf("get second task before bob's start: %v", err)
	}
	if precheck.State != "ready" || precheck.Assignee != "alice" {
		t.Fatalf("second task precondition = state %q assignee %q, want ready/alice", precheck.State, precheck.Assignee)
	}

	_, _, err = bob.StartTask(ctx, other.ID)
	if err == nil {
		t.Fatalf("bob start on alice's task: want error, got success")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("bob start on alice's task: error = %v, want *cli.ClientError", err)
	}
	if clientErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("bob start on alice's task: status = %d, want 422", clientErr.Status)
	}
	// Several distinct guards return 422; the body must name this specific
	// one (the assignment conflict), not just any 422.
	if !strings.Contains(clientErr.Msg, "assigned to alice") {
		t.Fatalf("bob start on alice's task: error body = %q, want it to name the assignment conflict (assigned to alice)", clientErr.Msg)
	}

	// The failed start must not have mutated the task.
	after, _, err := admin.GetTask(ctx, other.ID)
	if err != nil {
		t.Fatalf("get second task after bob's failed start: %v", err)
	}
	if after.State != "ready" || after.Assignee != "alice" {
		t.Fatalf("second task after bob's failed start = state %q assignee %q, want unchanged ready/alice", after.State, after.Assignee)
	}
}

// boardRowFor fetches the board and returns the row for taskID under the
// named project and bucket ("in_progress", "in_review", "ready", "blocked"),
// failing the test if the project, bucket, or task is not found there. This
// guards against asserting on the wrong row when other tasks share the
// board.
func boardRowFor(t *testing.T, ctx context.Context, c *cli.Client, projectID, bucket, taskID string) model.BoardTask {
	t.Helper()
	board, _, err := c.Board(ctx, projectID)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	var proj *model.BoardProject
	for i := range board.Projects {
		if board.Projects[i].ID == projectID {
			proj = &board.Projects[i]
		}
	}
	if proj == nil {
		t.Fatalf("board has no project %q: %+v", projectID, board.Projects)
	}
	var bucketTasks []model.BoardTask
	switch bucket {
	case "in_progress":
		bucketTasks = proj.InProgress
	case "in_review":
		bucketTasks = proj.InReview
	case "ready":
		bucketTasks = proj.Ready
	case "blocked":
		bucketTasks = proj.Blocked
	default:
		t.Fatalf("boardRowFor: unknown bucket %q", bucket)
	}
	for _, bt := range bucketTasks {
		if bt.ID == taskID {
			return bt
		}
	}
	t.Fatalf("board bucket %q in project %q has no task %s: %+v", bucket, projectID, taskID, bucketTasks)
	return model.BoardTask{}
}
