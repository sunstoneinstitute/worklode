//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestInstructionsRelay exercises the steering-instruction relay end-to-end
// through public surfaces only: an operator enqueues an instruction on a
// task the agent actor leases (POST /api/v1/tasks/{id}/instructions), the
// agent's claim call delivers it (POST /api/v1/instructions/claim), and a
// second claim on the same actor comes back empty — delivered_at makes
// delivery exactly-once from the caller's perspective even though the
// underlying claim is at-least-once (internal/store/instructions.go).
func TestInstructionsRelay(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "steer", Name: "Steer", Key: "STEER",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e instructions", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	task, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "steer", Title: "Rebase and land", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The claim route delivers instructions on tasks the actor currently
	// leases (ClaimPendingInstructionsForActor), so the agent must hold an
	// active lease before anything is enqueued.
	claim, _, err := agent.ClaimTask(ctx, task.ID, "h:/.worktrees/steer", 0)
	if err != nil {
		t.Fatalf("claim task: %v", err)
	}
	if claim.Branch == "" {
		t.Fatal("claim task returned empty branch")
	}

	// Operator enqueues a steering instruction against the leased task.
	enqueued, _, err := admin.Instruct(ctx, task.ID, "check the logs before continuing")
	if err != nil {
		t.Fatalf("instruct: %v", err)
	}
	if enqueued.Task != task.ID || enqueued.Body != "check the logs before continuing" {
		t.Fatalf("enqueued instruction = %+v, want task=%s body=%q", enqueued, task.ID, "check the logs before continuing")
	}

	// First claim: the agent receives the pending instruction.
	first, _, err := agent.ClaimInstructions(ctx)
	if err != nil {
		t.Fatalf("claim instructions #1: %v", err)
	}
	if len(first.Instructions) != 1 {
		t.Fatalf("claim instructions #1 = %+v, want exactly 1 instruction", first.Instructions)
	}
	got := first.Instructions[0]
	if got.ID != enqueued.ID || got.Task != task.ID || got.Body != "check the logs before continuing" {
		t.Fatalf("claimed instruction = %+v, want id=%d task=%s body=%q", got, enqueued.ID, task.ID, "check the logs before continuing")
	}

	// Second claim on the same actor: nothing left pending — delivery is
	// exactly-once from the caller's perspective.
	second, _, err := agent.ClaimInstructions(ctx)
	if err != nil {
		t.Fatalf("claim instructions #2: %v", err)
	}
	if len(second.Instructions) != 0 {
		t.Fatalf("claim instructions #2 = %+v, want none pending", second.Instructions)
	}
}
