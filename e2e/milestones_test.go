//go:build e2e

// milestones_test.go proves spec 029 §2's milestone end to end, through
// public surfaces only: the bootstrap-token JSON API creates milestones,
// attaches tasks and deliverables to them (and refuses a cross-project
// attach), the derived progress counts come back on the list and detail
// routes, and the two project-local web pages (Milestones, Deliverables)
// render what the API computed. A task that carries no milestone is proven
// to need no such attachment anywhere.
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

func TestMilestoneJourney(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken, WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})

	// --- Step 1: two projects, and two milestones in A ----------------------
	// B exists only so the cross-project refusal below has a real milestone
	// to point at.
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "a", Name: "Project A", Key: "PA",
	}); err != nil {
		t.Fatalf("create project a: %v", err)
	}
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "b", Name: "Project B", Key: "PB",
	}); err != nil {
		t.Fatalf("create project b: %v", err)
	}

	mile1, _, err := admin.CreateMilestone(ctx, "a", model.CreateMilestoneInput{Title: "Data collection", Position: 1})
	if err != nil {
		t.Fatalf("create milestone 1: %v", err)
	}
	if mile1.Position != 1 || !strings.HasSuffix(mile1.ID, "-MILE-1") {
		t.Fatalf("milestone 1 = %+v, want position 1, id ending -MILE-1", mile1)
	}
	mile2, _, err := admin.CreateMilestone(ctx, "a", model.CreateMilestoneInput{Title: "Analysis", Position: 2})
	if err != nil {
		t.Fatalf("create milestone 2: %v", err)
	}
	if mile2.Position != 2 || !strings.HasSuffix(mile2.ID, "-MILE-2") {
		t.Fatalf("milestone 2 = %+v, want position 2, id ending -MILE-2", mile2)
	}
	mileB, _, err := admin.CreateMilestone(ctx, "b", model.CreateMilestoneInput{Title: "B's only milestone", Position: 1})
	if err != nil {
		t.Fatalf("create milestone in b: %v", err)
	}

	// --- Step 2: two tasks in A; attach one, refuse a cross-project attach --

	task1, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "a", Title: "Collect the records", Priority: "high", Kind: "feature",
	})
	if err != nil {
		t.Fatalf("create task 1: %v", err)
	}
	task2, _, err := admin.CreateTask(ctx, model.CreateTaskInput{
		Project: "a", Title: "Runs with no milestone at all", Priority: "medium", Kind: "chore",
	})
	if err != nil {
		t.Fatalf("create task 2: %v", err)
	}

	attached, _, err := admin.EditTask(ctx, task1.ID, model.EditTaskInput{Milestone: &mile1.ID})
	if err != nil {
		t.Fatalf("attach task1 to mile1: %v", err)
	}
	if attached.Milestone != mile1.ID {
		t.Fatalf("task1 milestone = %q, want %q", attached.Milestone, mile1.ID)
	}

	_, _, err = admin.EditTask(ctx, task2.ID, model.EditTaskInput{Milestone: &mileB.ID})
	if err == nil {
		t.Fatal("attach task2 (project a) to a milestone in project b: want error, got success")
	}
	var clientErr *cli.ClientError
	if !errors.As(err, &clientErr) {
		t.Fatalf("cross-project task attach: error = %v (%T), want *cli.ClientError", err, err)
	}
	if clientErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("cross-project task attach: status = %d, want 422", clientErr.Status)
	}
	if !strings.Contains(clientErr.Msg, "cross-project") {
		t.Fatalf("cross-project task attach: body = %q, want it to name the cross-project refusal", clientErr.Msg)
	}

	// --- Step 3: two deliverables in A, one attached at declaration, one ----
	// reparented afterwards.

	var del1 model.Deliverable
	status := postJSON(t, srv.URL+"/api/v1/projects/a/deliverables", bootstrapToken,
		model.CreateDeliverableInput{Name: "Casualty dataset", Milestone: mile1.ID}, &del1)
	if status != http.StatusCreated {
		t.Fatalf("create deliverable 1: status = %d, want 201", status)
	}
	if del1.Milestone != mile1.ID {
		t.Fatalf("deliverable 1 milestone = %q, want %q at declaration", del1.Milestone, mile1.ID)
	}

	var del2 model.Deliverable
	status = postJSON(t, srv.URL+"/api/v1/projects/a/deliverables", bootstrapToken,
		model.CreateDeliverableInput{Name: "Report draft"}, &del2)
	if status != http.StatusCreated {
		t.Fatalf("create deliverable 2: status = %d, want 201", status)
	}
	if del2.Milestone != "" {
		t.Fatalf("deliverable 2 milestone = %q, want none at declaration", del2.Milestone)
	}

	reparented, _, err := admin.SetDeliverableMilestone(ctx, del2.ID, mile1.ID)
	if err != nil {
		t.Fatalf("reparent deliverable 2 onto mile1: %v", err)
	}
	if reparented.Milestone != mile1.ID {
		t.Fatalf("reparented deliverable 2 milestone = %q, want %q", reparented.Milestone, mile1.ID)
	}

	// --- Step 4: the derived progress counts on the list and detail routes --

	list, _, err := admin.ListMilestones(ctx, "a")
	if err != nil {
		t.Fatalf("list milestones: %v", err)
	}
	var m1, m2 *model.Milestone
	for i := range list.Milestones {
		switch list.Milestones[i].ID {
		case mile1.ID:
			m1 = &list.Milestones[i]
		case mile2.ID:
			m2 = &list.Milestones[i]
		}
	}
	if m1 == nil || m2 == nil {
		t.Fatalf("milestone list missing an id: %+v", list.Milestones)
	}
	if m1.Progress.TasksTotal != 1 || m1.Progress.DeliverablesTotal != 2 {
		t.Fatalf("mile1 progress = %+v, want tasks_total 1, deliverables_total 2", m1.Progress)
	}
	if (model.MilestoneProgress{}) != m2.Progress {
		t.Fatalf("mile2 progress = %+v, want all zero", m2.Progress)
	}

	detail, _, err := admin.GetMilestone(ctx, mile1.ID)
	if err != nil {
		t.Fatalf("get milestone 1: %v", err)
	}
	if len(detail.Tasks) != 1 || detail.Tasks[0].ID != task1.ID {
		t.Fatalf("mile1 detail tasks = %+v, want just task1", detail.Tasks)
	}
	if len(detail.Deliverables) != 2 {
		t.Fatalf("mile1 detail deliverables = %+v, want del1 and del2", detail.Deliverables)
	}

	// --- Step 5: the two web pages -------------------------------------------

	code, body := getPage(t, srv.URL+"/projects/a/milestones")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/a/milestones: status = %d, want 200", code)
	}
	for _, want := range []string{
		mile1.Title, mile2.Title,
		"0/1 tasks closed \u00b7 0/2 deliverables live", // mile1: nothing closed or live yet
		"0/0 tasks closed \u00b7 0/0 deliverables live", // mile2: nothing attached
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("milestones page missing %q:\n%s", want, body)
		}
	}

	// Before any detach, both deliverables sit under mile1 and none are
	// unattached, so the page has exactly one group and renders flat (no
	// milestone header) — today's plain list. Detaching del1 gives the page
	// a second, unattached group, and that's what turns the header on: the
	// milestone header and the "reappears once nullable" behavior are the
	// same page load.
	code, body = getPage(t, srv.URL+"/projects/a/deliverables")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/a/deliverables (before detach): status = %d, want 200", code)
	}
	if strings.Contains(body, "<h4>") {
		t.Fatalf("deliverables page has a group header before a second group exists:\n%s", body)
	}
	for _, want := range []string{del1.Name, del2.Name} {
		if !strings.Contains(body, want) {
			t.Fatalf("deliverables page (before detach) missing %q:\n%s", want, body)
		}
	}

	if _, _, err := admin.SetDeliverableMilestone(ctx, del1.ID, ""); err != nil {
		t.Fatalf("detach deliverable 1: %v", err)
	}

	code, body = getPage(t, srv.URL+"/projects/a/deliverables")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/a/deliverables (after detach): status = %d, want 200", code)
	}
	if !strings.Contains(body, "<h4>"+mile1.Title+"</h4>") {
		t.Fatalf("deliverables page missing the %s group header:\n%s", mile1.Title, body)
	}
	if !strings.Contains(body, "<h4>No milestone</h4>") {
		t.Fatalf("deliverables page missing the unattached group header:\n%s", body)
	}
	mileHeaderIdx := strings.Index(body, "<h4>"+mile1.Title+"</h4>")
	unattachedIdx := strings.Index(body, "<h4>No milestone</h4>")
	del1Idx := strings.Index(body, del1.Name)
	del2Idx := strings.Index(body, del2.Name)
	if !(mileHeaderIdx < del2Idx && del2Idx < unattachedIdx && unattachedIdx < del1Idx) {
		t.Fatalf("deliverables page groups out of order: mile header %d, del2 %d, unattached header %d, del1 %d:\n%s",
			mileHeaderIdx, del2Idx, unattachedIdx, del1Idx, body)
	}

	// --- Step 6: the task with no milestone stays legal everywhere ----------
	// (claiming, editing, releasing all work without ever naming one).

	if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
		ID: "agent-1", Kind: "agent", DisplayName: "Agent One",
	}); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	tok, _, err := admin.CreateToken(ctx, "agent-1", "e2e milestone journey", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	agentClient := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tok.Token})

	claim, _, err := agentClient.ClaimTask(ctx, task2.ID, "e2e-milestones", 0)
	if err != nil {
		t.Fatalf("claim task2 (no milestone): %v", err)
	}
	if claim.Branch == "" {
		t.Fatal("claim task2: empty branch")
	}
	edited, _, err := agentClient.EditTask(ctx, task2.ID, model.EditTaskInput{Concern: strPtr("performance")})
	if err != nil {
		t.Fatalf("edit task2 (no milestone): %v", err)
	}
	if edited.Milestone != "" {
		t.Fatalf("task2 milestone = %q, want none throughout", edited.Milestone)
	}
	if _, err := agentClient.ReleaseLease(ctx, task2.ID); err != nil {
		t.Fatalf("release task2: %v", err)
	}
	final, _, err := admin.GetTask(ctx, task2.ID)
	if err != nil {
		t.Fatalf("get task2 after release: %v", err)
	}
	if final.Milestone != "" {
		t.Fatalf("task2 milestone after full lifecycle = %q, want none", final.Milestone)
	}
}

func strPtr(s string) *string { return &s }
