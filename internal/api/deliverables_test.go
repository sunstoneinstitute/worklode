package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestCreateDeliverableWithArtifact: the JSON API accepts the address a
// deliverable is verified by (029 §3.1) and echoes it back on the read, while
// reported_state stays empty — nothing has reported yet, and the deliverable
// itself never claims a state (§3.2).
func TestCreateDeliverableWithArtifact(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	const artifact = "bigquery://sunstone-prod/cow/casualties"
	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Casualty datapackage", Artifact: "  " + artifact + "  "})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var created model.Deliverable
	decodeInto(t, rr, &created)
	if created.Artifact != artifact {
		t.Fatalf("created artifact = %q, want %q", created.Artifact, artifact)
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/deliverables", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var list model.DeliverableListResponse
	decodeInto(t, rr, &list)
	if len(list.Deliverables) != 1 {
		t.Fatalf("listed %+v, want one deliverable", list.Deliverables)
	}
	got := list.Deliverables[0]
	if got.Artifact != artifact {
		t.Errorf("listed artifact = %q, want %q", got.Artifact, artifact)
	}
	if got.ReportedState != "" || got.ReportedAt != nil {
		t.Errorf("listed reported state = %q at %v, want unreported", got.ReportedState, got.ReportedAt)
	}
}

// TestCreateDeliverableArtifactBounds: the artifact is length-checked and
// nothing else. A catalog address is not a browser link, so schemes the URL
// field refuses are legal here.
func TestCreateDeliverableArtifactBounds(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for _, artifact := range []string{
		"gs://sunstone-prod/cow/casualties",
		"iceberg://warehouse/cow.casualties",
		"urn:datapackage:cow-casualties",
	} {
		rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
			model.CreateDeliverableInput{Name: artifact, Artifact: artifact})
		if rr.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, want 201; body %s", artifact, rr.Code, rr.Body.String())
		}
	}

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "too long", Artifact: strings.Repeat("x", 2001)})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-long artifact status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

// TestPatchDeliverableMilestone covers PATCH /api/v1/deliverables/{id}'s
// milestone field (spec 029 §2), mirroring TestPatchTaskMilestone for the
// deliverable side: an empty body is refused before it reaches the store, a
// same-project attach is 200 and stored, an unknown or cross-project
// milestone is refused (422), "" clears it, and the deliverable_attach
// counter is observed once per attempt that reaches the store.
func TestPatchDeliverableMilestone(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createProject(t, st, "proj2")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Attach target"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}
	deliverableID := decodeMap(t, rr)["id"].(string)

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Internal review"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone status = %d, body %s", rr.Code, rr.Body.String())
	}
	milestoneID := decodeMap(t, rr)["id"].(string)

	// An empty body is refused.
	rr = doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token, map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty body status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// Unknown milestone.
	rr = doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token,
		model.EditDeliverableInput{Milestone: strPtr("WL-MILE-9")})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown milestone status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// Same-project attach.
	rr = doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token,
		model.EditDeliverableInput{Milestone: &milestoneID})
	if rr.Code != http.StatusOK {
		t.Fatalf("attach status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["milestone"]; got != milestoneID {
		t.Fatalf("patch response milestone = %v, want %s", got, milestoneID)
	}
	d, err := st.GetDeliverable(context.Background(), deliverableID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if d.Milestone != milestoneID {
		t.Fatalf("stored milestone = %q, want %s", d.Milestone, milestoneID)
	}

	// Cross-project attach: 029 §5, containment never crosses a project
	// boundary.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj2/deliverables", token,
		model.CreateDeliverableInput{Name: "Other project deliverable"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable in proj2 status = %d, body %s", rr.Code, rr.Body.String())
	}
	otherID := decodeMap(t, rr)["id"].(string)
	rr = doReq(t, h, "PATCH", "/api/v1/deliverables/"+otherID, token,
		model.EditDeliverableInput{Milestone: &milestoneID})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cross-project attach status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// "" clears it.
	rr = doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token,
		model.EditDeliverableInput{Milestone: strPtr("")})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body %s", rr.Code, rr.Body.String())
	}
	if cleared := decodeMap(t, rr)["milestone"]; cleared != nil && cleared != "" {
		t.Errorf("patch response milestone after clear = %v, want empty", cleared)
	}
	d, err = st.GetDeliverable(context.Background(), deliverableID)
	if err != nil {
		t.Fatalf("get deliverable: %v", err)
	}
	if d.Milestone != "" {
		t.Fatalf("stored milestone after clear = %q, want empty", d.Milestone)
	}

	// Four attempts reach the store (the empty body above never does):
	// unknown (rejected), attach (ok), cross-project (rejected), clear (ok).
	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="deliverable_attach",outcome="ok"} 2`) {
		t.Errorf("metrics missing two ok deliverable_attach counts:\n%s", metrics)
	}
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="deliverable_attach",outcome="rejected"} 2`) {
		t.Errorf("metrics missing two rejected deliverable_attach counts:\n%s", metrics)
	}
}

// strPtr is a small helper for a literal *string in a table or struct
// literal, where &"x" is not legal Go.
func strPtr(s string) *string { return &s }
