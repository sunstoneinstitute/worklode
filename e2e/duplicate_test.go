//go:build e2e

package e2e

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestDuplicateLoop exercises 004 §1.3's duplicate_of end-to-end through
// public surfaces only: a triager marks the second filing of a request as a
// duplicate of the canonical one, both directions render on the task pages,
// the marking absorbs nothing and gates nothing, and a duplicate names only
// one canonical task.
func TestDuplicateLoop(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken, WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "dupe", Name: "Duplicate", Key: "DUPE",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e duplicate", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agent := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	canonical, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "dupe", Title: "Login is broken", Priority: "high", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("create canonical: %v", err)
	}
	second, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "dupe", Title: "Cannot log in", Priority: "medium", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("create second filing: %v", err)
	}
	third, _, err := agent.CreateTask(ctx, model.CreateTaskInput{
		Project: "dupe", Title: "Login still broken", Priority: "low", Kind: "bug",
	})
	if err != nil {
		t.Fatalf("create third filing: %v", err)
	}

	// Marking the duplicate is one call on the existing edge endpoint.
	if _, err := agent.Duplicate(ctx, second.ID, canonical.ID); err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	detail, _, err := agent.GetTask(ctx, second.ID)
	if err != nil {
		t.Fatalf("get duplicate: %v", err)
	}
	if len(detail.Edges.Out) != 1 ||
		detail.Edges.Out[0].Type != "duplicate_of" || detail.Edges.Out[0].To != canonical.ID {
		t.Fatalf("duplicate out edges = %+v, want one duplicate_of %s", detail.Edges.Out, canonical.ID)
	}

	// No absorption: the canonical task's own fields and hierarchy are
	// untouched by the marking, and the duplicate keeps its state.
	canon, _, err := agent.GetTask(ctx, canonical.ID)
	if err != nil {
		t.Fatalf("get canonical: %v", err)
	}
	if canon.Task.Priority != canonical.Priority || canon.Task.State != canonical.State ||
		canon.Hierarchy.Progress.Total != 0 || canon.Hierarchy.Parent != nil {
		t.Fatalf("canonical = %+v / hierarchy %+v, want unchanged and childless",
			canon.Task, canon.Hierarchy)
	}
	if detail.Task.State != second.State {
		t.Fatalf("duplicate state = %q, want %q: marking does not close it",
			detail.Task.State, second.State)
	}

	// A duplicate names exactly one canonical task.
	if _, err := agent.Duplicate(ctx, second.ID, third.ID); err == nil {
		t.Fatalf("second canonical accepted, want a conflict")
	}

	// Gates nothing: both tasks are claimable while the other is open.
	if _, _, err := agent.ClaimTask(ctx, canonical.ID, "wt-e2e-canonical", 0); err != nil {
		t.Fatalf("claim canonical: %v", err)
	}
	if _, _, err := agent.ClaimTask(ctx, second.ID, "wt-e2e-duplicate", 0); err != nil {
		t.Fatalf("claim duplicate while canonical is open: %v", err)
	}

	// Both directions render on the plain task pages.
	status, body := getPage(t, srv.URL+"/tasks/"+second.ID)
	if status != 200 {
		t.Fatalf("duplicate page status = %d", status)
	}
	if !strings.Contains(body, "Duplicate of") || !strings.Contains(body, canonical.ID) {
		t.Fatalf("duplicate page missing \"Duplicate of\" / canonical id %s:\n%s", canonical.ID, body)
	}
	status, body = getPage(t, srv.URL+"/tasks/"+canonical.ID)
	if status != 200 {
		t.Fatalf("canonical page status = %d", status)
	}
	if !strings.Contains(body, "Duplicates") || !strings.Contains(body, second.ID) {
		t.Fatalf("canonical page missing \"Duplicates\" / duplicate id %s:\n%s", second.ID, body)
	}

	// Unduplicate removes the edge.
	if _, err := agent.Unduplicate(ctx, second.ID, canonical.ID); err != nil {
		t.Fatalf("unduplicate: %v", err)
	}
	detail, _, err = agent.GetTask(ctx, second.ID)
	if err != nil {
		t.Fatalf("get duplicate after unduplicate: %v", err)
	}
	if len(detail.Edges.Out) != 0 {
		t.Fatalf("duplicate out edges after unduplicate = %+v, want none", detail.Edges.Out)
	}
}
