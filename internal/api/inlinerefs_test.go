package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestShowCarriesInlineRefs: the detail of a task, milestone and deliverable
// lists the inline references it makes and receives, as arrays even when
// empty (WL-REQ-1852).
func TestShowCarriesInlineRefs(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Target", "priority": "medium", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Source", "priority": "medium", "kind": "feature", "body": "Needs WL-1.\n"})
	rr := doReq(t, h, "POST", "/api/v1/projects/proj/milestones", token, model.CreateMilestoneInput{Title: "Before WL-1"})
	mile := decodeMap(t, rr)["id"].(string)
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/deliverables", token, model.CreateDeliverableInput{Name: "D", Description: "Proves WL-1."})
	del := decodeMap(t, rr)["id"].(string)

	refs := func(path, key string) []any {
		t.Helper()
		rr := doReq(t, h, "GET", path, token, nil)
		arr, ok := decodeMap(t, rr)[key].([]any)
		if !ok {
			t.Fatalf("GET %s: %s is not an array: %s", path, key, rr.Body.String())
		}
		return arr
	}
	in := refs("/api/v1/tasks/WL-1", "inline_refs_in")
	if len(in) != 3 {
		t.Fatalf("WL-1 inline_refs_in = %v, want task, milestone and deliverable", in)
	}
	first := in[0].(map[string]any)
	if first["to_ref"] != "WL-1" || first["to_title"] != "Target" {
		t.Errorf("first ref = %v", first)
	}
	if out := refs("/api/v1/tasks/WL-2", "inline_refs_out"); len(out) != 1 {
		t.Errorf("WL-2 inline_refs_out = %v", out)
	}
	for path, keys := range map[string][]string{
		"/api/v1/tasks/WL-2":          {"inline_refs_in"},
		"/api/v1/milestones/" + mile:  {"inline_refs_in", "inline_refs_out"},
		"/api/v1/deliverables/" + del: {"inline_refs_in"},
		"/api/v1/tasks/WL-1":          {"inline_refs_out"},
	} {
		for _, k := range keys {
			if got := refs(path, k); k == "inline_refs_in" && len(got) != 0 {
				t.Errorf("%s %s = %v, want none", path, k, got)
			}
		}
	}
	if out := refs("/api/v1/deliverables/"+del, "inline_refs_out"); len(out) != 1 {
		t.Errorf("deliverable inline_refs_out = %v", out)
	}
}

// TestDeriveRefsRoute: a dry run and a diff write nothing, a real run
// rewrites and reports, Limit pages the selection, and selecting nothing is
// a 400 (WL-REQ-1860).
func TestDeriveRefsRoute(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Target", "priority": "medium", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Source", "priority": "medium", "kind": "feature", "body": "Needs https://lode.example/WL-1 first.\n"})
	const path = "/api/v1/projects/proj/refs/derive"
	body := func(t *testing.T) string {
		t.Helper()
		return decodeMap(t, doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil))["body"].(string)
	}
	node := func(t *testing.T, req model.RefDeriveParams) map[string]any {
		t.Helper()
		rr := doReq(t, h, "POST", path, token, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		nodes := decodeMap(t, rr)["nodes"].([]any)
		if len(nodes) != 1 {
			t.Fatalf("nodes = %v", nodes)
		}
		return nodes[0].(map[string]any)
	}

	n := node(t, model.RefDeriveParams{Kind: "task", Refs: []string{"WL-2"}, DryRun: true})
	if n["rewrites"] != float64(1) || n["diff"] != nil {
		t.Errorf("dry run = %v", n)
	}
	n = node(t, model.RefDeriveParams{Kind: "task", Refs: []string{"WL-2"}, Diff: true})
	if d, _ := n["diff"].(string); !strings.Contains(d, "+Needs WL-1 first.") {
		t.Errorf("diff = %v", n)
	}
	if got := body(t); !strings.Contains(got, "lode.example/WL-1") {
		t.Fatalf("a dry run or diff wrote: %q", got)
	}

	n = node(t, model.RefDeriveParams{Kind: "task", Refs: []string{"WL-2"}})
	if n["ref"] == nil {
		t.Fatalf("node = %v", n)
	}
	if got := body(t); strings.Contains(got, "lode.example/WL-1") {
		t.Errorf("real run left %q", got)
	}

	page := func(t *testing.T, after string) (string, map[string]any) {
		t.Helper()
		rr := doReq(t, h, "POST", path, token, model.RefDeriveParams{Kind: "task", All: true, DryRun: true, Limit: 1, After: after})
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
		}
		m := decodeMap(t, rr)
		next, _ := m["next"].(string)
		return next, m
	}
	next, first := page(t, "")
	last, second := page(t, next)
	if next != "WL-1" || last != "" || len(first["nodes"].([]any)) != 1 ||
		second["nodes"].([]any)[0].(map[string]any)["ref"] != "WL-2" {
		t.Errorf("pages: %v then %v", first, second)
	}

	for _, bad := range []model.RefDeriveParams{{Kind: "task"}, {Kind: "idea", All: true}, {Kind: "task", All: true, Limit: -1}} {
		if rr := doReq(t, h, "POST", path, token, bad); rr.Code != http.StatusBadRequest {
			t.Errorf("%+v: status %d, want 400", bad, rr.Code)
		}
	}
}
