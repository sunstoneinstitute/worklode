package api_test

import (
	"net/http"
	"testing"
)

// TestProjectRallyRoute covers the open rally plus its transitive open
// blockers, and the 404 a project with no open rally must produce rather
// than an empty tree that reads as "nothing to steer".
func TestProjectRallyRoute(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "GET", "/api/v1/projects/proj/rally", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("no rally: status = %d, want 404, body %s", rr.Code, rr.Body.String())
	}

	blocker := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "the actual work", "priority": "high", "kind": "feature",
	})
	rally := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "finish the cockpit", "priority": "high", "kind": "rally",
	})
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+blocker["id"].(string)+"/edges", token,
		map[string]any{"to": rally["id"], "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocks edge: status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/projects/proj/rally", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rally: status = %d, want 200, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	task, _ := got["task"].(map[string]any)
	if task["id"] != rally["id"] {
		t.Fatalf("task.id = %v, want %v", task["id"], rally["id"])
	}
	blockersMap, _ := got["blockers"].(map[string]any)
	blockerRows, _ := blockersMap["blockers"].([]any)
	if len(blockerRows) != 1 {
		t.Fatalf("blockers = %v, want one row for %v", blockersMap["blockers"], blocker["id"])
	}
	row := blockerRows[0].(map[string]any)
	if row["id"] != blocker["id"] {
		t.Fatalf("blocker id = %v, want %v", row["id"], blocker["id"])
	}
}

// TestProjectRallyRequiresAuth mirrors TestProjectCockpitRequiresAuth: a
// missing bearer token must 401, not fall through to an unmatched route or
// an anonymous read.
func TestProjectRallyRequiresAuth(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/projects/proj/rally", "", nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}
