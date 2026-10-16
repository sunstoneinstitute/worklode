//go:build e2e

// create_test.go proves the cockpit's creation surface end to end, through
// public surfaces only: a person opens the project Overview in a browser,
// follows the affordance to the new-deliverable form, submits it as a browser
// submits a form, and finds the created object on the page that reads it
// back. Creating a task is the SPA's task page (WL-REQ-1803). Nothing here
// writes to the store directly.
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
	"github.com/sunstoneinstitute/worklode/internal/model"
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

// TestCreateFromCockpitPublicSurface follows one person through the
// deliverable flow and back out through the page that reads the result.
func TestCreateFromCockpitPublicSurface(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	handler, _, err := api.NewServer(st, api.Config{BootstrapToken: bootstrapToken, WebOpen: true})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "proj", Name: "Proj", Key: "WL",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	// --- Step 1: the project sidebar offers the way in ----------------------

	code, body := getPage(t, srv.URL+"/projects/proj/milestones")
	if code != http.StatusOK {
		t.Fatalf("GET /projects/proj/milestones: status = %d, want 200", code)
	}
	for _, want := range []string{`href="/projects/proj/tasks"`, `href="/projects/proj/deliverables"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("the project sidebar does not link to %s:\n%s", want, body)
		}
	}

	// --- Step 2: declare a deliverable through the form ----------------------

	code, body = getPage(t, srv.URL+"/projects/proj/deliverables")
	if code != http.StatusOK {
		t.Fatalf("GET the deliverables page: status = %d, want 200", code)
	}
	if !strings.Contains(body, "No deliverable is declared for this project yet") {
		t.Fatalf("the empty deliverables page is not honest about being empty:\n%s", body)
	}

	code, location := postForm(t, srv.URL, "/projects/proj/deliverables", url.Values{
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

	// --- Step 3: a rejected submit keeps what was typed and writes nothing ---

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

	// --- Step 4: a cross-site submit is refused ------------------------------

	crossReq, err := http.NewRequest(http.MethodPost, srv.URL+"/projects/proj/deliverables",
		strings.NewReader(url.Values{"name": {"forged"}, "label": {"on"}}.Encode()))
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

	// Exactly the one deliverable that was meant to land.
	code, body = getPage(t, srv.URL+"/projects/proj/deliverables")
	if code != http.StatusOK || strings.Count(body, `class="dodrow"`) != 1 {
		t.Fatalf("project holds %d deliverables, want exactly one:\n%s", strings.Count(body, `class="dodrow"`), body)
	}
}
