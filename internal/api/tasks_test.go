package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
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
// store and back out through model.Task.
func TestCreateTaskWithConcern(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Concerned", "priority": "high", "kind": "bug", "concern": "security",
	})
	if got["concern"] != "security" {
		t.Errorf("response concern = %v, want security", got["concern"])
	}
	if got["needs_decomposition"] != false {
		t.Errorf("response needs_decomposition = %v, want false", got["needs_decomposition"])
	}
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
	// Blank title: createTask requires one, so PATCH must not be able to take
	// it away again. The stored title survives the rejected patch.
	for _, blank := range []string{"", "   "} {
		rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"title": blank})
		if rr.Code != http.StatusUnprocessableEntity {
			t.Fatalf("blank title %q status = %d, want 422; body %s", blank, rr.Code, rr.Body.String())
		}
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got = decodeMap(t, rr); got["title"] != "New title" {
		t.Fatalf("title after rejected blank patch = %v, want %q", got["title"], "New title")
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
// extension, checking both the response body and the stored row.
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
	patched := decodeMap(t, rr)
	if patched["concern"] != "usability" || patched["needs_decomposition"] != true {
		t.Errorf("patch response concern/needs_decomposition = %v/%v, want usability/true",
			patched["concern"], patched["needs_decomposition"])
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
	if cleared := decodeMap(t, rr); cleared["concern"] != "" {
		t.Errorf("patch response concern after clear = %v, want empty", cleared["concern"])
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
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Child", "priority": "low", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Parent", "priority": "low", "kind": "feature"})

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
	// Name the reason: the other checkHierarchy rules also return 422, so a
	// status-only assertion would pass without the cycle check ever running.
	if body := rr.Body.String(); !strings.Contains(body, "cycle") {
		t.Fatalf("cycle body = %s, want the cycle rule to be what rejects", body)
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

// TestCreateTaskWithSkills verifies "skills" on the create request persists
// and round-trips through the response and a subsequent GET.
func TestCreateTaskWithSkills(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Pinned", "priority": "high", "kind": "feature",
		"skills": []string{"tdd"},
	})
	skills, ok := got["skills"].([]any)
	if !ok || len(skills) != 1 || skills[0] != "tdd" {
		t.Fatalf("create response skills = %v", got["skills"])
	}

	task, err := st.GetTask(context.Background(), got["id"].(string))
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if len(task.Skills) != 1 || task.Skills[0] != "tdd" {
		t.Fatalf("stored skills = %v", task.Skills)
	}
}

// TestSetTaskSkills covers PUT /api/v1/tasks/{id}/skills: the 200 + echoed
// list, that the pins show up in a later GET (proving taskColumns/scanTask
// wiring, not just the write), that an empty list clears existing pins
// (rather than merging), and the 404 for an unknown task.
func TestSetTaskSkills(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "T", "priority": "medium", "kind": "feature",
	})
	id := created["id"].(string)
	updatedAt0, _ := created["updated_at"].(string)

	rr := doReq(t, h, "PUT", "/api/v1/tasks/"+id+"/skills", token,
		map[string]any{"skills": []string{"tdd", "debugging"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("set skills status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	skills, ok := got["skills"].([]any)
	if !ok || len(skills) != 2 || skills[0] != "tdd" || skills[1] != "debugging" {
		t.Fatalf("echoed skills = %v", got["skills"])
	}

	// The GET response reflects the write (taskColumns/scanTask wiring), and
	// updated_at advances — a consumer polling on it must not see stale pins.
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	skills, ok = got["skills"].([]any)
	if !ok || len(skills) != 2 || skills[0] != "tdd" || skills[1] != "debugging" {
		t.Fatalf("task skills after set = %v", got["skills"])
	}
	if updatedAt1, _ := got["updated_at"].(string); updatedAt1 == "" || updatedAt1 == updatedAt0 {
		t.Fatalf("updated_at after set = %q, want it to advance from %q", updatedAt1, updatedAt0)
	}

	// Blank entries are dropped and duplicates removed: the response reflects
	// what was actually persisted, not the raw request.
	rr = doReq(t, h, "PUT", "/api/v1/tasks/"+id+"/skills", token,
		map[string]any{"skills": []string{"tdd", "", "  ", "tdd", "review"}})
	if rr.Code != http.StatusOK {
		t.Fatalf("set skills with blanks/dupes status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	if skills, ok := got["skills"].([]any); !ok || len(skills) != 2 || skills[0] != "tdd" || skills[1] != "review" {
		t.Fatalf("echoed skills with blanks/dupes = %v", got["skills"])
	}

	// An empty list clears rather than merges. Sent as null: the JSON
	// round-trip through jsonb is where nil-vs-empty tends to break.
	rr = doReq(t, h, "PUT", "/api/v1/tasks/"+id+"/skills", token,
		map[string]any{"skills": nil})
	if rr.Code != http.StatusOK {
		t.Fatalf("clear skills status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	if skills, ok := got["skills"].([]any); !ok || len(skills) != 0 {
		t.Fatalf("echoed skills after clear = %v", got["skills"])
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id, token, nil)
	got = decodeMap(t, rr)
	if skills, ok := got["skills"].([]any); !ok || len(skills) != 0 {
		t.Fatalf("task skills after clear = %v", got["skills"])
	}

	rr = doReq(t, h, "PUT", "/api/v1/tasks/WL-999/skills", token, map[string]any{"skills": []string{"tdd"}})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("set skills on unknown task status = %d, want 404", rr.Code)
	}
}

// TestTaskKindsAgreeAcrossSources pins the three places the kind enum is
// spelled — the API's validKinds, the tasks.kind CHECK constraint, and
// wlc:TaskKind in ns/concept.ttl — to the same set. Each is exercised by
// creating a task of every kind: the handler rejects anything outside
// validKinds, and the insert rejects anything outside the CHECK, so a
// disagreement between those two fails here. The .ttl is read directly,
// since nothing else in the Go build knows it exists.
func TestTaskKindsAgreeAcrossSources(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	kinds := []string{"feature", "bug", "chore", "spec", "review", "spike"}
	for _, k := range kinds {
		t.Run(k, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/tasks", token,
				map[string]any{"project": "proj", "title": "t", "priority": "high", "kind": k})
			if rr.Code != http.StatusCreated {
				t.Fatalf("create kind %q status = %d, want 201; body %s", k, rr.Code, rr.Body.String())
			}
		})
	}

	ttl, err := os.ReadFile(filepath.Join("..", "..", "ns", "concept.ttl"))
	if err != nil {
		t.Fatalf("read ns/concept.ttl: %v", err)
	}
	// Every wlc:<name> declared in scheme wlc:TaskKind.
	re := regexp.MustCompile(`wlc:(\w+) a skos:Concept ; skos:inScheme wlc:TaskKind`)
	var inTTL []string
	for _, m := range re.FindAllStringSubmatch(string(ttl), -1) {
		inTTL = append(inTTL, m[1])
	}
	sort.Strings(inTTL)
	want := append([]string(nil), kinds...)
	sort.Strings(want)
	if !slices.Equal(inTTL, want) {
		t.Errorf("wlc:TaskKind = %v, want %v (ns/concept.ttl disagrees with validKinds and the CHECK constraint)", inTTL, want)
	}
}

// TestCreateTaskWithFollowUpTo checks the one-round-trip path: the edge lands in
// the same transaction as the insert, so there is no window where the follow-up
// exists without its origin.
func TestCreateTaskWithFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-1",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Edges struct {
			Out []struct{ To, Type string } `json:"out"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if len(got.Edges.Out) != 1 ||
		got.Edges.Out[0].Type != "follow_up_to" || got.Edges.Out[0].To != "WL-1" {
		t.Fatalf("out edges = %+v, want one follow_up_to WL-1", got.Edges.Out)
	}
}

// TestCreateTaskUnknownFollowUpTo checks the named 404, so it cannot be
// confused with the project lookup's.
func TestCreateTaskUnknownFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
		"follow_up_to": "WL-99",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "WL-99") {
		t.Fatalf("body %s, want it to name the missing origin", rr.Body.String())
	}
}

// TestEdgeEndpointAcceptsFollowUpTo checks the generic edge endpoint, both
// directions of the request shape and the delete.
func TestEdgeEndpointAcceptsFollowUpTo(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Origin", "priority": "medium", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose end", "priority": "medium", "kind": "chore",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token,
		map[string]any{"to": "WL-1", "type": "follow_up_to"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "DELETE", "/api/v1/tasks/WL-2/edges", token,
		map[string]any{"to": "WL-1", "type": "follow_up_to"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("remove edge status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestListTasksByRepoAndBranch covers what the local merge reporter needs
// from the list endpoint: narrow to the repo the client is sitting in, and
// read back the server-authoritative branch name so the client never has to
// render one itself.
func TestListTasksByRepoAndBranch(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	if err := st.AddRepo(context.Background(), "proj", "acme/app"); err != nil {
		t.Fatalf("AddRepo: %v", err)
	}
	if err := st.AddRepo(context.Background(), "other", "acme/other"); err != nil {
		t.Fatalf("AddRepo other: %v", err)
	}
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Fix the thing", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "other", "title": "Not mine", "priority": "high", "kind": "feature"})

	// A remote URL works as well as owner/name: the client has a remote.
	rr := doReq(t, h, "GET", "/api/v1/tasks?repo="+url.QueryEscape("git@github.com:acme/app.git"), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Tasks []struct {
			ID     string `json:"id"`
			Branch string `json:"branch"`
		} `json:"tasks"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", rr.Body.String(), err)
	}
	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %+v, want only this repo's", got.Tasks)
	}
	if got.Tasks[0].Branch != "WL-1-fix-the-thing" {
		t.Fatalf("branch = %q, want WL-1-fix-the-thing", got.Tasks[0].Branch)
	}

	// A repo that is not a repo is a 422, not a silently unfiltered list.
	rr = doReq(t, h, "GET", "/api/v1/tasks?repo=nope", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad repo status = %d, want 422", rr.Code)
	}
}

// TestListTasksUpdatedSince covers the incremental sync path the Obsidian
// mirror polls with: a watermark narrows the list, and a watermark that is
// not a timestamp is refused rather than silently ignored — an ignored one
// would look like a working incremental sync while returning everything.
func TestListTasksUpdatedSince(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Fix the thing", "priority": "high", "kind": "feature"})

	ids := func(rr *httptest.ResponseRecorder) []string {
		t.Helper()
		var got struct {
			Tasks []struct {
				ID string `json:"id"`
			} `json:"tasks"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s: %v", rr.Body.String(), err)
		}
		out := make([]string, 0, len(got.Tasks))
		for _, task := range got.Tasks {
			out = append(out, task.ID)
		}
		return out
	}

	past := url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	rr := doReq(t, h, "GET", "/api/v1/tasks?updated_since="+past, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := ids(rr); len(got) != 1 {
		t.Fatalf("updated_since=past: got %v, want the one task", got)
	}

	future := url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	rr = doReq(t, h, "GET", "/api/v1/tasks?updated_since="+future, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := ids(rr); len(got) != 0 {
		t.Fatalf("updated_since=future: got %v, want none", got)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks?updated_since=yesterday", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad updated_since status = %d, want 422", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "updated_since") {
		t.Fatalf("bad updated_since body = %s, want it to name the parameter", rr.Body.String())
	}
}

// listDetailRow is the subset of GET /api/v1/tasks?detail=true's row shape
// these tests care about. Edges is a pointer so its absence (unexpanded
// path) is distinguishable from an empty object.
type listDetailRow struct {
	ID      string `json:"id"`
	Blocked *bool  `json:"blocked"`
	Edges   *struct {
		Out []struct {
			To   string `json:"to"`
			Type string `json:"type"`
		} `json:"out"`
		In []struct {
			From string `json:"from"`
			Type string `json:"type"`
		} `json:"in"`
	} `json:"edges"`
}

// TestListTasksDetailExpansion proves the unexpanded task list is unchanged
// (no "blocked" or "edges" key on any row) while detail=true adds both per
// row, that empty edge lists serialize as [] rather than null, that row
// order is preserved, and that worklode_list_expansions_total{tasks,detail}
// increments only on the expanded request.
func TestListTasksDetailExpansion(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	if err := st.CreateActor(ctx, "alice", "human", "Alice", true); err != nil {
		t.Fatalf("create actor: %v", err)
	}
	token, err := st.CreateToken(ctx, "alice", "test token", nil)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	h, admin, err := api.NewServer(st, api.Config{})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	createProject(t, st, "proj")
	for _, title := range []string{"One", "Two", "Three", "Four"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
		})
	}
	// WL-1 blocks WL-2.
	if rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"}); rr.Code != http.StatusCreated {
		t.Fatalf("blocks edge: %d %s", rr.Code, rr.Body)
	}
	// WL-3 child_of WL-1.
	if rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"from": "WL-3", "type": "child_of"}); rr.Code != http.StatusCreated {
		t.Fatalf("child_of edge: %d %s", rr.Code, rr.Body)
	}

	list := func(qs string) []listDetailRow {
		t.Helper()
		rr := doReq(t, h, "GET", "/api/v1/tasks?project=proj"+qs, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", qs, rr.Code, rr.Body)
		}
		var got struct {
			Tasks []listDetailRow `json:"tasks"`
		}
		decodeInto(t, rr, &got)
		return got.Tasks
	}

	unexpanded := list("")
	if len(unexpanded) != 4 {
		t.Fatalf("unexpanded: got %d tasks, want 4", len(unexpanded))
	}
	for _, row := range unexpanded {
		if row.Blocked != nil {
			t.Errorf("unexpanded %s: blocked present, want absent", row.ID)
		}
		if row.Edges != nil {
			t.Errorf("unexpanded %s: edges present, want absent", row.ID)
		}
	}

	metrics := func() string {
		t.Helper()
		return doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	}
	const detailMetric = `worklode_list_expansions_total{endpoint="tasks",expansion="detail"}`
	if strings.Contains(metrics(), detailMetric) {
		t.Errorf("unexpanded request touched %s", detailMetric)
	}

	expanded := list("&detail=true")
	if len(expanded) != 4 {
		t.Fatalf("expanded: got %d tasks, want 4", len(expanded))
	}
	for i, row := range expanded {
		if row.ID != unexpanded[i].ID {
			t.Fatalf("row order differs at %d: expanded %s, unexpanded %s", i, row.ID, unexpanded[i].ID)
		}
	}

	if got := metrics(); !strings.Contains(got, detailMetric+" 1") {
		t.Errorf("expanded request did not record %s = 1:\n%s", detailMetric, got)
	}

	byID := make(map[string]listDetailRow, len(expanded))
	for _, row := range expanded {
		byID[row.ID] = row
	}

	wl2 := byID["WL-2"]
	if wl2.Blocked == nil || !*wl2.Blocked {
		t.Errorf("WL-2 blocked = %v, want true", wl2.Blocked)
	}
	if wl2.Edges == nil || len(wl2.Edges.In) != 1 || wl2.Edges.In[0].From != "WL-1" || wl2.Edges.In[0].Type != "blocks" {
		t.Errorf("WL-2 edges.in = %+v, want [{WL-1 blocks}]", wl2.Edges)
	}

	wl1 := byID["WL-1"]
	if wl1.Blocked == nil || *wl1.Blocked {
		t.Errorf("WL-1 blocked = %v, want false", wl1.Blocked)
	}
	if wl1.Edges == nil || len(wl1.Edges.Out) != 1 || wl1.Edges.Out[0].To != "WL-2" || wl1.Edges.Out[0].Type != "blocks" {
		t.Errorf("WL-1 edges.out = %+v, want [{WL-2 blocks}]", wl1.Edges)
	}
	if wl1.Edges == nil || len(wl1.Edges.In) != 1 || wl1.Edges.In[0].From != "WL-3" || wl1.Edges.In[0].Type != "child_of" {
		t.Errorf("WL-1 edges.in = %+v, want [{WL-3 child_of}]", wl1.Edges)
	}

	wl4 := byID["WL-4"]
	if wl4.Edges == nil || wl4.Edges.Out == nil || len(wl4.Edges.Out) != 0 {
		t.Errorf("WL-4 edges.out = %#v, want [] (non-nil, empty)", wl4.Edges)
	}
	if wl4.Edges == nil || wl4.Edges.In == nil || len(wl4.Edges.In) != 0 {
		t.Errorf("WL-4 edges.in = %#v, want [] (non-nil, empty)", wl4.Edges)
	}
}

// TestListTasksDetailMatchesGetTask guards against the list-detail and
// get-task edge/blocked projections drifting apart: both read the same
// store facts and must agree.
func TestListTasksDetailMatchesGetTask(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocked", "priority": "high", "kind": "feature"})
	if rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"}); rr.Code != http.StatusCreated {
		t.Fatalf("blocks edge: %d %s", rr.Code, rr.Body)
	}

	rr := doReq(t, h, "GET", "/api/v1/tasks?project=proj&detail=true", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body)
	}
	var list struct {
		Tasks []listDetailRow `json:"tasks"`
	}
	decodeInto(t, rr, &list)
	var row listDetailRow
	for _, r := range list.Tasks {
		if r.ID == "WL-2" {
			row = r
		}
	}
	if row.ID != "WL-2" {
		t.Fatalf("WL-2 missing from list: %+v", list.Tasks)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-2", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rr.Code, rr.Body)
	}
	var single listDetailRow
	decodeInto(t, rr, &single)

	if row.Blocked == nil || single.Blocked == nil || *row.Blocked != *single.Blocked {
		t.Errorf("blocked: list %v, get %v", row.Blocked, single.Blocked)
	}
	if row.Edges == nil || single.Edges == nil || !reflect.DeepEqual(*row.Edges, *single.Edges) {
		t.Errorf("edges: list %+v, get %+v", row.Edges, single.Edges)
	}
}

func TestTaskSecretsOverAPI(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secapi", "name": "Sec", "key": "SA"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}

	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secapi", "title": "creds", "priority": "medium", "kind": "chore",
		"secrets": []string{"KUBECONFIG_HZDEV", "OPENALEX_API_KEY"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string   `json:"id"`
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(created.Secrets) != 2 || created.Secrets[0] != "KUBECONFIG_HZDEV" {
		t.Fatalf("secrets = %v; want the two declared names", created.Secrets)
	}

	// The brief shows the declaration (acceptance 1).
	rec = doReq(t, h, http.MethodGet, "/api/v1/tasks/"+created.ID+"/brief", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("brief: %d %s", rec.Code, rec.Body.String())
	}
	var brief struct {
		Task struct {
			Secrets []string `json:"secrets"`
		} `json:"task"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &brief); err != nil {
		t.Fatalf("decode brief: %v", err)
	}
	if len(brief.Task.Secrets) != 2 {
		t.Fatalf("brief secrets = %v; want 2 names", brief.Task.Secrets)
	}

	// PATCH replaces the list.
	rec = doReq(t, h, http.MethodPatch, "/api/v1/tasks/"+created.ID, token,
		map[string]any{"secrets": []string{"GITHUB_TOKEN"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Secrets []string `json:"secrets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(patched.Secrets) != 1 || patched.Secrets[0] != "GITHUB_TOKEN" {
		t.Fatalf("patched secrets = %v; want [GITHUB_TOKEN]", patched.Secrets)
	}
}

func TestTaskSecretsRejectsBadNames(t *testing.T) {
	_, h, token := newTestServer(t)
	rec := doReq(t, h, http.MethodPost, "/api/v1/projects", token,
		map[string]string{"id": "secbad", "name": "SecBad", "key": "SB"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project: %d %s", rec.Code, rec.Body.String())
	}
	rec = doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": "secbad", "title": "bad", "priority": "medium", "kind": "chore",
		"secrets": []string{"op://Employee/GitHub token/credential"},
	})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad secret name: %d %s; want 422", rec.Code, rec.Body.String())
	}
}
