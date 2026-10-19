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

// TestProjectDocumentsAPI covers the register's CRUD routes (WL-REQ-1798)
// and the worklode_project_documents_writes_total counter.
func TestProjectDocumentsAPI(t *testing.T) {
	t.Parallel()
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/projects/proj/documents", token, map[string]any{
		"name": "Interview notes", "category": "meeting-notes", "type": "document", "status": "draft",
		"url": "https://example.com/notes", "date": "2026-10-01",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d, want 201; body %s", rr.Code, rr.Body)
	}
	var created model.ProjectDocument
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode %q: %v", rr.Body, err)
	}
	if created.ID == "" || created.Project != "proj" || created.Date != "2026-10-01" {
		t.Fatalf("created = %+v", created)
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/documents", token, nil)
	var list model.ProjectDocumentListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("list = %d %s (%v)", rr.Code, rr.Body, err)
	}
	if len(list.Documents) != 1 || list.Documents[0].ID != created.ID {
		t.Fatalf("list = %+v", list)
	}

	rr = doReq(t, h, "PATCH", "/api/v1/documents/"+created.ID, token, map[string]any{"status": "final"})
	var patched model.ProjectDocument
	if err := json.Unmarshal(rr.Body.Bytes(), &patched); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("patch = %d %s (%v)", rr.Code, rr.Body, err)
	}
	if patched.Status != "final" || patched.Name != "Interview notes" {
		t.Fatalf("patched = %+v", patched)
	}

	if rr := doReq(t, h, "DELETE", "/api/v1/documents/"+created.ID, token, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204; body %s", rr.Code, rr.Body)
	}

	// 404s for an unknown project and an unknown document.
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/projects/nosuch/documents"},
		{"POST", "/api/v1/projects/nosuch/documents"},
		{"PATCH", "/api/v1/documents/" + created.ID},
		{"DELETE", "/api/v1/documents/" + created.ID},
	} {
		body := map[string]any{"name": "x", "category": "sources", "type": "link", "status": "draft"}
		if rr := doReq(t, h, c.method, c.path, token, body); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404; body %s", c.method, c.path, rr.Code, rr.Body)
		}
	}

	// A category outside the list is a 400 on create and on patch.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/documents", token, map[string]any{
		"name": "x", "category": "memo", "type": "link", "status": "draft",
	})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad category create = %d, want 400; body %s", rr.Code, rr.Body)
	}
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/documents", token, map[string]any{
		"name": "y", "category": "sources", "type": "link", "status": "draft",
	})
	var second model.ProjectDocument
	_ = json.Unmarshal(rr.Body.Bytes(), &second)
	if rr := doReq(t, h, "PATCH", "/api/v1/documents/"+second.ID, token, map[string]any{"category": "memo"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad category patch = %d, want 400; body %s", rr.Code, rr.Body)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	for _, want := range []string{
		`worklode_project_documents_writes_total{op="create"} 2`,
		`worklode_project_documents_writes_total{op="update"} 1`,
		`worklode_project_documents_writes_total{op="delete"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %s", want)
		}
	}
}

// TestProjectDocumentsCookieWrite checks a session-cookie write needs
// X-Requested-With: lode-web (WL-REQ-1770).
func TestProjectDocumentsCookieWrite(t *testing.T) {
	st, h, iss := newOIDCServer(t, api.Config{})
	createProject(t, st, "proj")
	cookie := loginSession(t, h, iss, "alice")
	post := func(withHeader bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/projects/proj/documents",
			strings.NewReader(`{"name":"x","category":"sources","type":"link","status":"draft"}`))
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

// TestIdeaSourcesAPI covers an idea's sources (WL-REQ-1798): a private
// idea's sources are 404 to everyone but its author, an admin included, and
// 409 to the author; a shared idea's rows take category "sources" and are
// edited through the register's PATCH and DELETE.
func TestIdeaSourcesAPI(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	admin := seedActor(t, st, "alice", "human", "Alice", true)
	bob := seedActor(t, st, "bob", "human", "Bob", false)
	createProject(t, st, "intake")
	h, metricsH, err := api.NewServer(st, api.Config{WebOpen: true, IntakeProject: "intake"})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	idea := decodeIdea(t, doReq(t, h, "POST", "/api/v1/ideas", bob,
		map[string]any{"title": "t", "type": "general"}), http.StatusCreated)
	p := "/api/v1/ideas/" + idea.ID + "/sources"
	body := map[string]any{"name": "Census", "category": "spec", "type": "dataset", "status": "draft", "source": "SSB"}

	for _, method := range []string{"GET", "POST"} {
		wantErr(t, doReq(t, h, method, p, admin, body), http.StatusNotFound, "")
		wantErr(t, doReq(t, h, method, p, bob, body), http.StatusConflict, "idea not shared")
		wantErr(t, doReq(t, h, method, "/api/v1/ideas/idea_nope/sources", bob, body), http.StatusNotFound, "")
	}

	decodeIdea(t, doReq(t, h, "POST", "/api/v1/ideas/"+idea.ID+"/share", bob, nil), http.StatusOK)

	var list model.ProjectDocumentListResponse
	decodeBody(t, doReq(t, h, "GET", p, admin, nil), http.StatusOK, &list)
	if list.Documents == nil || len(list.Documents) != 0 {
		t.Fatalf("empty list = %#v", list)
	}
	var d model.ProjectDocument
	decodeBody(t, doReq(t, h, "POST", p, admin, body), http.StatusCreated, &d)
	if d.ID == "" || d.Idea != idea.ID || d.Project != "" || d.Category != "sources" || d.Source != "SSB" {
		t.Fatalf("created = %+v", d)
	}
	decodeBody(t, doReq(t, h, "GET", p, bob, nil), http.StatusOK, &list)
	if len(list.Documents) != 1 || list.Documents[0].ID != d.ID {
		t.Fatalf("list = %+v", list)
	}
	decodeBody(t, doReq(t, h, "GET", "/api/v1/projects/intake/documents", admin, nil), http.StatusOK, &list)
	if len(list.Documents) != 0 {
		t.Fatalf("intake project's register shows the idea's source: %+v", list)
	}
	wantErr(t, doReq(t, h, "POST", p, bob, map[string]any{"name": "x", "type": "nope", "status": "draft"}),
		http.StatusBadRequest, "")

	var patched model.ProjectDocument
	decodeBody(t, doReq(t, h, "PATCH", "/api/v1/documents/"+d.ID, bob, map[string]any{"status": "final"}), http.StatusOK, &patched)
	if patched.Status != "final" || patched.Idea != idea.ID {
		t.Fatalf("patched = %+v", patched)
	}
	if rr := doReq(t, h, "DELETE", "/api/v1/documents/"+d.ID, bob, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204; body %s", rr.Code, rr.Body)
	}

	// A tombstoned idea's rows are gone for everyone.
	decodeBody(t, doReq(t, h, "POST", p, bob, body), http.StatusCreated, &d)
	if rr := doReq(t, h, "DELETE", "/api/v1/ideas/"+idea.ID, admin, nil); rr.Code != http.StatusNoContent {
		t.Fatalf("delete idea = %d; body %s", rr.Code, rr.Body)
	}
	for _, who := range []string{admin, bob} {
		wantErr(t, doReq(t, h, "PATCH", "/api/v1/documents/"+d.ID, who, map[string]any{"status": "final"}), http.StatusNotFound, "")
		wantErr(t, doReq(t, h, "DELETE", "/api/v1/documents/"+d.ID, who, nil), http.StatusNotFound, "")
		wantErr(t, doReq(t, h, "GET", p, who, nil), http.StatusNotFound, "")
	}

	metrics := doReq(t, metricsH, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_project_documents_writes_total{op="create"} 2`) {
		t.Errorf("metrics missing two creates")
	}
}
