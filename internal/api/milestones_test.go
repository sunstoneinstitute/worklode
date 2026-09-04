package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
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
