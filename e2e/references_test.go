//go:build e2e

// references_test.go proves the two halves of spec 029 §4 and §5 that no
// other e2e journey covers, through public surfaces only: a document created
// with no number draws one from its project's counter, an explicit number
// pushes that counter past itself, and a typed reference may cross a project
// boundary — created over the JSON API, read back from the far (deliverable)
// end, and rendered in the milestone's References section on the owning
// project's Milestones page.
package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestAllocatedNumbersAndCrossProjectReferences(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken, WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	for _, p := range []model.CreateProjectInput{
		{ID: "a", Name: "Project A", Key: "PA"},
		{ID: "b", Name: "Project B", Key: "PB"},
	} {
		if _, _, err := admin.CreateProject(ctx, p); err != nil {
			t.Fatalf("create project %s: %v", p.ID, err)
		}
	}

	// --- Part 1: allocated document numbers (029 §4) ------------------------
	// The first spec names no number and gets 1. The second reserves 7, which
	// must push the project's SPEC counter past it, so the third — naming no
	// number again — lands on 8 rather than retracing 2.

	first, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "a", Kind: "spec", Slug: "first-spec", Body: specSourceBody,
	})
	if err != nil {
		t.Fatalf("create first spec: %v", err)
	}
	if first.Number != 1 {
		t.Fatalf("first spec number = %d, want 1 (029 §4, first spec in project)", first.Number)
	}

	reserved, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "a", Kind: "spec", Number: 7, Slug: "reserved-spec", Body: specSourceBody,
	})
	if err != nil {
		t.Fatalf("create reserved spec: %v", err)
	}
	if reserved.Number != 7 {
		t.Fatalf("reserved spec number = %d, want the 7 it asked for", reserved.Number)
	}

	next, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "a", Kind: "spec", Slug: "next-spec", Body: specSourceBody,
	})
	if err != nil {
		t.Fatalf("create next spec: %v", err)
	}
	if next.Number != 8 {
		t.Fatalf("spec after the reserved 7 = %d, want 8 (the counter moves past an explicit number)", next.Number)
	}

	// Project B's counter is its own: its first spec is 1, not 9.
	otherProject, _, err := admin.CreateDoc(ctx, model.CreateDocInput{
		Project: "b", Kind: "spec", Slug: "b-first-spec", Body: specSourceBody,
	})
	if err != nil {
		t.Fatalf("create first spec in b: %v", err)
	}
	if otherProject.Number != 1 {
		t.Fatalf("project b's first spec number = %d, want 1 (counters are per project)", otherProject.Number)
	}

	// --- Part 2: a depends_on reference across a project boundary (029 §5) --

	mile, _, err := admin.CreateMilestone(ctx, "a", model.CreateMilestoneInput{
		Title: "Integration", Position: 1,
	})
	if err != nil {
		t.Fatalf("create milestone in a: %v", err)
	}

	var del model.Deliverable
	if status := postJSON(t, srv.URL+"/api/v1/projects/b/deliverables", bootstrapToken,
		model.CreateDeliverableInput{Name: "Shared dataset"}, &del); status != http.StatusCreated {
		t.Fatalf("create deliverable in b: status = %d, want 201", status)
	}
	if del.Project != "b" {
		t.Fatalf("deliverable project = %q, want b", del.Project)
	}

	var edge model.EntityEdge
	if status := postJSON(t, srv.URL+"/api/v1/references", bootstrapToken, model.EntityEdge{
		FromKind: "milestone", From: mile.ID,
		ToKind: "deliverable", To: del.ID,
		Rel: "depends_on",
	}, &edge); status != http.StatusCreated {
		t.Fatalf("create cross-project reference: status = %d, want 201", status)
	}
	if edge.From != mile.ID || edge.To != del.ID || edge.Rel != "depends_on" {
		t.Fatalf("created reference = %+v, want %s depends_on %s", edge, mile.ID, del.ID)
	}

	// Read it back from the far end: the deliverable in project B knows about
	// the milestone in project A.
	code, body := getAuthed(t, srv.URL+"/api/v1/references?kind=deliverable&id="+url.QueryEscape(del.ID), bootstrapToken)
	if code != http.StatusOK {
		t.Fatalf("GET references for %s: status = %d, want 200 (body %s)", del.ID, code, body)
	}
	var list model.ReferenceListResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode reference list: %v (body %s)", err, body)
	}
	if len(list.References) != 1 {
		t.Fatalf("references from the deliverable end = %+v, want exactly the depends_on edge", list.References)
	}
	if got := list.References[0]; got.FromKind != "milestone" || got.From != mile.ID || got.To != del.ID {
		t.Fatalf("reference from the deliverable end = %+v, want %s depends_on %s", got, mile.ID, del.ID)
	}

	// --- Part 3: the milestone's References section on A's page -------------
	// Part 1 of 029 landed no milestone detail page; the row renders in the
	// per-milestone section of the project's Milestones page.

	code, page := getPage(t, srv.URL+"/projects/a/milestones")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/a/milestones: status = %d, want 200", code)
	}
	for _, want := range []string{"References", del.ID, del.Name} {
		if !strings.Contains(page, want) {
			t.Fatalf("milestones page missing %q:\n%s", want, page)
		}
	}
}
