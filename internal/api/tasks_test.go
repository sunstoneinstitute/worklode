package api_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// createProject registers a project for tests. Most tests only create one
// project and rely on tasks getting "WL-<n>" ids (a holdover from the old
// global WL- counter, baked into many literal assertions across this
// package's tests); "proj" and "proj1" are the conventional ids for that
// project, so they always get key "WL". Any other id gets a key derived from
// itself, distinct from "WL", for tests that create a second project.
func createProject(t *testing.T, st *store.Store, id string) {
	t.Helper()
	key := strings.ToUpper(id)
	if id == "proj" || id == "proj1" {
		key = "WL"
	}
	if err := st.CreateProject(context.Background(), id, id, key); err != nil {
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
		"id": "WL-1", "project": "proj", "title": "First task",
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

// TestCreateTaskWithConcern verifies the concern field round-trips into the
// store (taskJSON deliberately does not expose concern, see patchTask).
func TestCreateTaskWithConcern(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Concerned", "priority": "high", "kind": "bug", "concern": "security",
	})
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Concern != "security" {
		t.Fatalf("stored concern = %q, want security", task.Concern)
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Bad concern", "priority": "high", "kind": "bug", "concern": "nonsense",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid concern status = %d, want 422; body %s", rr.Code, rr.Body.String())
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

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["id"] != "WL-1" || got["title"] != "One" {
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

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-99", token, nil)
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
	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"title": "New title"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["title"] != "New title" || got["body"] != "orig body" || got["priority"] != "high" {
		t.Fatalf("after title patch: %v", got)
	}

	// Patch body + priority: title stays.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"body": "new body", "priority": "low"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	if got["title"] != "New title" || got["body"] != "new body" || got["priority"] != "low" {
		t.Fatalf("after body/priority patch: %v", got)
	}

	// Invalid priority.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"priority": "bogus"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad priority status = %d, want 422", rr.Code)
	}
	// Empty patch.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty patch status = %d, want 422", rr.Code)
	}
	// Unknown task.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-99", token, map[string]any{"title": "x"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task status = %d, want 404", rr.Code)
	}
	// Unknown field.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"kind": "bug"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", rr.Code)
	}
}

// TestPatchTaskConcern covers the concern/needs_decomposition PATCH
// extension. taskJSON does not expose either field, so the effect is
// verified by reading the store directly.
func TestPatchTaskConcern(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Concern target", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"concern": "nonsense"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid concern status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{
		"concern": "usability", "needs_decomposition": true,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("valid patch status = %d, body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Concern != "usability" {
		t.Fatalf("stored concern = %q, want usability", task.Concern)
	}
	if !task.NeedsDecomposition {
		t.Fatalf("stored needs_decomposition = false, want true")
	}

	// Clearing concern with "" or "none".
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"concern": "none"})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear concern status = %d, body %s", rr.Code, rr.Body.String())
	}
	task, err = st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Concern != "" {
		t.Fatalf("stored concern after clear = %q, want empty", task.Concern)
	}
}

func TestPatchTaskState(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Draft task", "priority": "medium", "kind": "feature", "draft": true,
	})

	// draft -> ready.
	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "ready"})
	if rr.Code != http.StatusOK {
		t.Fatalf("ready patch status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["state"]; got != "ready" {
		t.Fatalf("state after ready patch = %v, want ready", got)
	}

	// ready -> ready is not a legal transition (task is no longer draft).
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "ready"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ready->ready status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// in_review -> in_progress (rework: reviewer requested changes).
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WL-1")
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "in_progress"})
	if rr.Code != http.StatusOK {
		t.Fatalf("rework patch status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["state"]; got != "in_progress" {
		t.Fatalf("state after rework patch = %v, want in_progress", got)
	}

	// States with dedicated endpoints are rejected with guidance.
	for _, state := range []string{"merged", "abandoned", "draft"} {
		rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": state})
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("state=%s status = %d, want 422; body %s", state, rr.Code, rr.Body.String())
		}
		if msg, _ := decodeMap(t, rr)["error"].(string); !strings.Contains(msg, "claim, release, done, or abandon") {
			t.Fatalf("state=%s error = %q, want guidance to the dedicated endpoints", state, msg)
		}
	}
}

func TestPatchTaskStateCombinesWithFields(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Draft", "priority": "low", "kind": "chore", "draft": true,
	})

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{
		"state": "ready", "title": "Published title", "priority": "high",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("combined patch status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["state"] != "ready" || got["title"] != "Published title" || got["priority"] != "high" {
		t.Fatalf("after combined patch: %v", got)
	}

	// An illegal transition rolls back the whole patch, field updates included.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{
		"state": "ready", "title": "Should not stick",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("illegal combined patch status = %d, want 422", rr.Code)
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got := decodeMap(t, rr); got["title"] != "Published title" {
		t.Fatalf("title after rolled-back patch = %v, want Published title", got["title"])
	}
}

func TestEdges(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocked", "priority": "high", "kind": "feature"})

	// WL-1 blocks WL-2, expressed via "to" on WL-1.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Adding the same edge again conflicts.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate edge status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}

	// Blocked task shows blocked:true and the incoming edge.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil)
	got := decodeMap(t, rr)
	if got["blocked"] != true {
		t.Fatalf("WL-2 blocked = %v, want true", got["blocked"])
	}
	in := got["edges"].(map[string]any)["in"].([]any)
	if len(in) != 1 {
		t.Fatalf("WL-2 edges.in = %v, want 1 edge", in)
	}
	e := in[0].(map[string]any)
	if e["from"] != "WL-1" || e["type"] != "blocks" {
		t.Fatalf("WL-2 in edge = %v", e)
	}

	// Blocker shows the outgoing edge and is not itself blocked.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	got = decodeMap(t, rr)
	if got["blocked"] != false {
		t.Fatalf("WL-1 blocked = %v, want false", got["blocked"])
	}
	out := got["edges"].(map[string]any)["out"].([]any)
	if len(out) != 1 {
		t.Fatalf("WL-1 edges.out = %v, want 1 edge", out)
	}
	e = out[0].(map[string]any)
	if e["to"] != "WL-2" || e["type"] != "blocks" {
		t.Fatalf("WL-1 out edge = %v", e)
	}

	// Remove the edge; WL-2 unblocks.
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil)
	if got := decodeMap(t, rr); got["blocked"] != false {
		t.Fatalf("WL-2 blocked after removal = %v, want false", got["blocked"])
	}
	// Removing again: gone.
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("second remove status = %d, want 404", rr.Code)
	}
}

func TestEdgesFromDirection(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Child", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Epic", "priority": "low", "kind": "feature"})

	// "from" on WL-2 means WL-1 -> WL-2 (WL-1 child_of WL-2).
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"from": "WL-1", "type": "child_of"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	out := decodeMap(t, rr)["edges"].(map[string]any)["out"].([]any)
	if len(out) != 1 || out[0].(map[string]any)["to"] != "WL-2" {
		t.Fatalf("WL-1 edges.out = %v, want child_of edge to WL-2", out)
	}

	// child_of cycle: WL-2 child_of WL-1 would close the loop.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"to": "WL-1", "type": "child_of"})
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
		{"both to and from", map[string]any{"to": "WL-2", "from": "WL-2", "type": "blocks"}, 422},
		{"neither to nor from", map[string]any{"type": "blocks"}, 422},
		{"self edge", map[string]any{"to": "WL-1", "type": "blocks"}, 422},
		{"bad type", map[string]any{"to": "WL-2", "type": "depends"}, 422},
		{"unknown target", map[string]any{"to": "WL-99", "type": "blocks"}, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, tc.body)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}
