package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

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

// --- new task ---------------------------------------------------------------

// TestNewTaskFormRenders checks the form offers every field the task needs,
// and that it does not offer the retired "epic" kind (specs 025 §6, 029 §2).
func TestNewTaskFormRenders(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/projects/proj/tasks/new", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	bodyContains(t, body,
		`<form method="post" action="/projects/proj/tasks"`,
		`name="title"`, `name="body"`, `name="priority"`, `name="kind"`,
		`name="concern"`, `name="draft"`,
	)
	main := mainContent(t, body)
	if strings.Contains(main, `value="epic"`) {
		t.Error("the new-task form offers the retired epic kind")
	}

	if rr := doReq(t, h, "GET", "/projects/nosuch/tasks/new", "", nil); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project form status = %d, want 404", rr.Code)
	}
}

// TestCreateTaskFromFormCreatesAndRedirects checks the happy path end to end:
// the task lands with the submitted fields, and the response is a 303 to the
// created task so a reload cannot create a second one.
func TestCreateTaskFromFormCreatesAndRedirects(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/tasks", url.Values{
		"title":    {"Wire the intake form"},
		"body":     {"The editor needs a title and a description."},
		"priority": {"high"},
		"kind":     {"feature"},
		"concern":  {"usability"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("Location"); got != "/tasks/WL-1" {
		t.Fatalf("Location = %q, want /tasks/WL-1", got)
	}

	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.Title != "Wire the intake form" || task.Priority != "high" ||
		task.Kind != "feature" || task.Concern != "usability" {
		t.Errorf("created task = %+v, want the submitted fields", task)
	}
	if task.State != "ready" {
		t.Errorf("state = %q, want ready (the draft box was not ticked)", task.State)
	}
	if task.Body != "The editor needs a title and a description." {
		t.Errorf("body = %q, want the submitted body", task.Body)
	}
}

// TestCreateTaskFromFormDraft checks the draft checkbox lands the task
// unclaimable rather than ready.
func TestCreateTaskFromFormDraft(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/tasks", url.Values{
		"title": {"Half-formed idea"}, "priority": {"low"}, "kind": {"spike"}, "draft": {"1"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.State != "draft" {
		t.Errorf("state = %q, want draft", task.State)
	}
}

// TestCreateTaskFromFormRejectsBadInput checks a rejected submit re-renders
// the form at 422 with the message and everything typed, and writes nothing.
func TestCreateTaskFromFormRejectsBadInput(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		name string
		form url.Values
		want string
	}{
		{"no title", url.Values{"title": {"  "}, "priority": {"high"}, "kind": {"bug"}}, "A title is required."},
		{"bad priority", url.Values{"title": {"x"}, "priority": {"urgent"}, "kind": {"bug"}}, "Choose a priority"},
		{"bad kind", url.Values{"title": {"x"}, "priority": {"high"}, "kind": {"saga"}}, "Choose one of the offered kinds."},
		{"bad concern", url.Values{"title": {"x"}, "priority": {"high"}, "kind": {"bug"}, "concern": {"vibes"}}, "Choose a concern"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rr := doForm(t, h, "/projects/proj/tasks", tt.form, nil)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
			bodyContains(t, rr.Body.String(), tt.want, `<form method="post"`)
		})
	}

	// The rejected values come back so nothing typed is lost.
	rr := doForm(t, h, "/projects/proj/tasks", url.Values{
		"title": {""}, "body": {"a body worth keeping"}, "priority": {"critical"}, "kind": {"bug"},
	}, nil)
	bodyContains(t, rr.Body.String(), "a body worth keeping", `value="critical" selected`)

	tasks, err := st.ListTasks(context.Background(), store.TaskFilter{})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("rejected submits created %d task(s), want 0", len(tasks))
	}
}

// TestCreateFormRefusesCrossOrigin checks both creation forms refuse a
// submission a browser marks as cross-site, and that nothing is written.
func TestCreateFormRefusesCrossOrigin(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for _, tt := range []struct {
		path    string
		form    url.Values
		headers map[string]string
	}{
		{"/projects/proj/tasks", url.Values{"title": {"x"}, "priority": {"high"}, "kind": {"bug"}},
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
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	for i, headers := range []map[string]string{
		{"Sec-Fetch-Site": "same-origin", "Origin": "https://evil.example"},
		{"Origin": "http://example.com"}, // httptest.NewRequest's host
	} {
		rr := doForm(t, h, "/projects/proj/tasks",
			url.Values{"title": {"same origin"}, "priority": {"low"}, "kind": {"chore"}}, headers)
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

// TestCreateDeliverableFromForm checks the happy path: the deliverable lands
// with its three descriptive fields, the response 303s back to the list, and
// the list then shows it as Declared.
func TestCreateDeliverableFromForm(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/deliverables", url.Values{
		"name":        {"Casualty datapackage"},
		"description": {"Frictionless datapackage of verified records."},
		"url":         {"https://example.org/data/casualties"},
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

	body := doReq(t, h, "GET", "/projects/proj/deliverables", "", nil).Body.String()
	bodyContains(t, body, "WL-DEL-1", "Casualty datapackage",
		"https://example.org/data/casualties", "Declared")
}

// TestCreateDeliverableFromFormRejectsBadInput checks the two validation
// rules a person can trip, including the URL scheme guard that keeps a
// javascript: address out of the stored row entirely.
func TestCreateDeliverableFromFormRejectsBadInput(t *testing.T) {
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

// TestDeliverableFormOffersNoStateControl pins spec 029 §3.2: the form asks
// for a declaration, never for a state a person could assert.
func TestDeliverableFormOffersNoStateControl(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	main := mainContent(t, doReq(t, h, "GET", "/projects/proj/deliverables/new", "", nil).Body.String())
	for _, forbidden := range []string{`name="state"`, `name="status"`, `name="published"`} {
		if strings.Contains(main, forbidden) {
			t.Errorf("the deliverable form offers %s, which §3.2 makes a reported fact", forbidden)
		}
	}
}

// TestCockpitLinksToNewTask checks the project Overview carries the
// affordance that makes the cockpit writable at all.
func TestCockpitLinksToNewTask(t *testing.T) {
	st, h, _ := newTestServer(t)
	createProject(t, st, "proj")

	body := doReq(t, h, "GET", "/projects/proj", "", nil).Body.String()
	bodyContains(t, body, `href="/projects/proj/tasks/new"`, "New task")
}

// --- JSON API ---------------------------------------------------------------

// TestDeliverablesAPI checks the JSON surface the web form shares its
// validation and write path with: create, list, project scoping, and the
// same rejections.
func TestDeliverablesAPI(t *testing.T) {
	st, h, token := newTestServer(t)
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
