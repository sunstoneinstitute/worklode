package api_test

import (
	"net/http"
	"testing"
)

func TestDecomposeEndpoint(t *testing.T) {
	t.Parallel()
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
	// The response names the parent, and 029 §2 / 004 §6.10 leave its kind
	// untouched — the child_of edges are what make it a container.
	gotParent := got["parent"].(map[string]any)
	if gotParent["kind"] != "feature" || gotParent["id"] != id {
		t.Fatalf("parent = %v, want %s split in place with kind feature", gotParent, id)
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
	t.Parallel()
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
	t.Parallel()
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

// TestDecomposeEndpointRejectsTaskWithChildren covers store.Decompose's
// rejection of re-splitting a container: a task that already has children is
// rejected with 422 — add more children with the edges endpoint instead.
func TestDecomposeEndpointRejectsTaskWithChildren(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Already split", "priority": "high", "kind": "feature",
	})
	id := parent["id"].(string)

	first := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"A"}})
	if first.Code != http.StatusCreated {
		t.Fatalf("first decompose status = %d, body %s", first.Code, first.Body.String())
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decompose", token,
		map[string]any{"into": []string{"B"}})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body %s", rr.Code, rr.Body.String())
	}
}
