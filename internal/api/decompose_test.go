package api_test

import (
	"net/http"
	"testing"
)

func TestDecomposeEndpoint(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})
	id := parent["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"Phase one", "Phase two"}})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	epic := got["epic"].(map[string]any)
	if epic["kind"] != "epic" || epic["id"] != id {
		t.Fatalf("epic = %v, want %s converted in place", epic, id)
	}
	children := got["children"].([]any)
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	for _, c := range children {
		if c.(map[string]any)["state"] != "draft" {
			t.Fatalf("child %v, want state draft", c)
		}
	}
}

func TestDecomposeEndpointRejectsEmptyList(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+parent["id"].(string)+"/decompose", token,
		map[string]any{"into": []string{}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
}

func TestDecomposeEndpointRejectsLeasedTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Too big", "priority": "high", "kind": "feature",
	})
	id := parent["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "wt-1"}); rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"A"}})
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rr.Code)
	}
}

// TestDecomposeEndpointRejectsExistingEpic covers store.Decompose's rejection
// of re-splitting a container: an epic already has its children, so it is
// rejected with 422 rather than silently taken as a new "feature" child kind.
func TestDecomposeEndpointRejectsExistingEpic(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Already an epic", "priority": "high", "kind": "epic",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+parent["id"].(string)+"/decompose", token,
		map[string]any{"into": []string{"A"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}
