package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// decodeIdea reads an idea response body, failing the test on any other status.
func decodeIdea(t *testing.T, rr *httptest.ResponseRecorder, want int) model.Idea {
	t.Helper()
	if rr.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, want, rr.Body)
	}
	var i model.Idea
	if err := json.Unmarshal(rr.Body.Bytes(), &i); err != nil {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
	return i
}

func listIdeaIDs(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d; body %s", rr.Code, rr.Body)
	}
	var l model.IdeaListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &l); err != nil {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
	ids := make([]string, 0, len(l.Ideas))
	for _, i := range l.Ideas {
		ids = append(ids, i.ID)
	}
	return ids
}

func wantErr(t *testing.T, rr *httptest.ResponseRecorder, code int, msg string) {
	t.Helper()
	if rr.Code != code {
		t.Fatalf("status = %d, want %d; body %s", rr.Code, code, rr.Body)
	}
	if msg == "" {
		return
	}
	var e model.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil || e.Error != msg {
		t.Fatalf("error = %q (%v), want %q", rr.Body, err, msg)
	}
}

// openReq sends an /api/v1 request with no bearer token, as the open
// subject on a LODE_WEB_OPEN server, carrying the headers a page-script
// write needs (WL-REQ-1770).
func openReq(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Requested-With", "lode-web")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// TestIdeasAPI drives the private/shared matrix of WL-REQ-1799 over HTTP:
// a private idea is invisible to everyone but its author, an admin included.
func TestIdeasAPI(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	admin := seedActor(t, st, "alice", "human", "Alice", true)
	bob := seedActor(t, st, "bob", "human", "Bob", false)
	createProject(t, st, "intake")
	h, metricsH, err := api.NewServer(st, api.Config{WebOpen: true, IntakeProject: "intake"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	priv := decodeIdea(t, doReq(t, h, "POST", "/api/v1/ideas", bob,
		map[string]any{"title": "Private thought", "body": "secret", "type": "general"}), http.StatusCreated)
	if priv.Author != "bob" || priv.Status != "private" || priv.Task != "" {
		t.Fatalf("created = %+v", priv)
	}
	p := "/api/v1/ideas/" + priv.ID

	// Every read and write path refuses the admin a private idea with 404.
	if ids := listIdeaIDs(t, doReq(t, h, "GET", "/api/v1/ideas", admin, nil)); len(ids) != 0 {
		t.Fatalf("admin list = %v, want none", ids)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", p, nil},
		{"PATCH", p, map[string]any{"title": "x"}},
		{"POST", p + "/share", nil},
		{"DELETE", p, nil},
		{"PATCH", p + "/intake", map[string]any{"decision": "park"}},
	} {
		if rr := doReq(t, h, c.method, c.path, admin, c.body); rr.Code != http.StatusNotFound {
			t.Errorf("admin %s %s = %d, want 404; body %s", c.method, c.path, rr.Code, rr.Body)
		}
	}
	// The open subject cannot read it either, and cannot create.
	if ids := listIdeaIDs(t, openReq(h, "GET", "/api/v1/ideas", "")); len(ids) != 0 {
		t.Fatalf("open list = %v, want none", ids)
	}
	wantErr(t, openReq(h, "GET", p, ""), http.StatusNotFound, "")
	wantErr(t, openReq(h, "POST", "/api/v1/ideas", `{"title":"x","type":"general"}`), http.StatusBadRequest,
		"idea needs an author: invalid input")

	// The author's intake-side act on a private idea is 409.
	wantErr(t, doReq(t, h, "PATCH", p+"/intake", bob, map[string]any{"decision": "park"}),
		http.StatusConflict, "idea not shared")

	// Field and body errors.
	rr := doReq(t, h, "POST", "/api/v1/ideas", bob, map[string]any{"title": " ", "type": "general"})
	wantErr(t, rr, http.StatusUnprocessableEntity, "")
	var fe model.ErrorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &fe)
	if fe.Error == "" || fe.Fields["title"] == "" {
		t.Fatalf("empty title body = %s, want error and fields.title", rr.Body)
	}
	rr = doReq(t, h, "PATCH", p, bob, map[string]any{"type": "memo"})
	_ = json.Unmarshal(rr.Body.Bytes(), &fe)
	if rr.Code != http.StatusUnprocessableEntity || fe.Fields["type"] == "" {
		t.Fatalf("bad type = %d %s, want 422 with fields.type", rr.Code, rr.Body)
	}
	wantErr(t, doReq(t, h, "POST", "/api/v1/ideas", bob, map[string]any{"nope": 1}), http.StatusBadRequest, "")

	// The author edits, then shares.
	if got := decodeIdea(t, doReq(t, h, "PATCH", p, bob, map[string]any{"title": "Shared thought"}), http.StatusOK); got.Title != "Shared thought" {
		t.Fatalf("patched = %+v", got)
	}
	shared := decodeIdea(t, doReq(t, h, "POST", p+"/share", bob, nil), http.StatusOK)
	if shared.Status != "intake" || shared.Task == "" {
		t.Fatalf("shared = %+v", shared)
	}
	if again := decodeIdea(t, doReq(t, h, "POST", p+"/share", bob, nil), http.StatusOK); again.Task != shared.Task {
		t.Fatalf("second share = %+v, want task %s", again, shared.Task)
	}

	// Shared: the admin reads it, the open subject lists it, a non-author edit is 403.
	decodeIdea(t, doReq(t, h, "GET", p, admin, nil), http.StatusOK)
	if ids := listIdeaIDs(t, openReq(h, "GET", "/api/v1/ideas", "")); len(ids) != 1 || ids[0] != priv.ID {
		t.Fatalf("open list = %v, want [%s]", ids, priv.ID)
	}
	if rr := doReq(t, h, "PATCH", p, admin, map[string]any{"title": "mine"}); rr.Code != http.StatusForbidden {
		t.Fatalf("admin patch shared = %d, want 403; body %s", rr.Code, rr.Body)
	}
	in := decodeIdea(t, doReq(t, h, "PATCH", p+"/intake", admin,
		map[string]any{"pre_research_authorized": true, "decision": "accept"}), http.StatusOK)
	if !in.Intake.PreResearchAuthorized || in.Intake.Decision != "accept" {
		t.Fatalf("intake = %+v", in.Intake)
	}

	// Any idea.write holder deletes a shared idea; it is gone afterwards.
	if rr := doReq(t, h, "DELETE", p, admin, map[string]any{"justification": "dup"}); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204; body %s", rr.Code, rr.Body)
	}
	wantErr(t, doReq(t, h, "GET", p, bob, nil), http.StatusNotFound, "")

	metrics := doReq(t, metricsH, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_ideas_writes_total{op="create"} 1`,
		`worklode_ideas_writes_total{op="update"} 1`,
		`worklode_ideas_writes_total{op="share"} 2`,
		`worklode_ideas_writes_total{op="intake"} 1`,
		`worklode_ideas_writes_total{op="delete"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// TestIdeaShareWithoutIntakeProject: with LODE_INTAKE_PROJECT unset the
// share act is 409 and the other routes work (WL-REQ-1799).
func TestIdeaShareWithoutIntakeProject(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	i := decodeIdea(t, doReq(t, h, "POST", "/api/v1/ideas", token,
		map[string]any{"title": "t", "type": "general"}), http.StatusCreated)
	wantErr(t, doReq(t, h, "POST", "/api/v1/ideas/"+i.ID+"/share", token, nil),
		http.StatusConflict, "intake project not configured")
}

// TestIdeasCookieWrite checks a session-cookie write needs
// X-Requested-With: lode-web (WL-REQ-1770).
func TestIdeasCookieWrite(t *testing.T) {
	_, h, iss := newOIDCServer(t, api.Config{})
	cookie := loginSession(t, h, iss, "alice")
	post := func(withHeader bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ideas",
			strings.NewReader(`{"title":"x","type":"general"}`))
		req.AddCookie(cookie)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Content-Type", "application/json")
		if withHeader {
			req.Header.Set("X-Requested-With", "lode-web")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(false); rec.Code != http.StatusForbidden {
		t.Fatalf("without X-Requested-With = %d, want 403; body %s", rec.Code, rec.Body)
	}
	if rec := post(true); rec.Code != http.StatusCreated {
		t.Fatalf("with X-Requested-With = %d, want 201; body %s", rec.Code, rec.Body)
	}
}
