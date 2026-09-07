package api_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// requireApproval posts one ad-hoc requirement and returns the response
// recorder, so each case below asserts on its own status.
func requireApproval(t *testing.T, h http.Handler, token string, in model.RequireApprovalInput) *model.Approval {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/approvals", token, in)
	if rr.Code != http.StatusCreated {
		t.Fatalf("require approval status = %d, body %s", rr.Code, rr.Body.String())
	}
	var a model.Approval
	decodeInto(t, rr, &a)
	return &a
}

// TestAdHocRequirementOnATask is 029 §7.2's "ad-hoc requirements can be added
// to any governed target": a task owes a review nothing in the project's flow
// demanded, on a revision the caller named, in the no-lane row.
func TestAdHocRequirementOnATask(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature",
	})["id"].(string)

	a := requireApproval(t, h, token, model.RequireApprovalInput{
		EntityKind: "task", EntityID: taskID, Role: "science-leads", Revision: "abc123",
	})
	if a.State != "awaiting" || a.SubjectRevision != "abc123" || a.Lane != "" {
		t.Errorf("row = %+v", a)
	}
	if a.RequiredRole == nil || *a.RequiredRole != "science-leads" {
		t.Errorf("required_role = %v, want science-leads", a.RequiredRole)
	}
	// created_by is the requesting actor, never the 'worklode' system actor:
	// a human filed this policy, and only rule-created rows belong to
	// worklode.
	if a.CreatedBy == nil || *a.CreatedBy != "alice" {
		t.Errorf("created_by = %v, want alice", a.CreatedBy)
	}

	// It lands in the same queue the flow's rows do.
	rr := doReq(t, h, "GET", "/api/v1/approvals", token, nil)
	var list model.ApprovalListResponse
	decodeInto(t, rr, &list)
	found := false
	for _, row := range list.Approvals {
		if row.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ad-hoc row %d is not in the awaiting queue: %+v", a.ID, list.Approvals)
	}
}

// TestAdHocRequirementOnADocument: an unqualified requirement on a document
// binds to the document's current version, and the entity_kind is 'doc' —
// the approvals table's own spelling, which is what lets the queue's doc join
// correlate the row.
func TestAdHocRequirementOnADocument(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	d := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "spec", Number: 700, Slug: "ad-hoc",
		Body: "# Ad hoc\n\nBody.\n",
	})

	a := requireApproval(t, h, token, model.RequireApprovalInput{
		EntityKind: "doc", EntityID: model.DocEntityID(d.ID), Actor: "alice",
	})
	if a.SubjectRevision != strconv.Itoa(d.Version) {
		t.Errorf("subject_revision = %q, want the document's current version %d",
			a.SubjectRevision, d.Version)
	}
	// A revision is exactly what DecideApproval refuses to act without
	// (ErrNoRevision), so defaulting it is what makes the row decidable.
	if a.SubjectRevision == "" {
		t.Error("an ad-hoc document requirement must name a revision")
	}
	if a.RequiredActor == nil || *a.RequiredActor != "alice" {
		t.Errorf("required_actor = %v, want alice", a.RequiredActor)
	}
}

// TestAdHocRequirementRefusals covers the three ways a request is wrong: an
// entity that does not exist, a kind no requirement may name, and naming both
// a role and an actor.
func TestAdHocRequirementRefusals(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Real", "priority": "low", "kind": "feature",
	})["id"].(string)

	cases := []struct {
		name string
		in   model.RequireApprovalInput
		want int
	}{
		{"missing entity", model.RequireApprovalInput{EntityKind: "task", EntityID: "WL-99999"},
			http.StatusNotFound},
		{"missing document", model.RequireApprovalInput{EntityKind: "doc", EntityID: "doc:99999"},
			http.StatusNotFound},
		{"unknown kind", model.RequireApprovalInput{EntityKind: "milestone", EntityID: taskID},
			http.StatusUnprocessableEntity},
		{"no entity id", model.RequireApprovalInput{EntityKind: "task"},
			http.StatusUnprocessableEntity},
		{"role and actor", model.RequireApprovalInput{EntityKind: "task", EntityID: taskID,
			Role: "science-leads", Actor: "alice"}, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/approvals", token, c.in)
			if rr.Code != c.want {
				t.Errorf("status = %d, want %d; body %s", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

// TestAdHocRequirementRefiling: 0064's key is (kind, id, revision, lane), so
// filing the same requirement twice is safe — the second call returns the row
// already there with 200 and counts nothing.
func TestAdHocRequirementRefiling(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Twice", "priority": "low", "kind": "feature",
	})["id"].(string)

	in := model.RequireApprovalInput{
		EntityKind: "task", EntityID: taskID, Lane: "security", Role: "sec-leads",
	}
	first := requireApproval(t, h, token, in)

	rr := doReq(t, h, "POST", "/api/v1/approvals", token, in)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-file status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var again model.Approval
	decodeInto(t, rr, &again)
	if again.ID != first.ID {
		t.Errorf("re-file returned approval %d, want the existing %d", again.ID, first.ID)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_approval_requirements_total{origin="adhoc"} 1`) {
		t.Errorf("re-filing double-counted the requirement:\n%s", metrics)
	}
}
