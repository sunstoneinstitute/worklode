//go:build e2e

// create_test.go proves the cockpit's two creation surfaces end to end,
// through public surfaces only: a person opens the project Overview in a
// browser, follows the affordances to the new-task and new-deliverable forms,
// submits them as a browser submits a form, and finds the created objects on
// the pages that read them back. Nothing here writes to the store directly.
package e2e

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// postForm submits an urlencoded form the way a browser does — including the
// Origin and Sec-Fetch-Site headers a browser sends — without following the
// redirect, and returns the status and Location.
func postForm(t *testing.T, base, path string, form url.Values) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request for %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", base)
	req.Header.Set("Sec-Fetch-Site", "same-origin")

	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header.Get("Location")
}

// TestCreateFromCockpitPublicSurface follows one person through both
// creation flows and back out through the pages that read the results.
func TestCreateFromCockpitPublicSurface(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, cli.CreateProjectInput{
		ID: "proj", Name: "Proj", Key: "WL",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// --- Step 1: the Overview offers the way in ------------------------------

	code, body := getPage(t, srv.URL+"/projects/proj")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/proj: status = %d, want 200", code)
	}
	for _, want := range []string{`href="/projects/proj/tasks/new"`, `href="/projects/proj/deliverables"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the Overview does not link to %s:\n%s", want, body)
		}
	}

	// --- Step 2: create a task through the form ------------------------------

	code, body = getPage(t, srv.URL+"/projects/proj/tasks/new")
	if code != http.StatusOK {
		t.Fatalf("GET the new-task form: status = %d, want 200", code)
	}
	if !strings.Contains(body, `action="/projects/proj/tasks"`) {
		t.Fatalf("the new-task form does not post to the project's task route:\n%s", body)
	}

	code, location := postForm(t, srv.URL, "/projects/proj/tasks", url.Values{
		"title":    {"Count the casualties"},
		"body":     {"Every record needs two independent sources."},
		"priority": {"high"},
		"kind":     {"feature"},
	})
	if code != http.StatusSeeOther || location != "/tasks/WL-1" {
		t.Fatalf("task submit = %d %q, want 303 /tasks/WL-1", code, location)
	}

	// The created task reads back on its own page and on the board, through
	// the ordinary read paths.
	code, body = getPage(t, srv.URL+location)
	if code != http.StatusOK || !strings.Contains(body, "Count the casualties") {
		t.Fatalf("GET %s: status = %d, body does not show the created task:\n%s", location, code, body)
	}
	if !strings.Contains(body, "Every record needs two independent sources.") {
		t.Fatalf("the task page does not show the submitted body:\n%s", body)
	}
	code, body = getPage(t, srv.URL+"/work")
	if code != http.StatusOK || !strings.Contains(body, "Count the casualties") {
		t.Fatalf("GET /work: status = %d, board does not carry the new task:\n%s", code, body)
	}

	// The API — a different surface entirely — sees the same task, which is
	// what makes this one backbone rather than a browser-only record.
	task, _, err := admin.GetTask(ctx, "WL-1")
	if err != nil {
		t.Fatalf("get the created task over the API: %v", err)
	}
	if task.Title != "Count the casualties" || task.State != "ready" {
		t.Fatalf("API view of the created task = %+v, want the submitted title and state ready", task)
	}

	// --- Step 3: declare a deliverable through the form ----------------------

	code, body = getPage(t, srv.URL+"/projects/proj/deliverables")
	if code != http.StatusOK {
		t.Fatalf("GET the deliverables page: status = %d, want 200", code)
	}
	if !strings.Contains(body, "No deliverable is declared for this project yet") {
		t.Fatalf("the empty deliverables page is not honest about being empty:\n%s", body)
	}

	code, location = postForm(t, srv.URL, "/projects/proj/deliverables", url.Values{
		"name":        {"Casualty datapackage"},
		"description": {"Frictionless datapackage of verified records."},
		"url":         {"https://example.org/data/casualties"},
	})
	if code != http.StatusSeeOther || location != "/projects/proj/deliverables" {
		t.Fatalf("deliverable submit = %d %q, want 303 /projects/proj/deliverables", code, location)
	}

	code, body = getPage(t, srv.URL+location)
	if code != http.StatusOK {
		t.Fatalf("GET the deliverables page after declaring: status = %d, want 200", code)
	}
	for _, want := range []string{
		"WL-DEL-1", "Casualty datapackage", "https://example.org/data/casualties",
		"Declared", "reported, never declared",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the deliverables page is missing %q:\n%s", want, body)
		}
	}

	// --- Step 4: a rejected submit keeps what was typed and writes nothing ---

	rejected := url.Values{"name": {""}, "description": {"a description worth keeping"}}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/projects/proj/deliverables",
		strings.NewReader(rejected.Encode()))
	if err != nil {
		t.Fatalf("build rejected request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", srv.URL)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST the rejected deliverable: %v", err)
	}
	rejectedBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("rejected submit status = %d, want 422", resp.StatusCode)
	}
	if !strings.Contains(string(rejectedBody), "a description worth keeping") {
		t.Fatalf("the re-rendered form lost what was typed:\n%s", rejectedBody)
	}

	// --- Step 5: a cross-site submit is refused ------------------------------

	crossReq, err := http.NewRequest(http.MethodPost, srv.URL+"/projects/proj/tasks",
		strings.NewReader(url.Values{"title": {"forged"}, "priority": {"low"}, "kind": {"chore"}}.Encode()))
	if err != nil {
		t.Fatalf("build cross-site request: %v", err)
	}
	crossReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	crossReq.Header.Set("Origin", "https://evil.example")
	crossReq.Header.Set("Sec-Fetch-Site", "cross-site")
	crossResp, err := http.DefaultClient.Do(crossReq)
	if err != nil {
		t.Fatalf("POST the cross-site form: %v", err)
	}
	io.Copy(io.Discard, crossResp.Body)
	crossResp.Body.Close()
	if crossResp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site submit status = %d, want 403", crossResp.StatusCode)
	}

	// Exactly the one task and the one deliverable that were meant to land.
	list, _, err := admin.ListTasks(ctx, cli.TaskListFilter{Project: "proj"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(list.Tasks) != 1 {
		t.Fatalf("project holds %d tasks, want exactly the one created through the form", len(list.Tasks))
	}
	code, body = getPage(t, srv.URL+"/projects/proj/deliverables")
	if code != http.StatusOK || strings.Count(body, "WL-DEL-") != 1 {
		t.Fatalf("project holds %d deliverables, want exactly one:\n%s", strings.Count(body, "WL-DEL-"), body)
	}
}
