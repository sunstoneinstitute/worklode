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

// TestHierarchyLoop exercises spec 018 end-to-end through public surfaces only
// (the cli.Client HTTP client — no direct store writes): decompose an
// oversized task into a parent plus children, confirm the parent never
// reaches the ready set, then close the children and watch it roll up on its
// own. Since 029 §2 the container is inferred from the child_of edges, so the
// parent keeps its kind throughout.
func TestHierarchyLoop(t *testing.T) {
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
		ID: "hier", Name: "Hier", Key: "HIER",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e hierarchy", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	big, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "hier", Title: "Ship the thing", Priority: "critical", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	// 1. Decompose: the id and the kind survive, children appear as drafts.
	split, _, err := agent.Decompose(ctx, big.ID, []string{"Phase one", "Phase two"})
	if err != nil {
		t.Fatalf("decompose: %v", err)
	}
	if split.Parent.ID != big.ID || split.Parent.Kind != big.Kind {
		t.Fatalf("parent = %+v, want %s split in place keeping kind %s", split.Parent, big.ID, big.Kind)
	}
	if len(split.Children) != 2 {
		t.Fatalf("children = %d, want 2", len(split.Children))
	}
	for _, c := range split.Children {
		if c.State != "draft" {
			t.Fatalf("child %s state = %s, want draft", c.ID, c.State)
		}
		if c.Priority != "critical" {
			t.Fatalf("child %s priority = %s, want critical inherited from the parent", c.ID, c.Priority)
		}
	}

	// 2. The parent is never handed out, even ranked critical and created first
	//    (priority, then created_at, then id — it wins every tiebreak it
	//    is allowed to enter).
	for _, c := range split.Children {
		if _, _, err := agent.ReadyTask(ctx, c.ID); err != nil {
			t.Fatalf("publish %s: %v", c.ID, err)
		}
	}
	pick, _, err := agent.ClaimNext(ctx, model.ClaimNextInput{Project: "hier", DryRun: true})
	if err != nil {
		t.Fatalf("claim-next dry run: %v", err)
	}
	if pick.Task == nil {
		t.Fatalf("claim-next found no candidate, want one of the two ready children")
	}
	if pick.Task.ID == big.ID {
		t.Fatalf("claim-next picked the parent %s, want a child", big.ID)
	}
	if pick.Task.ID != split.Children[0].ID && pick.Task.ID != split.Children[1].ID {
		t.Fatalf("claim-next picked %s, want one of the children", pick.Task.ID)
	}

	// A direct claim of the parent is refused too: excluding it from the ready
	// set alone would still leave `lode task claim <parent-id>` open.
	if _, _, err := agent.ClaimTask(ctx, big.ID, "wt-e2e-parent", 0); err == nil {
		t.Fatalf("claim of parent %s succeeded, want rejection", big.ID)
	}

	// 3. Closing every child rolls the parent up on its own. Abandon is the only
	//    close the HTTP API can drive unaided — in_progress -> in_review is the
	//    GitHub PR hook's move — and all-abandoned is the roll-up case most
	//    worth proving end to end: it must not report cancelled work as
	//    shipped. The merged path is covered by the store tests.
	//
	//    Each child claims a distinct worktree identity: an active lease is
	//    unique per worktree, so reusing one string would make the second
	//    claim lose the race.
	for i, c := range split.Children {
		if _, _, err := agent.ClaimTask(ctx, c.ID, "wt-e2e-"+c.ID, 0); err != nil {
			t.Fatalf("claim %s: %v", c.ID, err)
		}
		// The first child moving out of ready is enough to start the parent.
		mid, _, err := agent.GetTask(ctx, big.ID)
		if err != nil {
			t.Fatalf("get parent after claim %d: %v", i, err)
		}
		if mid.State != "in_progress" {
			t.Fatalf("parent state after claiming child %d = %s, want in_progress", i, mid.State)
		}
		if _, _, err := agent.AbandonTask(ctx, c.ID); err != nil {
			t.Fatalf("abandon %s: %v", c.ID, err)
		}
	}

	parent, _, err := agent.GetTask(ctx, big.ID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if parent.State != "abandoned" {
		t.Fatalf("parent state = %s, want abandoned", parent.State)
	}
	if parent.Hierarchy.Progress.Closed != 2 || parent.Hierarchy.Progress.Total != 2 {
		t.Fatalf("progress = %+v, want 2/2", parent.Hierarchy.Progress)
	}

	// The children carry the breadcrumb back up.
	child, _, err := agent.GetTask(ctx, split.Children[0].ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.Hierarchy.Parent == nil || child.Hierarchy.Parent.ID != big.ID {
		t.Fatalf("child parent = %+v, want %s", child.Hierarchy.Parent, big.ID)
	}
}
