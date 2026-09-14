package api_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// milestonePayload is the shape the milestones plan pins on
// milestone.created.
type milestonePayload struct {
	Project   string `json:"project"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Position  string `json:"position"`
	CreatedBy string `json:"created_by"`
}

// TestCreateMilestoneAPI covers POST /api/v1/projects/{id}/milestones: the
// 201 body is the created milestone with its appended position, the write is
// recorded as milestone.created from the "cli" surface, and the counter is
// observed.
func TestCreateMilestoneAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "  Internal review  "})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	var created model.Milestone
	decodeInto(t, rr, &created)
	if created.ID != "WL-MILE-1" || created.Title != "Internal review" || created.Position != 1 {
		t.Fatalf("created = %+v, want WL-MILE-1 / trimmed title / position 1", created)
	}
	if created.Project != "proj" {
		t.Errorf("created project = %q, want proj", created.Project)
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Publication", Position: 7})
	if rr.Code != http.StatusCreated {
		t.Fatalf("second create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	decodeInto(t, rr, &created)
	if created.ID != "WL-MILE-2" || created.Position != 7 {
		t.Fatalf("second = %+v, want WL-MILE-2 at position 7", created)
	}

	events := storeEventsOfType(t, st, "milestone.created", 2)
	if len(events) != 2 {
		t.Fatalf("milestone.created events = %d, want 2", len(events))
	}
	last := events[len(events)-1]
	if last.Source != "cli" {
		t.Errorf("event source = %q, want cli", last.Source)
	}
	var payload milestonePayload
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", last.Payload, err)
	}
	if payload.Project != "proj" || payload.ID != "WL-MILE-2" ||
		payload.Title != "Publication" || payload.Position != "7" {
		t.Fatalf("payload = %+v, want project/id/title/position of the created milestone", payload)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="create",outcome="ok"} 2`) {
		t.Errorf("metrics missing the create counter:\n%s", metrics)
	}
	// The other pinned actions pre-initialise to zero, so an unexercised one
	// reads as a flat zero rather than as no-data.
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="task_attach",outcome="error"} 0`) {
		t.Errorf("metrics missing the pre-initialised task_attach series:\n%s", metrics)
	}
}

// TestCreateMilestoneAPIRefusals: a blank or over-long title is the caller's
// input (422) and an unknown project is a 404, and each is counted as
// "rejected" rather than as a fault.
func TestCreateMilestoneAPIRefusals(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		name, path string
		in         model.CreateMilestoneInput
		want       int
	}{
		{"blank title", "/api/v1/projects/proj/milestones", model.CreateMilestoneInput{Title: "   "}, http.StatusUnprocessableEntity},
		{"long title", "/api/v1/projects/proj/milestones", model.CreateMilestoneInput{Title: strings.Repeat("x", 201)}, http.StatusUnprocessableEntity},
		{"negative position", "/api/v1/projects/proj/milestones", model.CreateMilestoneInput{Title: "ok", Position: -1}, http.StatusUnprocessableEntity},
		{"unknown project", "/api/v1/projects/nosuch/milestones", model.CreateMilestoneInput{Title: "ok"}, http.StatusNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", tt.path, token, tt.in)
			if rr.Code != tt.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tt.want, rr.Body.String())
			}
		})
	}

	if events := storeEventsOfType(t, st, "milestone.created", 0); len(events) != 0 {
		t.Errorf("refused creates recorded %d events, want none", len(events))
	}
	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="create",outcome="rejected"} 4`) {
		t.Errorf("metrics missing four rejected creates:\n%s", metrics)
	}
}

// TestListProjectMilestonesAPI covers GET /api/v1/projects/{id}/milestones:
// an empty project reads back an empty list rather than 404ing, a seeded
// project lists its milestones in position order with derived progress from
// their attached children, and an unknown project 404s.
func TestListProjectMilestonesAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/milestones", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var empty model.MilestoneListResponse
	decodeInto(t, rr, &empty)
	if len(empty.Milestones) != 0 {
		t.Fatalf("empty project milestones = %+v, want none", empty.Milestones)
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Internal review"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone 1 status = %d, body %s", rr.Code, rr.Body.String())
	}
	m1 := decodeMap(t, rr)["id"].(string)

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Publication"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone 2 status = %d, body %s", rr.Code, rr.Body.String())
	}
	m2 := decodeMap(t, rr)["id"].(string)

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Attached task", "priority": "high", "kind": "feature",
	})
	if rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"milestone": m1}); rr.Code != http.StatusOK {
		t.Fatalf("attach task status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/milestones", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("seeded status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var resp model.MilestoneListResponse
	decodeInto(t, rr, &resp)
	if len(resp.Milestones) != 2 {
		t.Fatalf("milestones = %+v, want 2", resp.Milestones)
	}
	if resp.Milestones[0].ID != m1 || resp.Milestones[1].ID != m2 {
		t.Fatalf("milestone order = [%s, %s], want [%s, %s]",
			resp.Milestones[0].ID, resp.Milestones[1].ID, m1, m2)
	}
	if got := resp.Milestones[0].Progress; got.TasksTotal != 1 || got.TasksClosed != 0 {
		t.Errorf("m1 progress = %+v, want 1 task open", got)
	}
	if got := resp.Milestones[1].Progress; got.TasksTotal != 0 {
		t.Errorf("m2 progress = %+v, want no tasks", got)
	}

	if rr := doReq(t, h, "GET", "/api/v1/projects/nosuch/milestones", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
	}
}

// TestGetMilestoneAPI covers GET /api/v1/milestones/{id}: the detail carries
// the milestone's attached task and deliverable, its progress derived from
// exactly those children, and an unknown id 404s.
func TestGetMilestoneAPI(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Internal review"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone status = %d, body %s", rr.Code, rr.Body.String())
	}
	milestoneID := decodeMap(t, rr)["id"].(string)

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Attached task", "priority": "high", "kind": "feature",
	})
	if rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"milestone": milestoneID}); rr.Code != http.StatusOK {
		t.Fatalf("attach task status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Attached deliverable"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}
	deliverableID := decodeMap(t, rr)["id"].(string)
	if rr := doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token,
		model.EditDeliverableInput{Milestone: &milestoneID}); rr.Code != http.StatusOK {
		t.Fatalf("attach deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/milestones/"+milestoneID, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var detail model.MilestoneDetail
	decodeInto(t, rr, &detail)
	if detail.ID != milestoneID || detail.Title != "Internal review" {
		t.Fatalf("detail = %+v, want id/title of the created milestone", detail)
	}
	if len(detail.Tasks) != 1 || detail.Tasks[0].ID != "WL-1" {
		t.Fatalf("detail tasks = %+v, want [WL-1]", detail.Tasks)
	}
	if len(detail.Deliverables) != 1 || detail.Deliverables[0].ID != deliverableID {
		t.Fatalf("detail deliverables = %+v, want [%s]", detail.Deliverables, deliverableID)
	}
	if detail.Progress.TasksTotal != 1 || detail.Progress.DeliverablesTotal != 1 {
		t.Fatalf("detail progress = %+v, want 1 task and 1 deliverable", detail.Progress)
	}

	if rr := doReq(t, h, "GET", "/api/v1/milestones/WL-MILE-9", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown milestone status = %d, want 404", rr.Code)
	}
}

// TestMilestonesPage covers the project-local Milestones destination (spec
// 029 §2, spec 032 §10): an empty project renders the honest "No milestones
// yet" state, a seeded project renders every milestone in position order with
// its derived progress, and an unknown project 404s the way every other
// project route does. Milestones are seeded through the real write path, so
// what the page shows is what a create actually stores.
func TestMilestonesPage(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/milestones", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body, "No milestones yet")

	for _, title := range []string{"Internal review", "Publication"} {
		if rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
			model.CreateMilestoneInput{Title: title}); rr.Code != http.StatusCreated {
			t.Fatalf("seed %q status = %d; body %s", title, rr.Code, rr.Body.String())
		}
	}

	rr = doReq(t, h, "GET", "/projects/proj/milestones", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("seeded status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	main := mainContent(t, body)
	if strings.Contains(main, "No milestones yet") {
		t.Error("a seeded page still renders the whole-page empty state")
	}
	// Section order is the store's position order, not insertion luck.
	assertOrder(t, main, "WL-MILE-1", "Internal review", "WL-MILE-2", "Publication")
	// Nothing is attached yet, so both sections carry zero progress and say
	// so rather than rendering an empty table.
	if n := strings.Count(main, "0/0 tasks closed"); n != 2 {
		t.Errorf("progress lines = %d, want 2:\n%s", n, main)
	}
	if n := strings.Count(main, "Nothing is attached to this milestone yet."); n != 2 {
		t.Errorf("childless milestone notes = %d, want 2:\n%s", n, main)
	}

	if rr := doReq(t, h, "GET", "/projects/nosuch/milestones", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
	}
}

// TestMilestoneReferencesOnPage covers the References section and its add
// form (029 §5): the empty state, a cross-project reference rendering with
// the reported state of the deliverable it points at, a good submit writing
// the edge through the "web" surface and 303ing back, and an unknown id
// coming back as the page with the message and what was typed.
func TestMilestoneReferencesOnPage(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createProject(t, st, "cow")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Publication"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone status = %d; body %s", rr.Code, rr.Body.String())
	}
	milestoneID := decodeMap(t, rr)["id"].(string)

	// The referenced deliverable lives in another project, which is what
	// entity_edges exist for, and carries a reported state so the row has
	// one to show.
	const artifact = "bigquery://sunstone-prod/cow/casualties"
	rr = doReq(t, h, "POST", "/api/v1/projects/cow/deliverables", token,
		model.CreateDeliverableInput{Name: "Casualty datapackage", Artifact: artifact})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable status = %d; body %s", rr.Code, rr.Body.String())
	}
	deliverableID := decodeMap(t, rr)["id"].(string)
	seedEvent(t, st, "cow-published", func(tx *sql.Tx, eventID int64) error {
		_, err := store.InsertArtifactEvidence(tx, eventID, model.ArtifactEvidence{
			EntityKind: "deliverable", EntityID: deliverableID, Artifact: artifact,
			Source: "catalog", State: "published", Provenance: "observed",
			OccurredAt: time.Now().UTC().Truncate(time.Second),
		})
		return err
	})

	main := mainContent(t, doReq(t, h, "GET", "/projects/proj/milestones", "", nil).Body.String())
	bodyContains(t, main, "No deliverable references.")

	action := "/projects/proj/milestones/" + milestoneID + "/references"
	rr = doForm(t, h, action, url.Values{"deliverable": {"  " + deliverableID + "  "}}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/projects/proj/milestones" {
		t.Fatalf("Location = %q, want /projects/proj/milestones", loc)
	}

	page := doReq(t, h, "GET", "/projects/proj/milestones", "", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	main = mainContent(t, page.Body.String())
	if strings.Contains(main, "No deliverable references.") {
		t.Error("the References section still renders its empty state")
	}
	// The id, the name, the reported state, and the link into the origin
	// project — the cross-project origin is what the id and the link make
	// visible.
	bodyContains(t, main, deliverableID, "Casualty datapackage", "Published",
		`href="/projects/cow/deliverables"`)

	events := storeEventsOfType(t, st, "reference.created", 1)
	if len(events) != 1 {
		t.Fatalf("reference.created events = %d, want 1", len(events))
	}
	if events[0].Source != "web" {
		t.Errorf("event source = %q, want web", events[0].Source)
	}

	// An unknown id comes back as the page, at 422, with the message and the
	// id still in the field it was typed into.
	rr = doForm(t, h, action, url.Values{"deliverable": {"COW-DEL-404"}}, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown id status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	main = mainContent(t, rr.Body.String())
	bodyContains(t, main, "No deliverable with that id.", `value="COW-DEL-404"`)

	// A second add of the same edge is refused as a duplicate, not a 500.
	rr = doForm(t, h, action, url.Values{"deliverable": {deliverableID}}, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	bodyContains(t, mainContent(t, rr.Body.String()), "already references that deliverable")

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_web_form_submissions_total{form="milestone_reference",outcome="created"} 1`) {
		t.Errorf("metrics missing the milestone_reference form counter:\n%s", metrics)
	}
}

// TestDeleteMilestoneAPI covers DELETE /api/v1/milestones/{id}: a milestone
// still holding children is 422 and stays whole, an emptied one answers 200
// with the record of what went, and an unknown id is 404.
func TestDeleteMilestoneAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	milestoneID, deliverableID := seedMilestoneWithChildren(t, h, token)

	if rr := doReq(t, h, "DELETE", "/api/v1/milestones/"+milestoneID+"?cascade=maybe", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("unparseable cascade = %d, want 422", rr.Code)
	}
	if rr := doReq(t, h, "DELETE", "/api/v1/milestones/"+milestoneID, token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("delete with children = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// The refusal is the whole guard: both children must still be attached.
	rr := doReq(t, h, "GET", "/api/v1/milestones/"+milestoneID, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get after refused delete = %d, body %s", rr.Code, rr.Body.String())
	}
	var detail model.MilestoneDetail
	decodeInto(t, rr, &detail)
	if len(detail.Tasks) != 1 || len(detail.Deliverables) != 1 {
		t.Fatalf("after refused delete: %d tasks, %d deliverables; want 1 and 1",
			len(detail.Tasks), len(detail.Deliverables))
	}

	for _, d := range []struct {
		path string
		body any
	}{
		{"/api/v1/tasks/WL-1", map[string]any{"milestone": ""}},
		{"/api/v1/deliverables/" + deliverableID, model.EditDeliverableInput{Milestone: new(string)}},
	} {
		if rr := doReq(t, h, "PATCH", d.path, token, d.body); rr.Code != http.StatusOK {
			t.Fatalf("detach %s = %d, body %s", d.path, rr.Code, rr.Body.String())
		}
	}

	rr = doReq(t, h, "DELETE", "/api/v1/milestones/"+milestoneID, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var deleted model.MilestoneDeletion
	decodeInto(t, rr, &deleted)
	if deleted.Milestone.ID != milestoneID || deleted.Milestone.Title != "Internal review" {
		t.Fatalf("body milestone = %+v, want the deleted row", deleted.Milestone)
	}
	if len(deleted.Tasks) != 0 || len(deleted.Deleted) != 0 {
		t.Errorf("body = %v tasks, %v deleted; want an emptied milestone to report neither",
			deleted.Tasks, deleted.Deleted)
	}
	if rr := doReq(t, h, "GET", "/api/v1/milestones/"+milestoneID, token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("get after delete = %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, "DELETE", "/api/v1/milestones/WL-MILE-9", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("delete unknown milestone = %d, want 404", rr.Code)
	}

	events := storeEventsOfType(t, st, "milestone.deleted", 1)
	if len(events) != 1 {
		t.Fatalf("milestone.deleted events = %d, want 1", len(events))
	}
	if events[0].Source != "cli" {
		t.Errorf("event source = %q, want cli", events[0].Source)
	}
	// The row is gone, so the payload is the only record of what it held.
	payload := decodeDeletePayload(t, events[0].Payload)
	if payload.Project != "proj" || payload.ID != milestoneID ||
		payload.Title != "Internal review" || payload.Position != "1" {
		t.Fatalf("payload = %+v, want the deleted milestone's fields", payload)
	}
	if payload.Cascade || len(payload.Tasks) != 0 || len(payload.Deleted) != 0 {
		t.Fatalf("payload lists = %+v, want an emptied milestone to report neither", payload)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_milestone_changes_total{action="delete",outcome="ok"} 1`,
		`worklode_milestone_changes_total{action="delete",outcome="rejected"} 2`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s:\n%s", want, metrics)
		}
	}
}

// TestDeleteMilestoneCascadeAPI: ?cascade=true overrides the refusal, deleting
// the attached deliverables and detaching the attached tasks, under its own
// counter action.
func TestDeleteMilestoneCascadeAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	milestoneID, deliverableID := seedMilestoneWithChildren(t, h, token)

	rr := doReq(t, h, "DELETE", "/api/v1/milestones/"+milestoneID+"?cascade=true", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("cascade status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	var deleted model.MilestoneDeletion
	decodeInto(t, rr, &deleted)
	if len(deleted.Deleted) != 1 || deleted.Deleted[0] != deliverableID {
		t.Fatalf("body deleted = %v, want [%s]", deleted.Deleted, deliverableID)
	}
	if len(deleted.Tasks) != 1 || deleted.Tasks[0] != "WL-1" {
		t.Fatalf("body tasks = %v, want [WL-1] detached", deleted.Tasks)
	}

	if rr := doReq(t, h, "GET", "/api/v1/deliverables/"+deliverableID, token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("deliverable after cascade = %d, want 404", rr.Code)
	}
	// Tasks are work, not an output of the milestone.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task after cascade = %d, want 200", rr.Code)
	}
	if got := decodeMap(t, rr)["milestone"]; got != nil && got != "" {
		t.Errorf("task milestone = %v, want detached", got)
	}

	events := storeEventsOfType(t, st, "milestone.deleted", 1)
	if len(events) != 1 {
		t.Fatalf("milestone.deleted events = %d, want 1", len(events))
	}
	payload := decodeDeletePayload(t, events[0].Payload)
	if !payload.Cascade || len(payload.Deleted) != 1 || payload.Deleted[0] != deliverableID ||
		len(payload.Tasks) != 1 || payload.Tasks[0] != "WL-1" {
		t.Fatalf("payload = %+v, want cascade with the deleted deliverable and detached task", payload)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_milestone_changes_total{action="delete_cascade",outcome="ok"} 1`) {
		t.Errorf("metrics missing the cascade counter:\n%s", metrics)
	}
}

// seedMilestoneWithChildren creates one milestone with a task (WL-1) and a
// deliverable attached, all through the real write paths, and returns the
// milestone and deliverable ids.
func seedMilestoneWithChildren(t *testing.T, h http.Handler, token string) (string, string) {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token,
		model.CreateMilestoneInput{Title: "Internal review"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create milestone status = %d, body %s", rr.Code, rr.Body.String())
	}
	milestoneID := decodeMap(t, rr)["id"].(string)

	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Attached task", "priority": "high", "kind": "feature",
	})
	if rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"milestone": milestoneID}); rr.Code != http.StatusOK {
		t.Fatalf("attach task status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
		model.CreateDeliverableInput{Name: "Attached deliverable"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}
	deliverableID := decodeMap(t, rr)["id"].(string)
	if rr := doReq(t, h, "PATCH", "/api/v1/deliverables/"+deliverableID, token,
		model.EditDeliverableInput{Milestone: &milestoneID}); rr.Code != http.StatusOK {
		t.Fatalf("attach deliverable status = %d, body %s", rr.Code, rr.Body.String())
	}
	return milestoneID, deliverableID
}

// milestoneDeletePayload is the milestone.deleted payload: the deleted row's
// fields plus the lists naming what the delete let go of.
type milestoneDeletePayload struct {
	milestonePayload
	Cascade    bool     `json:"cascade"`
	Tasks      []string `json:"tasks_detached"`
	Deleted    []string `json:"deliverables_deleted"`
	References []string `json:"references_dropped"`
}

func decodeDeletePayload(t *testing.T, raw []byte) milestoneDeletePayload {
	t.Helper()
	var p milestoneDeletePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("decode payload %s: %v", raw, err)
	}
	return p
}
