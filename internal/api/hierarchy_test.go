package api_test

import (
	"net/http"
	"testing"
)

// createContainer creates a task that will take children, through the API,
// and returns its id. Since 029 §2 there is no container kind to declare.
func createContainer(t *testing.T, h http.Handler, token, project, title string) string {
	t.Helper()
	got := createTaskViaAPI(t, h, token, map[string]any{
		"project": project, "title": title, "priority": "medium", "kind": "feature",
	})
	return got["id"].(string)
}

// TestCreateTaskRejectsContainerKind pins that no kind declares container-ness
// at the HTTP edge (025 §10): container-ness is inferred from child_of edges, so
// validKinds admits nothing structural and the create is a 422.
func TestCreateTaskRejectsContainerKind(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Container", "priority": "medium", "kind": "container",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (no kind declares a container)", rr.Code)
	}
}

func TestCreateTaskWithParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	container := createContainer(t, h, token, "proj", "Container")

	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": container,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string), token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d", rr.Code)
	}
	detail := decodeMap(t, rr)
	hier := detail["hierarchy"].(map[string]any)
	parent := hier["parent"].(map[string]any)
	if parent["id"] != container {
		t.Fatalf("parent = %v, want %s", parent["id"], container)
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
	container := createContainer(t, h, token, "proj", "Container")
	for _, title := range []string{"A", "B"} {
		createTaskViaAPI(t, h, token, map[string]any{
			"project": "proj", "title": title, "priority": "medium", "kind": "feature",
			"parent": container,
		})
	}

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+container, token, nil)
	hier := decodeMap(t, rr)["hierarchy"].(map[string]any)
	progress := hier["progress"].(map[string]any)
	if progress["total"].(float64) != 2 || progress["closed"].(float64) != 0 {
		t.Fatalf("progress = %v, want 0/2", progress)
	}
	if hier["parent"] != nil {
		t.Fatalf("parent = %v, want null for a root task", hier["parent"])
	}
}

// TestCreateTaskUnderOrdinaryParent pins 029 §2 on the create path: any
// ordinary task may be a parent, so what used to be a 422 ("parent must be an
// container") is now the supported way to file a child.
func TestCreateTaskUnderOrdinaryParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	plain := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Plain", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": plain["id"],
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	child := decodeMap(t, rr)
	detail := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string), token, nil)
	hier := decodeMap(t, detail)["hierarchy"].(map[string]any)
	parent := hier["parent"].(map[string]any)
	if parent["id"] != plain["id"] {
		t.Fatalf("parent = %v, want %v", parent["id"], plain["id"])
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
	container := createContainer(t, h, token, "proj", "Container")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "other", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": container,
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

func TestListTasksByParentAndHasChildren(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	container := createContainer(t, h, token, "proj", "Container")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": container,
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?parent="+container, token, nil)
	tasks := decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != child["id"] {
		t.Fatalf("children = %v, want [%v]", tasks, child["id"])
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks?has_children=true", token, nil)
	tasks = decodeMap(t, rr)["tasks"].([]any)
	if len(tasks) != 1 || tasks[0].(map[string]any)["id"] != container {
		t.Fatalf("parents = %v, want [%s]", tasks, container)
	}
}

// TestListTasksTree pins the one-request hierarchy read (WL-169): tree=true
// answers with every root container, its roll-up, and its children, so a
// client never issues a child list per container.
func TestListTasksTree(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	container := createContainer(t, h, token, "proj", "Container")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": container,
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Loose", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?tree=true&project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	nodes := decodeMap(t, rr)["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %v, want just the container (a childless task is no root)", nodes)
	}
	node := nodes[0].(map[string]any)
	if node["parent"].(map[string]any)["id"] != container {
		t.Fatalf("parent = %v, want %s", node["parent"], container)
	}
	kids := node["children"].([]any)
	if len(kids) != 1 || kids[0].(map[string]any)["id"] != child["id"] {
		t.Fatalf("children = %v, want [%v]", kids, child["id"])
	}
	if got := node["progress"].(map[string]any)["total"]; got != float64(1) {
		t.Fatalf("progress total = %v, want 1", got)
	}

	// root narrows the tree to one container.
	rr = doReq(t, h, "GET", "/api/v1/tasks?tree=true&root="+container, token, nil)
	nodes = decodeMap(t, rr)["nodes"].([]any)
	if len(nodes) != 1 || nodes[0].(map[string]any)["parent"].(map[string]any)["id"] != container {
		t.Fatalf("root tree = %v, want just %s", nodes, container)
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks?tree=true&root=PROJ-999", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown root status = %d, want 404", rr.Code)
	}

	// A non-boolean tree is named, not read as off — the stance every other
	// boolean query parameter takes.
	rr = doReq(t, h, "GET", "/api/v1/tasks?tree=yes%20please", token, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// TestListTasksTreeChildrenIgnoreStateFilter pins that state narrows which
// containers a tree reports, never which of their children it lists: the
// progress counts and the listed children must describe the same set.
func TestListTasksTreeChildrenIgnoreStateFilter(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	container := createContainer(t, h, token, "proj", "Container")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Draft piece", "priority": "medium", "kind": "feature",
		"parent": container, "draft": true,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?tree=true&project=proj&state=ready", token, nil)
	nodes := decodeMap(t, rr)["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %v, want the ready container", nodes)
	}
	if kids := nodes[0].(map[string]any)["children"].([]any); len(kids) != 1 {
		t.Fatalf("children = %v, want the draft child listed even though state=ready", kids)
	}
}
