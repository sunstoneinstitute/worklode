//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestFollowUpLoop exercises 004 §1.3 end-to-end through public surfaces
// only: an agent working a task files a follow-up in one call, the edge
// shows on both task pages, and — the property that separates follow_up_to
// from blocks — the follow-up is claimable while its origin is still open.
func TestFollowUpLoop(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "fllw", Name: "Follow", Key: "FLLW",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, cli.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e followup", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	// 1-2. The origin task, then a follow-up filed against it in the same
	// request that creates it.
	origin, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "fllw", Title: "Ship the thing", Priority: "medium", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create origin: %v", err)
	}
	followUp, _, err := agent.CreateTask(ctx, cli.CreateTaskInput{
		Project: "fllw", Title: "Loose end found while shipping", Priority: "medium",
		Kind: "chore", FollowUpTo: origin.ID,
	})
	if err != nil {
		t.Fatalf("create follow-up: %v", err)
	}

	// 3. The follow-up carries exactly one out-edge, follow_up_to -> origin.
	detail, _, err := agent.GetTask(ctx, followUp.ID)
	if err != nil {
		t.Fatalf("get follow-up: %v", err)
	}
	if len(detail.Edges.Out) != 1 ||
		detail.Edges.Out[0].Type != "follow_up_to" || detail.Edges.Out[0].To != origin.ID {
		t.Fatalf("follow-up out edges = %+v, want one follow_up_to %s", detail.Edges.Out, origin.ID)
	}

	// 4. Both tasks are claimable while the origin is still open: created
	// tasks land in the ready set directly, so claim each by id. The
	// follow-up must not be gated by its open origin — that is the property
	// that separates follow_up_to from blocks.
	if _, _, err := agent.ClaimTask(ctx, origin.ID, "wt-e2e-origin", 0); err != nil {
		t.Fatalf("claim origin: %v", err)
	}
	if _, _, err := agent.ClaimTask(ctx, followUp.ID, "wt-e2e-followup", 0); err != nil {
		t.Fatalf("claim follow-up while origin is still open: %v", err)
	}

	// 5. Both directions render on the plain (anonymous, no-login-provider)
	// task pages.
	status, body := getPage(t, srv.URL+"/tasks/"+followUp.ID)
	if status != 200 {
		t.Fatalf("follow-up page status = %d", status)
	}
	if !strings.Contains(body, "Follow-up to") || !strings.Contains(body, origin.ID) {
		t.Fatalf("follow-up page missing \"Follow-up to\" / origin id %s:\n%s", origin.ID, body)
	}
	status, body = getPage(t, srv.URL+"/tasks/"+origin.ID)
	if status != 200 {
		t.Fatalf("origin page status = %d", status)
	}
	if !strings.Contains(body, "Follow-ups") || !strings.Contains(body, followUp.ID) {
		t.Fatalf("origin page missing \"Follow-ups\" / follow-up id %s:\n%s", followUp.ID, body)
	}

	// 6. Unfollow removes the edge.
	if _, err := agent.Unfollow(ctx, followUp.ID, origin.ID); err != nil {
		t.Fatalf("unfollow: %v", err)
	}
	detail, _, err = agent.GetTask(ctx, followUp.ID)
	if err != nil {
		t.Fatalf("get follow-up after unfollow: %v", err)
	}
	if len(detail.Edges.Out) != 0 {
		t.Fatalf("follow-up out edges after unfollow = %+v, want none", detail.Edges.Out)
	}
}
