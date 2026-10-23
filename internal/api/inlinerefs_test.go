package api_test

import (
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
