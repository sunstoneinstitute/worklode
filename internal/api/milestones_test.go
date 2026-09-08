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
