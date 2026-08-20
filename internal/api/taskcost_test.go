package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestGetTaskCost covers the 200 shape: a task with recorded usage reports
// its sessions and cost, scoped to itself when children is not requested.
func TestGetTaskCost(t *testing.T) {
	st, h, token := newTestServer(t)
	id := endSessionWithUsage(t, st, h, token, []map[string]any{sonnetUsagePrevDay, sonnetUsage})

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id+"/cost", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task cost status = %d, body %s", rr.Code, rr.Body.String())
	}
	var tc model.TaskCost
	decodeInto(t, rr, &tc)
	if tc.Task != id {
		t.Fatalf("task = %q, want %q", tc.Task, id)
	}
	if tc.IncludesChildren {
		t.Fatalf("includes_children = true, want false")
	}
	if tc.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", tc.Sessions)
	}
	if len(tc.Cost.Days) != 2 {
		t.Fatalf("days = %+v, want 2", tc.Cost.Days)
	}
	if len(tc.Cost.Totals) != 1 || tc.Cost.Totals[0].CostAmount != "10.000000" {
		t.Fatalf("totals = %+v, want one total of 10.000000", tc.Cost.Totals)
	}
}

// TestGetTaskCostNotFound covers 404 for an unknown task id.
func TestGetTaskCostNotFound(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/tasks/nosuch/cost", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestGetTaskCostBadParams covers 400 on a malformed from and a malformed
// children value.
func TestGetTaskCostBadParams(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, st, h, token)

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id+"/cost?from=31-07-2026", token, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad from status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/cost?children=maybe", token, nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad children status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestGetTaskCostChildren covers children=true reaching the store with the
// flag set: a container task's own sessions are empty, but its child's usage
// shows up once children is requested.
func TestGetTaskCostChildren(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	parent := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Container", "priority": "high", "kind": "feature",
	})
	parentID, _ := parent["id"].(string)

	child := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Child", "priority": "high", "kind": "bug", "parent": parentID,
	})
	childID, _ := child["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+childID+"/claim", token,
		map[string]any{"worktree": "host:/.worktrees/one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim child: got %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+childID+"/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch: got %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+childID+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "usage": []map[string]any{sonnetUsage}})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end with usage: got %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+parentID+"/cost", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get parent cost status = %d, body %s", rr.Code, rr.Body.String())
	}
	var own model.TaskCost
	decodeInto(t, rr, &own)
	if own.Sessions != 0 || len(own.Cost.Totals) != 0 {
		t.Fatalf("container own cost = %+v, want empty", own)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+parentID+"/cost?children=true", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get parent cost with children status = %d, body %s", rr.Code, rr.Body.String())
	}
	var withChildren model.TaskCost
	decodeInto(t, rr, &withChildren)
	if !withChildren.IncludesChildren {
		t.Fatalf("includes_children = false, want true")
	}
	if withChildren.Sessions != 1 {
		t.Fatalf("sessions = %d, want 1", withChildren.Sessions)
	}
	if len(withChildren.Cost.Totals) != 1 || withChildren.Cost.Totals[0].CostAmount != "9.000000" {
		t.Fatalf("totals = %+v, want one total of 9.000000", withChildren.Cost.Totals)
	}
}
