package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// doForm submits an application/x-www-form-urlencoded body the way a browser
// would, and returns the recorder without following the redirect. headers are
// applied last, so a test can add or override Origin/Sec-Fetch-Site.
func doForm(t *testing.T, h http.Handler, path string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// --- form guards ------------------------------------------------------------

// TestRejectedSubmitTitlesThePageAsAnError holds the half of a rejected
// submit's announcement that lives on the server. A browser reads the new
// document's <title> before anything in it, so on a form that comes back
// rejected the title is what tells a screen-reader user the submit failed;
// the message itself takes focus (internal/ui/forms.templ). Neither half is
// worth much alone, which is why both are tested.
func TestRejectedSubmitTitlesThePageAsAnError(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		name, path string
		bad        url.Values
	}{
		{"deliverable", "/projects/proj/deliverables", url.Values{"name": {""}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rejected := doForm(t, h, tt.path, tt.bad, nil).Body.String()
			bodyContains(t, rejected, "<title>Error: worklode: ",
				`<p id="form-error" class="formerr" tabindex="-1" autofocus>`)
			if strings.Contains(rejected, "aria-live") {
				t.Error("the rejected form still carries an aria-live region, which announces nothing across a page load")
			}
			if n := strings.Count(rejected, "autofocus"); n != 1 {
				t.Errorf("%d elements carry autofocus on a rejected submit, want exactly 1 (the message)", n)
			}

			// The form's own GET is where the clean title shows: an accepted
			// submit redirects rather than rendering.
			first := doReq(t, h, "GET", tt.path+"/new", "", nil).Body.String()
			if strings.Contains(first, "<title>Error:") {
				t.Error("a first render titles itself an error")
			}
			if !strings.Contains(first, "autofocus") {
				t.Error("a first render should still autofocus its first field")
			}
		})
	}
}

// TestCreateFormRefusesCrossOrigin checks the creation form refuses a
// submission a browser marks as cross-site or sends from another origin, and that nothing is written.
func TestCreateFormRefusesCrossOrigin(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		path    string
		form    url.Values
		headers map[string]string
	}{
		{"/projects/proj/deliverables", url.Values{"name": {"x"}, "label": {"on"}},
			map[string]string{"Sec-Fetch-Site": "cross-site"}},
		{"/projects/proj/deliverables", url.Values{"name": {"x"}},
			map[string]string{"Origin": "https://evil.example"}},
	} {
		rr := doForm(t, h, tt.path, tt.form, tt.headers)
		if rr.Code != http.StatusForbidden {
			t.Errorf("POST %s with %v status = %d, want 403", tt.path, tt.headers, rr.Code)
		}
	}

	ctx := context.Background()
	tasks, err := st.ListTasks(ctx, store.TaskFilter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	items, err := st.ListDeliverables(ctx, "proj")
	if err != nil {
		t.Fatalf("list deliverables: %v", err)
	}
	if len(tasks) != 0 || len(items) != 0 {
		t.Errorf("refused submits wrote %d task(s) and %d deliverable(s), want none", len(tasks), len(items))
	}
}

// TestCreateFormAcceptsSameOrigin checks the origin guard lets a genuine
// same-origin submission through, by both signals a browser sends.
func TestCreateFormAcceptsSameOrigin(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for i, headers := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin", "Origin": "https://evil.example"},
		{"Origin": "http://example.com"}, // httptest.NewRequest's host
	} {
		rr := doForm(t, h, "/projects/proj/deliverables",
			url.Values{"name": {fmt.Sprintf("same origin %d", i)}, "label": {"on"}}, headers)
		if rr.Code != http.StatusSeeOther {
			t.Errorf("same-origin submit %d status = %d, want 303; body %s", i, rr.Code, rr.Body.String())
		}
	}
}

// --- deliverables -----------------------------------------------------------

// TestDeliverablesPageEmptyState checks the built destination replaces the
// placeholder: it offers the form, states honestly that nothing is declared,
// and never claims a state for a deliverable.
func TestDeliverablesPageEmptyState(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/deliverables", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertOneAriaCurrent(t, body)
	bodyContains(t, body,
		"No deliverable is declared for this project yet",
		`href="/projects/proj/deliverables/new"`,
		"reported, never declared",
	)
	if strings.Contains(body, "spec 029 §7.") {
		t.Error("the deliverables destination still renders its old placeholder message")
	}

	if rr := doReq(t, h, "GET", "/projects/nosuch/deliverables", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
	}
}

// TestDeliverablesPageGroupsByMilestone pins the grouped shape (WL-SPEC-75
// §2): two milestones each holding one deliverable, plus one unattached
// deliverable, render as three groups in the project's milestone order,
// unattached last — and every declared deliverable still shows, whichever
// group it lands in.
func TestDeliverablesPageGroupsByMilestone(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	mkMilestone := func(title string) string {
		rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token, map[string]any{"title": title})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create milestone %s status = %d, body %s", title, rr.Code, rr.Body.String())
		}
		return decodeMap(t, rr)["id"].(string)
	}
	first := mkMilestone("First review")
	second := mkMilestone("Second review")

	mkDeliverable := func(name, milestone string) {
		rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token,
			map[string]any{"name": name, "milestone": milestone})
		if rr.Code != http.StatusCreated {
			t.Fatalf("create deliverable %s status = %d, body %s", name, rr.Code, rr.Body.String())
		}
	}
	mkDeliverable("Second output", second)
	mkDeliverable("Unattached output", "")
	mkDeliverable("First output", first)

	body := doReq(t, h, "GET", "/projects/proj/deliverables", "", nil).Body.String()
	bodyContains(t, body, "First review", "First output",
		"Second review", "Second output", "No milestone", "Unattached output")

	pos := func(s string) int {
		i := strings.Index(body, s)
		if i < 0 {
			t.Fatalf("page is missing %q", s)
		}
		return i
	}
	// Milestone groups in position order (declaration order here), then the
	// unattached group last — never the deliverable declaration order, which
	// put the unattached one second.
	if !(pos("First review") < pos("First output") &&
		pos("First output") < pos("Second review") &&
		pos("Second review") < pos("Second output") &&
		pos("Second output") < pos("No milestone") &&
		pos("No milestone") < pos("Unattached output")) {
		t.Errorf("groups are not in milestone-position-then-unattached order:\n%s", body)
	}
}

// TestCreateDeliverableFromForm checks the happy path: the deliverable lands
// with its descriptive fields — including the artifact address the catalog
// ingest routes reports by (WL-254) — the response 303s back to the list, and
// the list then shows it as Declared.
func TestCreateDeliverableFromForm(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/deliverables", url.Values{
		"name":        {"Casualty datapackage"},
		"description": {"Frictionless datapackage of verified records."},
		"url":         {"https://example.org/data/casualties"},
		"artifact":    {"bigquery://proj.releases.casualties"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/projects/proj/deliverables" {
		t.Fatalf("Location = %q, want /projects/proj/deliverables", got)
	}

	items, err := st.ListDeliverables(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list deliverables: %v", err)
	}
	if len(items) != 1 || items[0].ID != "WL-DEL-1" || items[0].Name != "Casualty datapackage" {
		t.Fatalf("stored deliverables = %+v, want one WL-DEL-1", items)
	}
	if items[0].Artifact != "bigquery://proj.releases.casualties" {
		t.Fatalf("stored artifact = %q, want the declared catalog address", items[0].Artifact)
	}

	body := doReq(t, h, "GET", "/projects/proj/deliverables", "", nil).Body.String()
	bodyContains(t, body, "WL-DEL-1", "Casualty datapackage",
		"https://example.org/data/casualties", "Declared",
		"bigquery://proj.releases.casualties")
}

// TestCreateDeliverableFromFormByLabel checks the label checkbox path: a
// declaration with no artifact address gets a minted label instead, the
// event lands with source "web" like every other cockpit write, and the
// deliverables page renders the label row rather than an artifact one.
func TestCreateDeliverableFromFormByLabel(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/deliverables", url.Values{
		"name":  {"Datasets"},
		"label": {"on"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}

	items, err := st.ListDeliverables(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list deliverables: %v", err)
	}
	if len(items) != 1 || items[0].Label != "worklode.deliverable=WL/datasets" || items[0].Artifact != "" {
		t.Fatalf("stored deliverables = %+v, want one label deliverable", items)
	}

	events := storeEventsOfType(t, st, "deliverable.created", 1)
	if events[0].Source != "web" {
		t.Errorf("event source = %q, want web", events[0].Source)
	}

	body := doReq(t, h, "GET", "/projects/proj/deliverables", "", nil).Body.String()
	bodyContains(t, body, "identified by label", "worklode.deliverable=WL/datasets")
}

// TestCreateDeliverableFromFormRejectsBadInput checks the two validation
// rules a person can trip, including the URL scheme guard that keeps a
// javascript: address out of the stored row entirely.
func TestCreateDeliverableFromFormRejectsBadInput(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		name string
		form url.Values
		want string
	}{
		{"no name", url.Values{"name": {"  "}}, "A name is required."},
		{"script url", url.Values{"name": {"x"}, "url": {"javascript:alert(1)"}}, "absolute http:// or https://"},
		{"relative url", url.Values{"name": {"x"}, "url": {"/data/casualties"}}, "absolute http:// or https://"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := doForm(t, h, "/projects/proj/deliverables", tt.form, nil)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			bodyContains(t, rr.Body.String(), tt.want, `<form method="post"`)
		})
	}

	items, err := st.ListDeliverables(context.Background(), "proj")
	if err != nil {
		t.Fatalf("list deliverables: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("rejected submits created %d deliverable(s), want 0", len(items))
	}
}

// TestDeliverableFormOffersNoStateControl pins WL-RULE-121: the form asks
// for a declaration, never for a state a person could assert.
func TestDeliverableFormOffersNoStateControl(t *testing.T) {
	t.Parallel()
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	main := mainContent(t, doReq(t, h, "GET", "/projects/proj/deliverables/new", "", nil).Body.String())
	for _, forbidden := range []string{`name="state"`, `name="status"`, `name="published"`} {
		if strings.Contains(main, forbidden) {
			t.Errorf("the deliverable form offers %s, which §3.2 makes a reported fact", forbidden)
		}
	}
	if !strings.Contains(main, `name="artifact"`) {
		t.Error("the deliverable form offers no artifact input (WL-254)")
	}
}

// --- JSON API ---------------------------------------------------------------

// TestDeliverablesAPI checks the JSON surface the web form shares its
// validation and write path with: create, list, project scoping, and the
// same rejections.
func TestDeliverablesAPI(t *testing.T) {
	t.Parallel()
	// A closed instance: the trailing assertion in this test expects an
	// anonymous request to 401, which WL-1115 reserves for "no credential at
	// all" — a WebOpen instance would hand it the open subject instead.
	st, h, token := newTestServerWithConfig(t, api.Config{})
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token, map[string]any{
		"name": "Methodology note", "description": "How the counting works.",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	created := decodeMap(t, rr)
	if created["id"] != "WL-DEL-1" || created["project"] != "proj" {
		t.Errorf("created = %v, want id WL-DEL-1 in project proj", created)
	}
	if _, ok := created["state"]; ok {
		t.Error("the deliverable JSON carries a state field; spec 029 §3.2 stores none")
	}
	if created["created_by"] != "alice" {
		t.Errorf("created_by = %v, want the authenticated actor", created["created_by"])
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/deliverables", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	list, ok := decodeMap(t, rr)["deliverables"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("list = %v, want one deliverable", decodeMap(t, rr))
	}

	for _, tt := range []struct {
		name string
		body map[string]any
	}{
		{"no name", map[string]any{"name": " "}},
		{"script url", map[string]any{"name": "x", "url": "javascript:alert(1)"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token, tt.body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
		})
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/nosuch/deliverables", token, map[string]any{"name": "x"})
	if rr.Code != http.StatusNotFound {
		t.Errorf("unknown project status = %d, want 404", rr.Code)
	}
	rr = doReq(t, h, "GET", "/api/v1/projects/proj/deliverables", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list status = %d, want 401", rr.Code)
	}
}
