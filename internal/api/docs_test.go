package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func specDocPayload() map[string]any {
	return map[string]any{
		"kind": "spec", "ordinal": "34", "status": "accepted",
		"title":       "Spec 034 — Design-doc sync",
		"body":        "---\nstatus: accepted\n---\n# Spec 034 — Design-doc sync\n\n## 1. Scope {#sec-1}\n\nBody.\n",
		"frontmatter": map[string]any{"status": "accepted"},
		"sections": []map[string]any{
			{"anchor": "sec-1", "heading": "Scope", "depth": 2, "position": 0},
		},
		"edges": []map[string]any{
			{"src_anchor": "sec-1", "rel": "amends",
				"target": "025-documents-in-the-backbone.md", "target_anchor": "sec-2"},
		},
	}
}

func syncBody(dryRun bool, docs ...map[string]any) map[string]any {
	return map[string]any{
		"project": "wl", "source_branch": "main", "dirty": false,
		"force": false, "dry_run": dryRun, "docs": docs,
	}
}

func createDocProject(t *testing.T, h http.Handler, token string) {
	t.Helper()
	rr := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]any{"id": "wl", "name": "Worklode", "key": "WL"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rr.Code, rr.Body)
	}
}

func TestDocsSync(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload()))
	if rr.Code != http.StatusOK {
		t.Fatalf("sync: %d %s", rr.Code, rr.Body)
	}
	var resp struct {
		DryRun    bool `json:"dry_run"`
		Added     int  `json:"added"`
		Unchanged int  `json:"unchanged"`
		Results   []struct{ ID, Kind, Outcome string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Added != 1 || len(resp.Results) != 1 || resp.Results[0].ID != "WL-SPEC-34" ||
		resp.Results[0].Outcome != "added" {
		t.Fatalf("resp = %+v", resp)
	}

	// Idempotent: second run reports unchanged (034 §12.2).
	rr = doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload()))
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Unchanged != 1 || resp.Added != 0 {
		t.Fatalf("second sync = %+v, want one unchanged", resp)
	}
}

func TestDocsSyncDryRunWritesNothing(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(true, specDocPayload()))
	if rr.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", rr.Code, rr.Body)
	}
	var resp struct {
		DryRun bool `json:"dry_run"`
		Added  int  `json:"added"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.DryRun || resp.Added != 1 {
		t.Fatalf("dry-run resp = %+v", resp)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-34", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("dry run wrote: GET = %d", rr.Code)
	}
}

func TestDocsSyncErrors(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)

	bad := specDocPayload()
	bad["kind"] = "memo"
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, bad)); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad kind: %d, want 422", rr.Code)
	}
	body := syncBody(false, specDocPayload())
	body["project"] = "nope"
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, body); rr.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/docs/sync", "", syncBody(false)); rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rr.Code)
	}
}

func TestDocsListAndGet(t *testing.T) {
	_, h, token := newTestServer(t)
	createDocProject(t, h, token)
	plan := map[string]any{
		"kind": "plan", "ordinal": "34-1", "status": "draft", "title": "Part 1",
		"body":        "---\nstatus: draft\n---\n# Part 1\n",
		"frontmatter": map[string]any{"status": "draft"},
		"edges": []map[string]any{
			{"rel": "implements", "target": "docs/specs/034-design-doc-sync.md"},
		},
	}
	doReq(t, h, http.MethodPost, "/api/v1/docs/sync", token, syncBody(false, specDocPayload(), plan))

	rr := doReq(t, h, http.MethodGet, "/api/v1/docs?project=wl", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	var list struct {
		Docs []struct {
			ID, Kind, Status, Title, Body string
		} `json:"docs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Docs) != 2 {
		t.Fatalf("list = %+v, want 2 docs", list.Docs)
	}
	for _, d := range list.Docs {
		if d.Body != "" {
			t.Errorf("%s: list row carries a body", d.ID)
		}
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/docs?project=wl&kind=plan", token, nil)
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Docs) != 1 || list.Docs[0].ID != "WL-PLAN-34-1" {
		t.Fatalf("kind filter = %+v", list.Docs)
	}

	rr = doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-34", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}
	var got struct {
		ID       string `json:"id"`
		Body     string `json:"body"`
		Sections []struct{ Anchor string }
		Edges    []struct{ Rel string }
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "WL-SPEC-34" || got.Body == "" || len(got.Sections) != 1 || len(got.Edges) != 1 {
		t.Fatalf("get = %+v", got)
	}

	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs/WL-SPEC-999", token, nil); rr.Code != http.StatusNotFound {
		t.Errorf("missing doc: %d, want 404", rr.Code)
	}
	if rr := doReq(t, h, http.MethodGet, "/api/v1/docs?kind=memo", token, nil); rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad kind filter: %d, want 422", rr.Code)
	}
}
