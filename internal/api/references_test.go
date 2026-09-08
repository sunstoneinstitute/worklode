package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// referencePayload is the shape reference.created's payload carries, mirroring
// milestonePayload in milestones_test.go.
type referencePayload struct {
	FromKind  string `json:"from_kind"`
	From      string `json:"from"`
	ToKind    string `json:"to_kind"`
	To        string `json:"to"`
	Rel       string `json:"rel"`
	CreatedBy string `json:"created_by"`
}

// seedMilestoneDeliverable creates a milestone and a deliverable in project
// and returns their ids, for tests exercising the depends_on rel (029 §5).
func seedMilestoneDeliverable(t *testing.T, h http.Handler, token, project string) (milestoneID, deliverableID string) {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/projects/"+project+"/milestones", token,
		model.CreateMilestoneInput{Title: "Internal review"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone status = %d, body %s", rr.Code, rr.Body.String())
	}
	milestoneID = decodeMap(t, rr)["id"].(string)

	rr = doReq(t, h, "POST", "/api/v1/projects/"+project+"/deliverables", token,
		model.CreateDeliverableInput{Name: "Casualty datapackage"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}
	deliverableID = decodeMap(t, rr)["id"].(string)
	return milestoneID, deliverableID
}

// TestCreateReferenceAPI covers POST /api/v1/references and its GET
// readback from both ends (029 §5): the 201 body is the created edge with
// created_by taken from the authenticated actor (never the body), the write
// is recorded as reference.created from the "cli" surface, and the
// worklode_reference_writes_total counter is observed.
func TestCreateReferenceAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	milestoneID, deliverableID := seedMilestoneDeliverable(t, h, token, "proj")

	rr := doReq(t, h, "POST", "/api/v1/references", token, model.EntityEdge{
		FromKind: "milestone", From: milestoneID,
		ToKind: "deliverable", To: deliverableID,
		Rel: "depends_on",
		// A caller-supplied created_by must be ignored: the actor from the
		// bearer token is the only source of truth.
		CreatedBy: "someone-else",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var created model.EntityEdge
	decodeInto(t, rr, &created)
	if created.FromKind != "milestone" || created.From != milestoneID ||
		created.ToKind != "deliverable" || created.To != deliverableID || created.Rel != "depends_on" {
		t.Fatalf("created = %+v, want the milestone->deliverable depends_on edge", created)
	}
	if created.CreatedBy != "alice" {
		t.Errorf("created_by = %q, want alice (the authenticated actor)", created.CreatedBy)
	}
	if created.CreatedAt.IsZero() {
		t.Error("created_at is zero")
	}

	// Readback from the "from" end.
	rr = doReq(t, h, "GET", "/api/v1/references?kind=milestone&id="+milestoneID, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get from-end status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var list model.ReferenceListResponse
	decodeInto(t, rr, &list)
	if len(list.References) != 1 || list.References[0].To != deliverableID {
		t.Fatalf("from-end references = %+v, want one edge to %s", list.References, deliverableID)
	}

	// Readback from the "to" end.
	rr = doReq(t, h, "GET", "/api/v1/references?kind=deliverable&id="+deliverableID, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get to-end status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &list)
	if len(list.References) != 1 || list.References[0].From != milestoneID {
		t.Fatalf("to-end references = %+v, want one edge from %s", list.References, milestoneID)
	}

	events := storeEventsOfType(t, st, "reference.created", 1)
	if len(events) != 1 {
		t.Fatalf("reference.created events = %d, want 1", len(events))
	}
	if events[0].Source != "cli" {
		t.Errorf("event source = %q, want cli", events[0].Source)
	}
	var payload referencePayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", events[0].Payload, err)
	}
	if payload.FromKind != "milestone" || payload.From != milestoneID ||
		payload.ToKind != "deliverable" || payload.To != deliverableID ||
		payload.Rel != "depends_on" || payload.CreatedBy != "alice" {
		t.Fatalf("payload = %+v, want the created edge attributed to alice", payload)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_reference_writes_total{outcome="ok",rel="depends_on"} 1`) {
		t.Errorf("metrics missing the depends_on ok counter:\n%s", metrics)
	}
	// The other pinned rel pre-initialises to zero, so an unexercised one
	// reads as a flat zero rather than as no-data.
	if !strings.Contains(metrics, `worklode_reference_writes_total{outcome="ok",rel="seeded_by"} 0`) {
		t.Errorf("metrics missing the pre-initialised seeded_by series:\n%s", metrics)
	}
}

// TestCreateReferenceAPISeededBy covers the project->task rel, the other half
// of the closed rel vocabulary (029 §5), through project and task ids
// instead of milestone and deliverable ones.
func TestCreateReferenceAPISeededBy(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Seeded task", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/references", token, model.EntityEdge{
		FromKind: "project", From: "proj",
		ToKind: "task", To: "WL-1",
		Rel: "seeded_by",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/references?kind=task&id=WL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var list model.ReferenceListResponse
	decodeInto(t, rr, &list)
	if len(list.References) != 1 || list.References[0].From != "proj" || list.References[0].Rel != "seeded_by" {
		t.Fatalf("references = %+v, want one seeded_by edge from proj", list.References)
	}
}

// TestCreateReferenceAPIRefusals covers the store sentinels mapStoreErr
// translates for a reference write: an unknown rel and a shape mismatch are
// both the caller's input (422), a missing end is 404, and re-declaring the
// exact same edge is 409.
func TestCreateReferenceAPIRefusals(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	milestoneID, deliverableID := seedMilestoneDeliverable(t, h, token, "proj")

	for _, tt := range []struct {
		name string
		edge model.EntityEdge
		want int
	}{
		{
			"unknown rel",
			model.EntityEdge{FromKind: "milestone", From: milestoneID, ToKind: "deliverable", To: deliverableID, Rel: "bogus"},
			http.StatusUnprocessableEntity,
		},
		{
			"shape mismatch",
			model.EntityEdge{FromKind: "deliverable", From: deliverableID, ToKind: "milestone", To: milestoneID, Rel: "depends_on"},
			http.StatusUnprocessableEntity,
		},
		{
			"unknown from end",
			model.EntityEdge{FromKind: "milestone", From: "WL-MILE-9", ToKind: "deliverable", To: deliverableID, Rel: "depends_on"},
			http.StatusNotFound,
		},
		{
			"unknown to end",
			model.EntityEdge{FromKind: "milestone", From: milestoneID, ToKind: "deliverable", To: "WL-DEL-9", Rel: "depends_on"},
			http.StatusNotFound,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/references", token, tt.edge)
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tt.want, rr.Body.String())
			}
		})
	}

	rr := doReq(t, h, "POST", "/api/v1/references", token, model.EntityEdge{
		FromKind: "milestone", From: milestoneID, ToKind: "deliverable", To: deliverableID, Rel: "depends_on",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("first create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/references", token, model.EntityEdge{
		FromKind: "milestone", From: milestoneID, ToKind: "deliverable", To: deliverableID, Rel: "depends_on",
	})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

// TestListReferencesAPIRequiresBothParams covers GET /api/v1/references's
// required kind and id query parameters: either missing is 422.
func TestListReferencesAPIRequiresBothParams(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)

	for _, query := range []string{"", "?kind=milestone", "?id=WL-MILE-1"} {
		rr := doReq(t, h, "GET", "/api/v1/references"+query, token, nil)
		if rr.Code != http.StatusUnprocessableEntity {
			t.Errorf("query %q status = %d, want 422; body %s", query, rr.Code, rr.Body.String())
		}
	}
}
