package api_test

import (
	"net/http"
	"testing"
)

// createEpic creates a container task through the API and returns its id.
func createEpic(t *testing.T, h http.Handler, token, project, title string) string {
	t.Helper()
	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": project, "title": title, "priority": "medium", "kind": "epic",
	})
	return got["id"].(string)
}

func TestCreateTaskWithEpicKind(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Container", "priority": "medium", "kind": "epic",
	})
	if got["kind"] != "epic" {
		t.Fatalf("kind = %v, want epic", got["kind"])
	}
}

func TestCreateTaskWithParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")

	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d", rr.Code)
	}
	detail := decodeMap(t, rr)
	hier := detail["hierarchy"].(map[string]any)
	parent := hier["parent"].(map[string]any)
	if parent["id"] != epic {
		t.Fatalf("parent = %v, want %s", parent["id"], epic)
	}
	if parent["title"] != "Container" {
		t.Fatalf("parent title = %v, want Container", parent["title"])
	}
}

// TestCreateTaskWithUnknownParentCreatesNothing checks the single-transaction
// promise: a rejected parent must not leave an unparented child behind.
func TestCreateTaskWithUnknownParentCreatesNothing(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Orphan", "priority": "medium", "kind": "feature",
		"parent": "WL-999",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	list := doReq(t, h, "GET", "/api/v1/tasks?project=proj", token, nil)
	tasks := decodeMap(t, list)["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (the create must have rolled back)", len(tasks))
	}
}

func TestTaskDetailProgress(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	for _, title := range []string{"A", "B"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": epic,
		})
	}

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+epic, token, nil)
	hier := decodeMap(t, rr)["hierarchy"].(map[string]any)
	progress := hier["progress"].(map[string]any)
	if progress["total"].(float64) != 2 || progress["closed"].(float64) != 0 {
		t.Fatalf("progress = %v, want 0/2", progress)
	}
	if hier["parent"] != nil {
		t.Fatalf("parent = %v, want null for a root epic", hier["parent"])
	}
}

// TestCreateTaskWithNonEpicParentIsUnprocessable checks a rule only
// reachable through create's "parent" field: a fresh task can never already
// have a parent, so the edges-endpoint conflict case this replaced was pure
// duplication of internal/store/hierarchy_test.go and tasks_test.go's
// TestEdges/TestEdgeValidation. "parent must be an epic" has no such
// coverage on the create path.
func TestCreateTaskWithNonEpicParentIsUnprocessable(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	notEpic := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Plain", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": notEpic["id"],
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	list := doReq(t, h, "GET", "/api/v1/tasks?project=proj", token, nil)
	tasks := decodeMap(t, list)["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1 (only Plain; the rejected create must have rolled back)", len(tasks))
	}
}

// TestCrossProjectParentIsUnprocessable drives create's "parent" field, the
// only path that reaches this rule: the edges endpoint (POST
// /tasks/{id}/edges) predates spec 004 and is already covered by
// internal/store/hierarchy_test.go and tasks_test.go's TestEdges/
// TestEdgeValidation. This also proves the transaction rolls back on a 422,
// not just on the 404 TestCreateTaskWithUnknownParentCreatesNothing covers.
func TestCrossProjectParentIsUnprocessable(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createProject(t, st, "other")
	epic := createEpic(t, h, token, "proj", "Container")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "other", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}
	list := doReq(t, h, "GET", "/api/v1/tasks?project=other", token, nil)
	tasks := decodeMap(t, list)["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("tasks = %d, want 0 (the create must have rolled back)", len(tasks))
	}
}

func TestListTasksByParentAndKind(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Container")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?parent="+epic, token, nil)
	tasks := decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != child["id"] {
		t.Fatalf("children = %v, want [%v]", tasks, child["id"])
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks?kind=epic", token, nil)
	tasks = decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != epic {
		t.Fatalf("epics = %v, want [%s]", tasks, epic)
	}
}
