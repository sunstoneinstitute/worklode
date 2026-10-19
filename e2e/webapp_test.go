//go:build e2e

// webapp_test.go drives the one-host web app over public surfaces only: a
// spaRoutes path is the SPA shell (WL-REQ-1304), every other cockpit path is
// templ (WL-REQ-1303), one login session covers both (WL-REQ-1308), /api/v1
// reads and the write checks on a non-bearer write (WL-REQ-1307,
// WL-REQ-1770), the project summary and Overview shell (WL-REQ-1797), the Documents register
// (WL-REQ-1805), the Ideas API and pages (WL-REQ-1799, WL-REQ-1806), and the OpenAPI document's security schemes (WL-REQ-1309).
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/cli"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/oidc/oidctest"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

func TestWebAppOneHost(t *testing.T) {
	ctx := context.Background()

	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)
	handler, _, err := api.NewServer(st, api.Config{
		BootstrapToken: bootstrapToken,
		OIDCIssuer:     iss.URL(),
		OIDCClientID:   iss.ClientID,
		PublicURL:      "http://localhost:8080",
		SessionSecret:  "e2e-session-secret",
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	srv := httptest.NewServer(handler)
	defer srv.Close()

	admin := cli.NewClient(cli.Config{ServerURL: srv.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "web", Name: "Web Project", Key: "WEB",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}

	do := func(method, path, body string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rd)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read %s %s: %v", method, path, err)
		}
		return resp, string(b)
	}
	wantStatus := func(resp *http.Response, body string, want int) {
		t.Helper()
		if resp.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d (body %s)", resp.Request.Method,
				resp.Request.URL.RequestURI(), resp.StatusCode, want, body)
		}
	}

	// 1. Signed out, the SPA path and a templ page both send the browser to
	// the one login.
	for _, path := range []string{"/projects", "/reviews"} {
		resp, body := do(http.MethodGet, path, "", nil)
		wantStatus(resp, body, http.StatusFound)
		if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/auth/login") {
			t.Errorf("signed-out %s Location = %q, want /auth/login", path, loc)
		}
	}

	session := webSession(t, srv.URL, iss, "alice")
	cookie := map[string]string{"Cookie": "wl_session=" + session}

	// 2. /projects is the SPA shell.
	resp, body := do(http.MethodGet, "/projects", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("/projects Content-Type = %q, want text/html", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("/projects Cache-Control = %q, want no-cache", cc)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Error("/projects carries no Content-Security-Policy")
	}
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/projects rendered the templ cockpit, want the SPA shell")
	}

	// 2b. A project's Crew page is the SPA shell too (WL-REQ-1802).
	resp, body = do(http.MethodGet, "/projects/web/crew", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/projects/web/crew rendered the templ cockpit, want the SPA shell")
	}

	// 2c. Both task pages are the SPA shell (WL-REQ-1803).
	for _, path := range []string{"/tasks", "/projects/web/tasks"} {
		resp, body = do(http.MethodGet, path, "", cookie)
		wantStatus(resp, body, http.StatusOK)
		if strings.Contains(body, `<nav aria-label="Primary"`) {
			t.Errorf("%s rendered the templ cockpit, want the SPA shell", path)
		}
	}

	// 3. /reviews is still the templ cockpit, on the same session.
	resp, body = do(http.MethodGet, "/reviews", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if !strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Errorf("/reviews is not the templ cockpit: %.200s", body)
	}

	// 4. /api/v1: a session read, then a session write without and with the
	// write checks, then a bearer write.
	resp, body = do(http.MethodGet, "/api/v1/projects", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	var list model.ProjectListResponse
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("decode projects: %v (body %s)", err, body)
	}
	if len(list.Projects) != 1 || list.Projects[0].ID != "web" {
		t.Errorf("projects = %+v, want the one project \"web\"", list.Projects)
	}

	task := `{"project":"web","title":"From the web app","priority":"medium","kind":"feature"}`
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, cookie)
	wantStatus(resp, body, http.StatusForbidden)
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("refused write Content-Type = %q, want JSON", ct)
	}
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, map[string]string{
		"Cookie":           "wl_session=" + session,
		"Sec-Fetch-Site":   "same-origin",
		"X-Requested-With": "lode-web",
		"Content-Type":     "application/json",
	})
	wantStatus(resp, body, http.StatusCreated)
	resp, body = do(http.MethodPost, "/api/v1/tasks", task, map[string]string{
		"Authorization": "Bearer " + bootstrapToken,
	})
	wantStatus(resp, body, http.StatusCreated)

	// 4b. The Crew page's two surfaces on the session: the instance roster,
	// then add, list and remove a participant (WL-REQ-1795, WL-REQ-1802).
	webWrite := map[string]string{
		"Cookie":           "wl_session=" + session,
		"Sec-Fetch-Site":   "same-origin",
		"X-Requested-With": "lode-web",
		"Content-Type":     "application/json",
	}
	webSession(t, srv.URL, iss, "bob")
	resp, body = do(http.MethodGet, "/api/v1/crew", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	var roster model.CrewListResponse
	if err := json.Unmarshal([]byte(body), &roster); err != nil {
		t.Fatalf("decode crew: %v (body %s)", err, body)
	}
	have := map[string]bool{}
	for _, m := range roster.Crew {
		have[m.Actor] = true
	}
	if !have["alice"] || !have["bob"] {
		t.Fatalf("crew = %+v, want both alice and bob", roster.Crew)
	}
	participants := func() []model.CrewMember {
		t.Helper()
		resp, body := do(http.MethodGet, "/api/v1/projects/web/participants", "", cookie)
		wantStatus(resp, body, http.StatusOK)
		var pl model.ParticipantListResponse
		if err := json.Unmarshal([]byte(body), &pl); err != nil {
			t.Fatalf("decode participants: %v (body %s)", err, body)
		}
		return pl.Participants
	}
	resp, body = do(http.MethodPost, "/api/v1/projects/web/participants", `{"actor":"bob"}`, webWrite)
	wantStatus(resp, body, http.StatusCreated)
	if ps := participants(); len(ps) != 1 || ps[0].Actor != "bob" {
		t.Errorf("participants after add = %+v, want just bob", ps)
	}
	resp, body = do(http.MethodDelete, "/api/v1/projects/web/participants/bob", "", webWrite)
	wantStatus(resp, body, http.StatusNoContent)
	if ps := participants(); len(ps) != 0 {
		t.Errorf("participants after remove = %+v, want none", ps)
	}

	// 4c. A task's due date: set on create, listed, cleared by PATCH ""
	// (WL-REQ-1796, WL-REQ-1803).
	dueOf := func(id string) string {
		t.Helper()
		resp, body := do(http.MethodGet, "/api/v1/tasks?project=web", "", cookie)
		wantStatus(resp, body, http.StatusOK)
		var tl model.TaskListResponse
		if err := json.Unmarshal([]byte(body), &tl); err != nil {
			t.Fatalf("decode tasks: %v (body %s)", err, body)
		}
		for _, tk := range tl.Tasks {
			if tk.ID == id {
				return tk.Due
			}
		}
		t.Fatalf("task %s missing from GET /api/v1/tasks?project=web", id)
		return ""
	}
	resp, body = do(http.MethodPost, "/api/v1/tasks",
		`{"project":"web","title":"Due soon","priority":"medium","kind":"feature","due":"2026-12-31"}`, webWrite)
	wantStatus(resp, body, http.StatusCreated)
	var dueTask model.Task
	if err := json.Unmarshal([]byte(body), &dueTask); err != nil {
		t.Fatalf("decode created task: %v (body %s)", err, body)
	}
	if got := dueOf(dueTask.ID); got != "2026-12-31" {
		t.Errorf("listed due = %q, want 2026-12-31", got)
	}
	resp, body = do(http.MethodPatch, "/api/v1/tasks/"+dueTask.ID, `{"due":""}`, webWrite)
	wantStatus(resp, body, http.StatusOK)
	if got := dueOf(dueTask.ID); got != "" {
		t.Errorf("due after PATCH \"\" = %q, want cleared", got)
	}

	// 4d. The project summary lists a assigned past-due task, and its
	// assignee reads overbooked; the Overview page is the SPA shell
	// (WL-REQ-1797, WL-REQ-1804).
	resp, body = do(http.MethodPost, "/api/v1/tasks",
		`{"project":"web","title":"Overdue","priority":"medium","kind":"feature","due":"2020-01-01"}`, webWrite)
	wantStatus(resp, body, http.StatusCreated)
	var lateTask model.Task
	if err := json.Unmarshal([]byte(body), &lateTask); err != nil {
		t.Fatalf("decode created task: %v (body %s)", err, body)
	}
	resp, body = do(http.MethodPost, "/api/v1/projects/web/participants", `{"actor":"alice"}`, webWrite)
	wantStatus(resp, body, http.StatusCreated)
	resp, body = do(http.MethodPost, "/api/v1/tasks/"+lateTask.ID+"/assign", `{"assignee":"alice"}`, webWrite)
	wantStatus(resp, body, http.StatusOK)
	resp, body = do(http.MethodGet, "/api/v1/projects/web/summary", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	var sum model.ProjectSummary
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatalf("decode summary: %v (body %s)", err, body)
	}
	if len(sum.PastDue) != 1 || sum.PastDue[0].ID != lateTask.ID {
		t.Errorf("summary past_due = %+v, want just %s", sum.PastDue, lateTask.ID)
	}
	if len(sum.Workloads) != 1 || sum.Workloads[0].Actor != "alice" || sum.Workloads[0].Availability != "overbooked" {
		t.Errorf("summary workloads = %+v, want alice overbooked", sum.Workloads)
	}
	resp, body = do(http.MethodGet, "/projects/web", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/projects/web rendered the templ cockpit, want the SPA shell")
	}

	// 4e. The Documents register on the session: register, list, patch and
	// delete a link; the spec and rule reads answer; the page is the SPA shell
	// (WL-REQ-1805).
	resp, body = do(http.MethodPost, "/api/v1/projects/web/documents",
		`{"name":"Interview notes","category":"meeting-notes","type":"document","status":"draft","url":"https://example.com/notes","date":"2026-10-01"}`, webWrite)
	wantStatus(resp, body, http.StatusCreated)
	var regDoc model.ProjectDocument
	if err := json.Unmarshal([]byte(body), &regDoc); err != nil || regDoc.ID == "" {
		t.Fatalf("decode created document: %v (body %s)", err, body)
	}
	resp, body = do(http.MethodGet, "/api/v1/projects/web/documents", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	var regList model.ProjectDocumentListResponse
	if err := json.Unmarshal([]byte(body), &regList); err != nil {
		t.Fatalf("decode documents: %v (body %s)", err, body)
	}
	if len(regList.Documents) != 1 || regList.Documents[0].ID != regDoc.ID {
		t.Errorf("documents = %+v, want just %s", regList.Documents, regDoc.ID)
	}
	resp, body = do(http.MethodPatch, "/api/v1/documents/"+regDoc.ID, `{"status":"final"}`, webWrite)
	wantStatus(resp, body, http.StatusOK)
	if !strings.Contains(body, `"final"`) {
		t.Errorf("patched document = %s, want status final", body)
	}
	resp, body = do(http.MethodDelete, "/api/v1/documents/"+regDoc.ID, "", webWrite)
	wantStatus(resp, body, http.StatusNoContent)
	resp, body = do(http.MethodGet, "/api/v1/docs?project=web&kind=spec", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	resp, body = do(http.MethodGet, "/api/v1/rules?project=web", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	resp, body = do(http.MethodGet, "/projects/web/documents", "", cookie)
	wantStatus(resp, body, http.StatusOK)
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/projects/web/documents rendered the templ cockpit, want the SPA shell")
	}

	// 5. /static/ belongs to the SPA, /assets/ to the templ cockpit.
	resp, body = do(http.MethodGet, "/static/nope.js", "", nil)
	wantStatus(resp, body, http.StatusNotFound)
	resp, body = do(http.MethodGet, "/assets/app.css", "", nil)
	wantStatus(resp, body, http.StatusOK)

	// 6. The OpenAPI document declares both ways in.
	resp, body = do(http.MethodGet, "/api/v1/openapi.json", "", nil)
	wantStatus(resp, body, http.StatusOK)
	var doc struct {
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode openapi.json: %v", err)
	}
	for _, name := range []string{"bearerToken", "sessionCookie"} {
		if _, ok := doc.Components.SecuritySchemes[name]; !ok {
			t.Errorf("openapi.json securitySchemes lacks %q", name)
		}
	}
}

// TestWebAppIdeas proves ideas are private to their author until shared, that
// sharing mints a design task in the intake project, and that an instance
// without LODE_INTAKE_PROJECT refuses the share but still captures
// (WL-REQ-1799, WL-REQ-1806).
func TestWebAppIdeas(t *testing.T) {
	ctx := context.Background()
	st := store.OpenTestStore(t)
	iss := oidctest.NewIssuer(t)
	newServer := func(intake string) *httptest.Server {
		t.Helper()
		h, _, err := api.NewServer(st, api.Config{
			BootstrapToken: bootstrapToken,
			OIDCIssuer:     iss.URL(),
			OIDCClientID:   iss.ClientID,
			PublicURL:      "http://localhost:8080",
			SessionSecret:  "e2e-session-secret",
			IntakeProject:  intake,
		})
		if err != nil {
			t.Fatalf("new server (intake %q): %v", intake, err)
		}
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv
	}

	// The intake project must exist before a server boots with it set.
	bare := newServer("")
	admin := cli.NewClient(cli.Config{ServerURL: bare.URL, Token: bootstrapToken})
	if _, _, err := admin.CreateProject(ctx, model.CreateProjectInput{
		ID: "intake", Name: "Intake", Key: "INT",
	}); err != nil {
		t.Fatalf("create project: %v", err)
	}
	srv := newServer("intake")

	call := func(base, method, path, body string, hdr map[string]string) (*http.Response, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, base+path, rd)
		if err != nil {
			t.Fatalf("build %s %s: %v", method, path, err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := noRedirect.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	want := func(resp *http.Response, body string, status int) {
		t.Helper()
		if resp.StatusCode != status {
			t.Fatalf("%s %s = %d, want %d (body %s)", resp.Request.Method,
				resp.Request.URL.RequestURI(), resp.StatusCode, status, body)
		}
	}
	writer := func(session string) map[string]string {
		return map[string]string{
			"Cookie":           "wl_session=" + session,
			"Sec-Fetch-Site":   "same-origin",
			"X-Requested-With": "lode-web",
			"Content-Type":     "application/json",
		}
	}
	listIDs := func(base string, hdr map[string]string) []string {
		t.Helper()
		resp, body := call(base, http.MethodGet, "/api/v1/ideas", "", hdr)
		want(resp, body, http.StatusOK)
		var l model.IdeaListResponse
		if err := json.Unmarshal([]byte(body), &l); err != nil {
			t.Fatalf("decode ideas: %v (body %s)", err, body)
		}
		var ids []string
		for _, i := range l.Ideas {
			ids = append(ids, i.ID)
		}
		return ids
	}
	contains := func(ids []string, id string) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}

	a := writer(webSession(t, srv.URL, iss, "alice"))
	b := writer(webSession(t, srv.URL, iss, "bob"))
	adm := map[string]string{"Authorization": "Bearer " + bootstrapToken, "Content-Type": "application/json"}

	// 1. Alice captures a private idea. Bob and the admin cannot list or read it.
	resp, body := call(srv.URL, http.MethodPost, "/api/v1/ideas",
		`{"title":"Coastal wind subsidies","body":"Who profits?","type":"general"}`, a)
	want(resp, body, http.StatusCreated)
	var idea model.Idea
	if err := json.Unmarshal([]byte(body), &idea); err != nil || idea.ID == "" {
		t.Fatalf("decode created idea: %v (body %s)", err, body)
	}
	if idea.Status != "private" {
		t.Errorf("new idea status = %q, want private", idea.Status)
	}
	path := "/api/v1/ideas/" + idea.ID
	for name, hdr := range map[string]map[string]string{"bob": b, "admin": adm} {
		if contains(listIDs(srv.URL, hdr), idea.ID) {
			t.Errorf("%s lists alice's private idea", name)
		}
		resp, body = call(srv.URL, http.MethodGet, path, "", hdr)
		want(resp, body, http.StatusNotFound)
	}
	if !contains(listIDs(srv.URL, a), idea.ID) {
		t.Error("alice does not list her own idea")
	}

	// 2. Alice shares. The idea reaches the intake project as a design task.
	resp, body = call(srv.URL, http.MethodPost, path+"/share", "", a)
	want(resp, body, http.StatusOK)
	if err := json.Unmarshal([]byte(body), &idea); err != nil || idea.Task == "" || idea.Status != "intake" {
		t.Fatalf("shared idea = %s (err %v), want status intake with a task", body, err)
	}
	resp, body = call(srv.URL, http.MethodGet, "/api/v1/tasks?project=intake", "", b)
	want(resp, body, http.StatusOK)
	var tasks model.TaskListResponse
	if err := json.Unmarshal([]byte(body), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (body %s)", err, body)
	}
	found := false
	for _, tk := range tasks.Tasks {
		if tk.ID == idea.Task && tk.Kind == "design" && tk.Title == idea.Title {
			found = true
		}
	}
	if !found {
		t.Errorf("intake tasks = %s, want design task %s titled %q", body, idea.Task, idea.Title)
	}

	// 3. Bob now sees it, and cannot edit it.
	if !contains(listIDs(srv.URL, b), idea.ID) {
		t.Error("bob does not list the shared idea")
	}
	resp, body = call(srv.URL, http.MethodGet, path, "", b)
	want(resp, body, http.StatusOK)
	resp, body = call(srv.URL, http.MethodPatch, path, `{"title":"Hijacked"}`, b)
	want(resp, body, http.StatusForbidden)

	// 3b. Findings (WL-REQ-1800): the skills write with a token, the page reads
	// with the cookie. The admin token has an actor, so writes are allowed.
	resp, body = call(srv.URL, http.MethodPost, path+"/litmus-tests",
		`{"date":"2026-10-01","summary":"Passes","recommendations":["Narrow to offshore"]}`, adm)
	want(resp, body, http.StatusCreated)
	resp, body = call(srv.URL, http.MethodPut, path+"/pre-research",
		`{"date":"2026-10-02","findings":["Three subsidy schemes"]}`, adm)
	want(resp, body, http.StatusOK)
	resp, body = call(srv.URL, http.MethodGet, path+"/litmus-tests", "", b)
	want(resp, body, http.StatusOK)
	if !strings.Contains(body, "Narrow to offshore") {
		t.Errorf("litmus tests = %s, want the posted recommendation", body)
	}
	resp, body = call(srv.URL, http.MethodGet, path+"/pre-research", "", b)
	want(resp, body, http.StatusOK)
	if !strings.Contains(body, "Three subsidy schemes") {
		t.Errorf("pre-research = %s, want the put finding", body)
	}
	resp, body = call(srv.URL, http.MethodPost, path+"/sources",
		`{"name":"Subsidy register","url":"https://example.org/register","type":"dataset","status":"draft"}`, b)
	want(resp, body, http.StatusCreated)
	resp, body = call(srv.URL, http.MethodGet, path+"/sources", "", a)
	want(resp, body, http.StatusOK)
	if !strings.Contains(body, "Subsidy register") {
		t.Errorf("sources = %s, want the added source", body)
	}
	resp, body = call(srv.URL, http.MethodPatch, path+"/intake",
		`{"pre_research_authorized":true,"decision":"narrow"}`, b)
	want(resp, body, http.StatusOK)
	if !strings.Contains(body, `"decision":"narrow"`) {
		t.Errorf("patched intake = %s, want decision narrow", body)
	}
	resp, body = call(srv.URL, http.MethodGet, "/ideas/"+idea.ID+"/intake", "", map[string]string{"Cookie": b["Cookie"]})
	want(resp, body, http.StatusOK)
	if strings.Contains(body, `<nav aria-label="Primary"`) {
		t.Error("/ideas/{id}/intake rendered the templ cockpit, want the SPA shell")
	}

	// 3c. A private idea has no findings: 409 to its author, 404 to anyone else.
	resp, body = call(srv.URL, http.MethodPost, "/api/v1/ideas",
		`{"title":"Still private","body":"x","type":"general"}`, a)
	want(resp, body, http.StatusCreated)
	var priv model.Idea
	if err := json.Unmarshal([]byte(body), &priv); err != nil {
		t.Fatalf("decode idea: %v", err)
	}
	resp, body = call(srv.URL, http.MethodGet, "/api/v1/ideas/"+priv.ID+"/litmus-tests", "", a)
	want(resp, body, http.StatusConflict)
	resp, body = call(srv.URL, http.MethodGet, "/api/v1/ideas/"+priv.ID+"/litmus-tests", "", b)
	want(resp, body, http.StatusNotFound)

	// 4. Deleting the shared idea tombstones its task.
	resp, body = call(srv.URL, http.MethodDelete, path, "", a)
	want(resp, body, http.StatusNoContent)
	resp, body = call(srv.URL, http.MethodGet, "/api/v1/tasks?project=intake&deleted=true", "", b)
	want(resp, body, http.StatusOK)
	if !strings.Contains(body, idea.Task) {
		t.Errorf("tombstoned intake tasks = %s, want %s", body, idea.Task)
	}

	// 5. The pages are the SPA shell.
	for _, p := range []string{"/ideas", "/ideas/new", "/ideas/" + idea.ID + "/edit"} {
		resp, body = call(srv.URL, http.MethodGet, p, "", map[string]string{"Cookie": b["Cookie"]})
		want(resp, body, http.StatusOK)
		if strings.Contains(body, `<nav aria-label="Primary"`) {
			t.Errorf("%s rendered the templ cockpit, want the SPA shell", p)
		}
	}

	// 6. With no intake project, capture works and share is 409.
	resp, body = call(bare.URL, http.MethodPost, "/api/v1/ideas",
		`{"title":"Unshared","body":"x","type":"general"}`, a)
	want(resp, body, http.StatusCreated)
	var loose model.Idea
	if err := json.Unmarshal([]byte(body), &loose); err != nil {
		t.Fatalf("decode idea: %v", err)
	}
	resp, body = call(bare.URL, http.MethodPost, "/api/v1/ideas/"+loose.ID+"/share", "", a)
	want(resp, body, http.StatusConflict)
}
