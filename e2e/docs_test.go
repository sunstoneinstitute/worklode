//go:build e2e

package e2e

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// specSourceBody is the draft spec actor A creates: real frontmatter, an H1,
// and two anchored sections whose anchors agree with their numbers (the
// server parses and lints this, per 025 §5/§6.1).
const specSourceBody = `---
status: draft
---

# Test Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 2. Model {#sec-2}

Model body.
`

// specRevisedBody is specSourceBody with sec-1a inserted and sec-2's body
// edited; sec-1 is left untouched. This is the revision that must be
// accepted: exactly one Added anchor and one Changed anchor (025 §6 rule 5).
const specRevisedBody = `---
status: accepted
---

# Test Spec

Intro.

## 1. Scope {#sec-1}

Scope body.

## 1a. Extra {#sec-1a}

Extra body.

## 2. Model {#sec-2}

Model body, revised.
`

// planSourceBody is a plan doc: no corpus number, no anchored sections
// (025 §9).
const planSourceBody = `---
status: draft
---

# Test Plan

## Task 1

Do the thing.
`

// findDocSection returns the section with the given anchor, or nil.
func findDocSection(secs []model.DocSection, anchor string) *model.DocSection {
	for i := range secs {
		if secs[i].Anchor == anchor {
			return &secs[i]
		}
	}
	return nil
}

// clientErrStatus unwraps a *cli.ClientError's HTTP status from err, failing
// the test if err is not one (every non-2xx response from the server comes
// back as one, per cli.Client.do).
func clientErrStatus(t *testing.T, err error) int {
	t.Helper()
	var ce *cli.ClientError
	if !errors.As(err, &ce) {
		t.Fatalf("err = %v (%T), want a *cli.ClientError", err, err)
	}
	return ce.Status
}

// TestDocLifecycle drives spec 025's document lifecycle through the public
// HTTP API only: a spec's draft -> accept -> revise -> accept-revision path,
// its assignee gate and its §6 anchor rules, and a plan's freely-editable
// body with acceptance still stubbed out (025 §9.2, lifted in part 3).
func TestDocLifecycle(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		WebOpen:        true,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// 1. Project and two actors, each with its own token: actor A is the
	// spec's assignee, actor B is not.
	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "docs", Name: "Docs E2E", Key: "DOCS",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	for _, id := range []string{"actor-a", "actor-b"} {
		if _, _, err := admin.CreateActor(ctx, model.CreateActorInput{
			ID: id, Kind: "human", DisplayName: id,
		}); err != nil {
			t.Fatalf("create actor %s: %v", id, err)
		}
	}
	tokA, _, err := admin.CreateToken(ctx, "actor-a", "e2e doc lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for actor-a: %v", err)
	}
	tokB, _, err := admin.CreateToken(ctx, "actor-b", "e2e doc lifecycle", nil)
	if err != nil {
		t.Fatalf("create token for actor-b: %v", err)
	}
	actorA := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tokA.Token})
	actorB := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: tokB.Token})

	// 2. Actor A creates a spec draft with two anchored sections, assigned to
	// itself — the only actor that can accept it (025 §7).
	doc, _, err := actorA.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs", Kind: "spec", Number: 1, Slug: "test-spec",
		Body: specSourceBody, Assignee: "actor-a",
	})
	if err != nil {
		t.Fatalf("create spec: %v", err)
	}
	if doc.Status != "draft" || doc.Assignee != "actor-a" {
		t.Fatalf("created doc = %+v, want draft assigned to actor-a", doc)
	}

	// 3. Actor B's accept fails: the assignee gate is 403, not a generic
	// error.
	if _, _, err := actorB.AcceptDoc(ctx, doc.ID); err == nil {
		t.Fatal("actor-b accept: want an error, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusForbidden {
		t.Fatalf("actor-b accept: status = %d, want 403 (err %v)", status, err)
	}

	// 4. Actor A accepts: status flips to accepted and both sections publish.
	accepted, _, err := actorA.AcceptDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("actor-a accept: %v", err)
	}
	if accepted.Status != "accepted" {
		t.Fatalf("accepted doc status = %q, want accepted", accepted.Status)
	}
	detail, _, err := actorA.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc after accept: %v", err)
	}
	sec1 := findDocSection(detail.Sections, "sec-1")
	sec2 := findDocSection(detail.Sections, "sec-2")
	if sec1 == nil || sec2 == nil {
		t.Fatalf("sections after accept = %+v, want sec-1 and sec-2", detail.Sections)
	}
	if !sec1.Published || !sec2.Published {
		t.Fatalf("sections after accept = %+v, want both published", detail.Sections)
	}

	// 5. Open a revision, then edit it to drop sec-2's number while keeping
	// its anchor: this is the form that reaches the diff (renumbering while
	// keeping the anchor is a lintAnchors defect refused at parse time), and
	// AcceptDocRevision must reject it citing 025 §6 rule 3.
	if _, _, err := actorA.ReviseDoc(ctx, doc.ID); err != nil {
		t.Fatalf("revise doc: %v", err)
	}
	droppedNumberBody := strings.Replace(specSourceBody, "## 2. Model {#sec-2}", "## Model {#sec-2}", 1)
	if _, _, err := actorA.UpdateDocRevision(ctx, doc.ID, droppedNumberBody); err != nil {
		t.Fatalf("update revision (dropped number): %v", err)
	}
	if _, _, err := actorA.AcceptDocRevision(ctx, doc.ID); err == nil {
		t.Fatal("accept revision with dropped number: want an error, got nil")
	} else {
		if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
			t.Fatalf("accept revision with dropped number: status = %d, want 422 (err %v)", status, err)
		}
		if !strings.Contains(err.Error(), `sec-2: renumbered from "2" to ""`) ||
			!strings.Contains(err.Error(), "025 §6 rule 3") {
			t.Fatalf("accept revision with dropped number: err = %v, want the rule 3 violation naming sec-2", err)
		}
	}

	// 6. Replace the open revision with one that adds sec-1a and edits only
	// sec-2's body: this must be accepted, landing as version 2 with
	// last_revised_in moved on exactly sec-2 (025 §6 rule 5).
	if _, _, err := actorA.UpdateDocRevision(ctx, doc.ID, specRevisedBody); err != nil {
		t.Fatalf("update revision (valid): %v", err)
	}
	landed, _, err := actorA.AcceptDocRevision(ctx, doc.ID)
	if err != nil {
		t.Fatalf("accept valid revision: %v", err)
	}
	if landed.Version != 2 {
		t.Fatalf("landed doc version = %d, want 2", landed.Version)
	}
	detail, _, err = actorA.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("get doc after revision accept: %v", err)
	}
	if detail.Version != 2 {
		t.Fatalf("doc version after revision accept = %d, want 2", detail.Version)
	}
	if len(detail.Sections) != 3 {
		t.Fatalf("sections after revision accept = %+v, want exactly 3 (sec-1, sec-1a, sec-2)", detail.Sections)
	}
	sec1 = findDocSection(detail.Sections, "sec-1")
	sec1a := findDocSection(detail.Sections, "sec-1a")
	sec2 = findDocSection(detail.Sections, "sec-2")
	if sec1 == nil || sec1a == nil || sec2 == nil {
		t.Fatalf("sections after revision accept = %+v, want sec-1, sec-1a and sec-2", detail.Sections)
	}
	if sec1.LastRevisedIn != 1 {
		t.Fatalf("sec-1 last_revised_in = %d, want 1 (untouched by the revision)", sec1.LastRevisedIn)
	}
	if sec2.LastRevisedIn != 2 {
		t.Fatalf("sec-2 last_revised_in = %d, want 2 (its body was edited)", sec2.LastRevisedIn)
	}
	if sec1a.LastRevisedIn != 2 {
		t.Fatalf("sec-1a last_revised_in = %d, want 2 (introduced in this revision)", sec1a.LastRevisedIn)
	}
	if !sec1a.Published {
		t.Fatalf("sec-1a published = false, want true (accept publishes every current anchor)")
	}

	// 7. The plan half: a plan carries no number and no anchors, its body is
	// freely editable at any status, and accepting it is still stubbed out
	// (025 §9.2 — acceptance must mint the plan's tasks, not built yet).
	plan, _, err := actorA.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs", Kind: "plan", Slug: "test-plan",
		Body: planSourceBody, Assignee: "actor-a",
	})
	if err != nil {
		t.Fatalf("create plan: %v", err)
	}
	if plan.Number != 0 {
		t.Fatalf("plan number = %d, want 0 (025 §14.3)", plan.Number)
	}
	editedPlanBody := strings.Replace(planSourceBody, "Do the thing.", "Do the other thing.", 1)
	if _, _, err := actorA.UpdateDocBody(ctx, plan.ID, editedPlanBody); err != nil {
		t.Fatalf("edit plan body while draft: %v", err)
	}
	if _, _, err := actorA.AcceptDoc(ctx, plan.ID); err == nil {
		t.Fatal("accept plan: want the 422 stub, got nil")
	} else if status := clientErrStatus(t, err); status != http.StatusUnprocessableEntity {
		t.Fatalf("accept plan: status = %d, want 422 (err %v)", status, err)
	}
}

// TestDocOperationsMetricOnMetrics proves worklode_doc_operations_total is
// visible on the admin /metrics endpoint after a real document mutation
// through the public API — the wiring serve.go relies on
// (store.WithMetrics(reg) -> the same registry -> the admin listener's
// /metrics), not just that the counter increments into an in-process
// registry nobody scrapes, which the store unit test already covers.
func TestDocOperationsMetricOnMetrics(t *testing.T) {
	ctx := context.Background()

	reg := prometheus.NewRegistry()
	st := store.OpenTestStore(t, store.WithMetrics(reg))
	main, admin, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		WebOpen:        true,
		Metrics:        reg,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(main)
	defer srv.Close()

	adminClient := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := adminClient.CreateProject(ctx, model.CreateProjectInput{
		ID: "docs-metrics", Name: "Docs Metrics", Key: "DOCM",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, _, err := adminClient.CreateDoc(ctx, model.CreateDocInput{
		Project: "docs-metrics", Kind: "plan", Slug: "metrics-plan", Body: planSourceBody,
	}); err != nil {
		t.Fatalf("create doc: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	admin.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics: status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "worklode_doc_operations_total") {
		t.Fatalf("/metrics missing worklode_doc_operations_total:\n%s", body)
	}
	if want := `worklode_doc_operations_total{op="create",outcome="ok"}`; !strings.Contains(body, want) {
		t.Fatalf("/metrics missing %s; body:\n%s", want, body)
	}
}
