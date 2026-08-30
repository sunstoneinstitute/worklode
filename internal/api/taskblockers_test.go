package api_test

import (
	"net/http"
	"testing"
)

// TestBlockerRoutes covers both forms of the blocker read: the per-task tree
// and the scoped forest, plus the 404 an unknown task must produce rather
// than an empty tree that reads as "nothing blocks it".
func TestBlockerRoutes(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	for _, title := range []string{"Root", "Middle", "Bottom"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "high", "kind": "feature",
		})
	}
	// WL-3 blocks WL-2 blocks WL-1.
	for _, e := range [][2]string{{"WL-2", "WL-1"}, {"WL-3", "WL-2"}} {
		rr := doReq(t, h, "POST", "/api/v1/tasks/"+e[0]+"/edges", token,
			map[string]any{"to": e[1], "type": "blocks"})
		if rr.Code != http.StatusCreated {
			t.Fatalf("add edge %s->%s status = %d, body %s", e[0], e[1], rr.Code, rr.Body.String())
		}
	}

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/blockers", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("task blockers status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	blockers, _ := got["blockers"].([]any)
	if len(blockers) != 2 {
		t.Fatalf("blockers = %v, want WL-2 at depth 1 and WL-3 at depth 2", got["blockers"])
	}

	rr = doReq(t, h, "GET", "/api/v1/blockers?project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("forest status = %d, body %s", rr.Code, rr.Body.String())
	}
	trees, _ := decodeMap(t, rr)["trees"].([]any)
	if len(trees) != 1 {
		t.Fatalf("trees = %v, want just WL-1 (WL-2 is already inside its tree)", trees)
	}

	// The forest with no project spans every project, and is the same route.
	if rr = doReq(t, h, "GET", "/api/v1/blockers", token, nil); rr.Code != http.StatusOK {
		t.Fatalf("unscoped forest status = %d, body %s", rr.Code, rr.Body.String())
	}

	if rr = doReq(t, h, "GET", "/api/v1/tasks/WL-999/blockers", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404, body %s", rr.Code, rr.Body.String())
	}
}
