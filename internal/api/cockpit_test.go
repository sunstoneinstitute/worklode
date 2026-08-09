package api_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestProjectCockpit asserts the normalized top-level shape of the cockpit
// projection for an ordinary project with no tasks: every collection is a
// concrete empty slice (never null), pinned_focus and next_decision are JSON
// null (never dummy records), and the mode is the declared-evidence
// Operations basis (Part 1 stores no other lifecycle facts).
func TestProjectCockpit(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q, want application/json", ct)
	}

	var got struct {
		CanonicalURL string `json:"canonical_url"`
		Project      struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"project"`
		Mode struct {
			Name  string `json:"name"`
			Basis struct {
				Category string `json:"category"`
				Summary  string `json:"summary"`
			} `json:"basis"`
		} `json:"mode"`
	}
	decodeInto(t, rr, &got)

	if got.CanonicalURL != "/projects/proj" {
		t.Errorf("canonical_url = %q, want /projects/proj", got.CanonicalURL)
	}
	if got.Project.ID != "proj" || got.Project.Key != "WL" {
		t.Errorf("project = %+v, want id=proj key=WL", got.Project)
	}
	if got.Mode.Name != "operations" {
		t.Errorf("mode.name = %q, want operations", got.Mode.Name)
	}
	if got.Mode.Basis.Category != "declared" {
		t.Errorf("mode.basis.category = %q, want declared", got.Mode.Basis.Category)
	}
	const wantSummary = "Existing Worklode project; no intake lifecycle facts are present"
	if got.Mode.Basis.Summary != wantSummary {
		t.Errorf("mode.basis.summary = %q, want %q", got.Mode.Basis.Summary, wantSummary)
	}

	// Marshal-level check: collections serialize as [], never null; optional
	// governed objects serialize as null, never a dummy record.
	body := rr.Body.String()
	for _, want := range []string{
		`"pinned_focus":null`,
		`"ranking_focus":[]`,
		`"next_decision":null`,
		`"work":{"in_progress":[],"in_review":[],"ready":[],"blocked":[]}`,
		`"secondary_concerns":[]`,
		`"repositories":[]`,
		`"cost":{"days":[],"totals":[]}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody: %s", want, body)
		}
	}
}

// TestProjectCockpitRequiresAuth mirrors TestAPIRequiresAuth for the cockpit
// route: a missing bearer token must 401, not fall through to an unmatched
// route (404) or an anonymous read.
func TestProjectCockpitRequiresAuth(t *testing.T) {
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

// TestProjectCockpitUnknownProject asserts GetProject's ErrNotFound maps to
// 404 through mapStoreErr, same as every other project-scoped endpoint.
func TestProjectCockpitUnknownProject(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/projects/nosuch/cockpit", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body %s", rr.Code, rr.Body.String())
	}
}

// TestProjectCockpitReadyTask asserts a ready task is adapted from the board
// into the ready work bucket with its declared fields intact and blocked
// left false (it has no blocking edge).
func TestProjectCockpitReadyTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ready task", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}

	var got struct {
		Work struct {
			Ready []struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Priority string `json:"priority"`
				State    string `json:"state"`
				Blocked  bool   `json:"blocked"`
				URL      string `json:"url"`
			} `json:"ready"`
			InProgress []any `json:"in_progress"`
			InReview   []any `json:"in_review"`
			Blocked    []any `json:"blocked"`
		} `json:"work"`
	}
	decodeInto(t, rr, &got)

	if len(got.Work.Ready) != 1 {
		t.Fatalf("ready tasks = %d, want 1: %+v", len(got.Work.Ready), got.Work.Ready)
	}
	task := got.Work.Ready[0]
	if task.ID != "WL-1" || task.Title != "Ready task" || task.Priority != "medium" ||
		task.State != "ready" || task.Blocked || task.URL != "/tasks/WL-1" {
		t.Errorf("ready task = %+v", task)
	}
	if len(got.Work.InProgress) != 0 || len(got.Work.InReview) != 0 || len(got.Work.Blocked) != 0 {
		t.Errorf("other buckets non-empty: %+v", got.Work)
	}
}

// TestProjectCockpitForbidsLegacyKeys guards the plan's binding constraint:
// the cockpit is a projection, never a workflow column, and must never emit
// a stored/editable health field, completion percentage, stage, or
// variant-driven mode.
func TestProjectCockpitForbidsLegacyKeys(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/cockpit", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, forbidden := range []string{
		`"completion_percentage"`, `"health"`, `"stage"`, `"variant"`,
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains forbidden key %s:\n%s", forbidden, body)
		}
	}
}

// TestProjectCockpitVariantQueryIgnored asserts the plan's binding
// constraint that ?variant=A|B|C must never change the mode: Part 1 does not
// even read the query string, so this proves the negative rather than
// merely assuming it.
func TestProjectCockpitVariantQueryIgnored(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	for _, variant := range []string{"", "A", "B", "C"} {
		path := "/api/v1/projects/proj/cockpit"
		if variant != "" {
			path += "?variant=" + variant
		}
		rr := doReq(t, h, "GET", path, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("variant %q: status = %d, body %s", variant, rr.Code, rr.Body.String())
		}
		var got struct {
			Mode struct {
				Name string `json:"name"`
			} `json:"mode"`
		}
		decodeInto(t, rr, &got)
		if got.Mode.Name != "operations" {
			t.Errorf("variant %q: mode.name = %q, want operations", variant, got.Mode.Name)
		}
	}
}
