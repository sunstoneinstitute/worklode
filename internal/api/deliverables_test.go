package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
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

// TestCreateDeliverableByLabel sends 029 §3.1's label form through the JSON
// API, so the transport cannot silently drop the selector before it reaches
// the store.
func TestCreateDeliverableByLabel(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Datasets", Label: true})
	if rr.Code != http.StatusCreated {
		t.Fatalf("label create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var created model.Deliverable
	decodeInto(t, rr, &created)
	if created.Label != "worklode.deliverable=WL/datasets" || created.Artifact != "" {
		t.Fatalf("created label/artifact = %q/%q, want minted label and empty artifact", created.Label, created.Artifact)
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Both", Label: true, Artifact: "gs://proj/both"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("label plus artifact status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	var errResp model.ErrorResponse
	decodeInto(t, rr, &errResp)
	const wantMsg = "declare an artifact address or a label, not both"
	if errResp.Error != wantMsg {
		t.Errorf("error = %q, want %q", errResp.Error, wantMsg)
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

// stampStoryFlow puts the shipped `story` flow on a project, the way Task 10's
// apply surface will. Reading it from api.LoadApprovalFlows keeps the test
// honest about the flow the instance actually ships.
func stampStoryFlow(t *testing.T, st *store.Store, projectID string) {
	t.Helper()
	flows, err := api.LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	story := flowByName(t, flows, "story")
	tx, err := st.DBForTests().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // committed below on the happy path
	if err := store.SetProjectApprovalFlow(tx, projectID,
		model.ApprovalFlowSnapshot{Flow: story}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// deliverableApproval is one materialized row, read straight from the table:
// the awaiting queue's joins do not carry deliverable-kind rows yet (WL-719).
type deliverableApproval struct{ entityID, lane, state, rev, role, createdBy string }

func deliverableApprovals(t *testing.T, st *store.Store) []deliverableApproval {
	t.Helper()
	rows, err := st.DBForTests().Query(
		`SELECT entity_id, lane, state, subject_revision,
		        COALESCE(required_role, ''), COALESCE(created_by, '')
		   FROM approvals WHERE entity_kind = 'deliverable'
		  ORDER BY entity_id, lane`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []deliverableApproval
	for rows.Next() {
		var r deliverableApproval
		if err := rows.Scan(&r.entityID, &r.lane, &r.state, &r.rev, &r.role, &r.createdBy); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestDeliverableCreationMaterializesFlowLanes: 029 §7.1's "materialized as an
// awaiting row when the entity is created". A stamped project's new
// deliverable owes its flow's lanes immediately; a project with no snapshot
// owes nothing; and two same-named deliverables owe a set each, because the
// requirement is keyed on the entity id and not on the name that matched it.
func TestDeliverableCreationMaterializesFlowLanes(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "story")
	createProject(t, st, "plain")
	stampStoryFlow(t, st, "story")

	create := func(project, name string) string {
		t.Helper()
		rr := doReq(t, h, "POST", "/api/v1/projects/"+project+"/deliverables", token,
			model.CreateDeliverableInput{Name: name})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d, body %s", name, rr.Code, rr.Body.String())
		}
		return decodeMap(t, rr)["id"].(string)
	}

	first := create("story", "Methodology")
	rows := deliverableApprovals(t, st)
	if len(rows) != 2 {
		t.Fatalf("materialized %d lanes, want 2: %+v", len(rows), rows)
	}
	want := []deliverableApproval{
		{first, "methodology/domain-expert", "awaiting", "", "domain-experts", "worklode"},
		{first, "methodology/science-lead", "awaiting", "", "science-leads", "worklode"},
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_approval_requirements_total{origin="flow"} 2`) {
		t.Errorf("metrics missing two flow-materialized requirements:\n%s", metrics)
	}

	// A deliverable no lane targets, and one in a project with no snapshot:
	// neither owes anything.
	create("story", "Interview notes")
	create("plain", "Methodology")
	if rows := deliverableApprovals(t, st); len(rows) != 2 {
		t.Fatalf("after two unmatched deliverables, %d rows: %+v", len(rows), rows)
	}

	// The same name again is a second entity, so it owes its own lanes.
	second := create("story", "Methodology")
	rows = deliverableApprovals(t, st)
	if len(rows) != 4 {
		t.Fatalf("after the second Methodology, %d rows, want 4: %+v", len(rows), rows)
	}
	ids := map[string]int{}
	for _, r := range rows {
		ids[r.entityID]++
	}
	if ids[first] != 2 || ids[second] != 2 {
		t.Errorf("lanes per entity = %v, want 2 for %s and 2 for %s", ids, first, second)
	}
	metrics = doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_approval_requirements_total{origin="flow"} 4`) {
		t.Errorf("metrics missing four flow-materialized requirements:\n%s", metrics)
	}
}

// TestGetDeliverableAPI covers GET /api/v1/deliverables/{id}: the read
// projection carries the reported state/timestamp alongside the declared
// fields, and an unknown id 404s (WL-715).
func TestGetDeliverableAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Casualty datapackage", Artifact: "bigquery://sunstone-prod/cow/casualties"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	id := decodeMap(t, rr)["id"].(string)

	rr = doReq(t, h, "GET", "/api/v1/deliverables/"+id, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var got model.Deliverable
	decodeInto(t, rr, &got)
	if got.ID != id || got.Name != "Casualty datapackage" {
		t.Fatalf("get = %+v, want id/name of the created deliverable", got)
	}
	if got.ReportedState != "" || got.ReportedAt != nil {
		t.Errorf("reported state = %q at %v, want unreported", got.ReportedState, got.ReportedAt)
	}

	rr = doReq(t, h, "GET", "/api/v1/deliverables/WL-DEL-999", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("get unknown status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
