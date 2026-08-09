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
		t.Fatalf("dry run wrote: GET = %d", rr.Code) // relies on Task 2's GET; until it lands, assert via a real sync + unchanged=0 instead
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
