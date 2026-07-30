package api_test

import (
	"net/http"
	"testing"
)

func TestTaskBrief(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Fix the: Thing!!", "priority": "high", "kind": "bug",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature",
	})

	// Claim WL-1, then make WL-2 an open blocker of it.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/edges", token, map[string]any{"to": "WL-1", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)

	task, ok := got["task"].(map[string]any)
	if !ok || task["id"] != "WL-1" {
		t.Fatalf("task = %v, want id WL-1", got["task"])
	}
	if got["branch"] != "lode/WL-1-fix-the-thing" {
		t.Fatalf("branch = %v, want lode/WL-1-fix-the-thing", got["branch"])
	}
	if _, ok := got["body"]; !ok {
		t.Fatalf("body key missing: %v", got)
	}

	blockers, ok := got["open_blockers"].([]any)
	if !ok || len(blockers) != 1 {
		t.Fatalf("open_blockers = %v, want one entry", got["open_blockers"])
	}
	blk := blockers[0].(map[string]any)
	if blk["id"] != "WL-2" || blk["state"] != "ready" || blk["title"] != "Blocker" {
		t.Fatalf("open blocker = %v, want id WL-2 state ready title Blocker", blk)
	}

	lease, ok := got["lease"].(map[string]any)
	if !ok || lease["worktree"] != "host:/wt-1" {
		t.Fatalf("lease = %v, want worktree host:/wt-1", got["lease"])
	}

	// Reserved fields are present and null in v1.
	for _, k := range []string{"governing_design", "affected_components", "definition_of_done"} {
		v, present := got[k]
		if !present || v != nil {
			t.Fatalf("%s = %v (present=%v), want JSON null", k, v, present)
		}
	}
}

func TestTaskBriefNoLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Unclaimed", "priority": "low", "kind": "chore",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["lease"] != nil {
		t.Fatalf("lease = %v, want null", got["lease"])
	}
	if blockers, ok := got["open_blockers"].([]any); !ok || len(blockers) != 0 {
		t.Fatalf("open_blockers = %v, want empty array", got["open_blockers"])
	}
}

func TestTaskBriefParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	epic := createEpic(t, h, token, "proj", "Delivery lifecycle")
	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Piece", "priority": "medium", "kind": "feature",
		"parent": epic,
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+child["id"].(string)+"/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	parent, ok := got["parent"].(map[string]any)
	if !ok {
		t.Fatalf("parent = %v, want an object", got["parent"])
	}
	if parent["id"] != epic || parent["title"] != "Delivery lifecycle" || parent["state"] != "ready" {
		t.Fatalf("parent = %v, want id %s title Delivery lifecycle state ready", parent, epic)
	}
}

func TestTaskBriefNoParent(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Root", "priority": "medium", "kind": "feature",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-1/brief", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("brief status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if v, present := got["parent"]; !present || v != nil {
		t.Fatalf("parent = %v (present=%v), want JSON null", v, present)
	}
}

func TestTaskBriefNotFound(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/tasks/WL-99/brief", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("brief unknown task status = %d, want 404", rr.Code)
	}
}

func TestRebindWorktree(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Work", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Holder rebinds: 200 with the updated lease.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", token, map[string]any{"worktree": "host:/wt-moved"})
	if rr.Code != http.StatusOK {
		t.Fatalf("rebind status = %d, body %s", rr.Code, rr.Body.String())
	}
	lease := decodeMap(t, rr)
	if lease["worktree"] != "host:/wt-moved" || lease["task_id"] != "WL-1" {
		t.Fatalf("rebound lease = %v, want worktree host:/wt-moved task_id WL-1", lease)
	}

	// Empty worktree: 400.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", token, map[string]any{})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("empty worktree status = %d, want 400", rr.Code)
	}

	// Non-holder: 404 (probe-resistant).
	bobToken := secondActor(t, st, "bob")
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/lease/worktree", bobToken, map[string]any{"worktree": "host:/wt-bob"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-holder rebind status = %d, want 404", rr.Code)
	}
}
