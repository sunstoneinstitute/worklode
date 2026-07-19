package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

func createProject(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.CreateProject(context.Background(), id, id); err != nil {
		t.Fatalf("create project %s: %v", id, err)
	}
}

func createTaskViaAPI(t *testing.T, h http.Handler, token string, body map[string]any) map[string]any {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/tasks", token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create task status = %d, body %s", rr.Code, rr.Body.String())
	}
	return decodeMap(t, rr)
}

func TestCreateTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project":  "proj",
		"title":    "First task",
		"body":     "do the thing",
		"priority": "high",
		"kind":     "feature",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	want := map[string]any{
		"id": "WT-1", "project": "proj", "title": "First task",
		"body": "do the thing", "priority": "high", "kind": "feature",
		"state": "ready", "created_by": "alice",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	for _, k := range []string{"created_at", "updated_at"} {
		if s, _ := got[k].(string); s == "" {
			t.Errorf("%s missing or empty", k)
		}
	}
}

func TestCreateTaskDraft(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Draft", "priority": "low", "kind": "chore", "draft": true,
	})
	if got["state"] != "draft" {
		t.Fatalf("state = %v, want draft", got["state"])
	}
}

func TestCreateTaskValidation(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown project", map[string]any{"project": "nope", "title": "t", "priority": "high", "kind": "bug"}, 404},
		{"bad priority", map[string]any{"project": "proj", "title": "t", "priority": "urgent", "kind": "bug"}, 422},
		{"bad kind", map[string]any{"project": "proj", "title": "t", "priority": "high", "kind": "task"}, 422},
		{"missing title", map[string]any{"project": "proj", "priority": "high", "kind": "bug"}, 422},
		{"unknown field", map[string]any{"project": "proj", "title": "t", "priority": "high", "kind": "bug", "bogus": 1}, 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/tasks", token, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestGetTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "One", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WT-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["id"] != "WT-1" || got["title"] != "One" {
		t.Fatalf("unexpected task: %v", got)
	}
	if got["blocked"] != false {
		t.Fatalf("blocked = %v, want false", got["blocked"])
	}
	edges, ok := got["edges"].(map[string]any)
	if !ok {
		t.Fatalf("edges missing: %v", got)
	}
	// Empty edge lists must be JSON arrays, not null.
	if _, ok := edges["out"].([]any); !ok {
		t.Fatalf("edges.out = %v, want []", edges["out"])
	}
	if _, ok := edges["in"].([]any); !ok {
		t.Fatalf("edges.in = %v, want []", edges["in"])
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-99", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", rr.Code)
	}
}

func TestListTasksFilters(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "proj2")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "A", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "B", "priority": "low", "kind": "bug", "draft": true})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj2", "title": "C", "priority": "medium", "kind": "chore"})

	list := func(path string) []any {
		t.Helper()
		rr := doReq(t, h, "GET", path, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, body %s", path, rr.Code, rr.Body.String())
		}
		tasks, ok := decodeMap(t, rr)["tasks"].([]any)
		if !ok {
			t.Fatalf("GET %s: tasks not an array: %s", path, rr.Body.String())
		}
		return tasks
	}

	cases := []struct {
		path string
		want int
	}{
		{"/api/v1/tasks", 3},
		{"/api/v1/tasks?project=proj", 2},
		{"/api/v1/tasks?state=draft", 1},
		{"/api/v1/tasks?state=draft,ready", 3},
		{"/api/v1/tasks?state=draft&state=ready", 3},
		{"/api/v1/tasks?priority=high", 1},
		{"/api/v1/tasks?project=proj&priority=low", 1},
		{"/api/v1/tasks?project=proj2&state=draft", 0},
	}
	for _, tc := range cases {
		if got := len(list(tc.path)); got != tc.want {
			t.Errorf("%s: %d tasks, want %d", tc.path, got, tc.want)
		}
	}
}

func TestPatchTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Orig", "body": "orig body", "priority": "high", "kind": "feature",
	})

	// Patch title only: other fields unchanged.
	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WT-1", token, map[string]any{"title": "New title"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["title"] != "New title" || got["body"] != "orig body" || got["priority"] != "high" {
		t.Fatalf("after title patch: %v", got)
	}

	// Patch body + priority: title stays.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WT-1", token, map[string]any{"body": "new body", "priority": "low"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	if got["title"] != "New title" || got["body"] != "new body" || got["priority"] != "low" {
		t.Fatalf("after body/priority patch: %v", got)
	}

	// Invalid priority.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WT-1", token, map[string]any{"priority": "bogus"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad priority status = %d, want 422", rr.Code)
	}
	// Empty patch.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WT-1", token, map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch status = %d, want 422", rr.Code)
	}
	// Unknown task.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WT-99", token, map[string]any{"title": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", rr.Code)
	}
	// Unknown field.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WT-1", token, map[string]any{"state": "done"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", rr.Code)
	}
}

func TestEdges(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocked", "priority": "high", "kind": "feature"})

	// WT-1 blocks WT-2, expressed via "to" on WT-1.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WT-1/edges", token, map[string]any{"to": "WT-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Adding the same edge again conflicts.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WT-1/edges", token, map[string]any{"to": "WT-2", "type": "blocks"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate edge status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}

	// Blocked task shows blocked:true and the incoming edge.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-2", token, nil)
	got := decodeMap(t, rr)
	if got["blocked"] != true {
		t.Fatalf("WT-2 blocked = %v, want true", got["blocked"])
	}
	in := got["edges"].(map[string]any)["in"].([]any)
	if len(in) != 1 {
		t.Fatalf("WT-2 edges.in = %v, want 1 edge", in)
	}
	e := in[0].(map[string]any)
	if e["from"] != "WT-1" || e["type"] != "blocks" {
		t.Fatalf("WT-2 in edge = %v", e)
	}

	// Blocker shows the outgoing edge and is not itself blocked.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-1", token, nil)
	got = decodeMap(t, rr)
	if got["blocked"] != false {
		t.Fatalf("WT-1 blocked = %v, want false", got["blocked"])
	}
	out := got["edges"].(map[string]any)["out"].([]any)
	if len(out) != 1 {
		t.Fatalf("WT-1 edges.out = %v, want 1 edge", out)
	}
	e = out[0].(map[string]any)
	if e["to"] != "WT-2" || e["type"] != "blocks" {
		t.Fatalf("WT-1 out edge = %v", e)
	}

	// Remove the edge; WT-2 unblocks.
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WT-1/edges", token, map[string]any{"to": "WT-2", "type": "blocks"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-2", token, nil)
	if got := decodeMap(t, rr); got["blocked"] != false {
		t.Fatalf("WT-2 blocked after removal = %v, want false", got["blocked"])
	}
	// Removing again: gone.
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WT-1/edges", token, map[string]any{"to": "WT-2", "type": "blocks"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("second remove status = %d, want 404", rr.Code)
	}
}

func TestEdgesFromDirection(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Child", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Epic", "priority": "low", "kind": "feature"})

	// "from" on WT-2 means WT-1 -> WT-2 (WT-1 child_of WT-2).
	rr := doReq(t, h, "POST", "/api/v1/tasks/WT-2/edges", token, map[string]any{"from": "WT-1", "type": "child_of"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-1", token, nil)
	out := decodeMap(t, rr)["edges"].(map[string]any)["out"].([]any)
	if len(out) != 1 || out[0].(map[string]any)["to"] != "WT-2" {
		t.Fatalf("WT-1 edges.out = %v, want child_of edge to WT-2", out)
	}

	// child_of cycle: WT-2 child_of WT-1 would close the loop.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WT-2/edges", token, map[string]any{"to": "WT-1", "type": "child_of"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("cycle status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

func TestEdgeValidation(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "A", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "B", "priority": "low", "kind": "chore"})

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"both to and from", map[string]any{"to": "WT-2", "from": "WT-2", "type": "blocks"}, 422},
		{"neither to nor from", map[string]any{"type": "blocks"}, 422},
		{"self edge", map[string]any{"to": "WT-1", "type": "blocks"}, 422},
		{"bad type", map[string]any{"to": "WT-2", "type": "depends"}, 422},
		{"unknown target", map[string]any{"to": "WT-99", "type": "blocks"}, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/tasks/WT-1/edges", token, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}
