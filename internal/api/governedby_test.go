package api_test

import (
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestGovernedBy exercises governedBy over the API: a task created with
// governed_by, then hand-added and hand-removed governing clauses
// (12-spec-refactoring-design-tree.md S2, S3).
func TestGovernedBy(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")
	createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: projID, Kind: "spec", Slug: "t", Body: clauseDocV1,
	})

	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": projID, "title": "t", "kind": "bug", "priority": "medium",
		"governed_by": []string{"WL-CL-3"},
	})
	id := created["id"].(string)

	detail := func() model.TaskDetail {
		rr := doReq(t, h, http.MethodGet, "/api/v1/tasks/"+id, token, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("get task: %d %s", rr.Code, rr.Body.String())
		}
		var d model.TaskDetail
		decodeInto(t, rr, &d)
		return d
	}
	if d := detail(); len(d.GovernedBy) != 1 || d.GovernedBy[0].Clause != "WL-CL-3" || d.GovernedBy[0].Source != "manual" {
		t.Fatalf("governed_by after create = %+v", d.GovernedBy)
	}

	rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-1"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("govern: %d %s", rr.Code, rr.Body.String())
	}
	if d := detail(); len(d.GovernedBy) != 2 || d.GovernedBy[0].Clause != "WL-CL-1" {
		t.Errorf("governed_by after govern = %+v", d.GovernedBy)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-999"}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown clause: %d", rr.Code)
	}
	if rr := doReq(t, h, http.MethodPost, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "junk"}); rr.Code != http.StatusBadRequest {
		t.Errorf("malformed clause: %d", rr.Code)
	}
	rr = doReq(t, h, http.MethodDelete, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-3"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("ungovern: %d %s", rr.Code, rr.Body.String())
	}
	if d := detail(); len(d.GovernedBy) != 1 || d.GovernedBy[0].Clause != "WL-CL-1" {
		t.Errorf("governed_by after ungovern = %+v", d.GovernedBy)
	}
	if rr := doReq(t, h, http.MethodDelete, "/api/v1/tasks/"+id+"/governed-by", token, map[string]any{"clause": "WL-CL-3"}); rr.Code != http.StatusNotFound {
		t.Errorf("ungovern absent link: %d", rr.Code)
	}
}

// TestCreateTaskBadGovernedByRefIs422: a governed_by ref naming no clause is
// caller error about the task body, not a missing task, so it must not read
// as the bare 404 ClauseIDByRef's ErrNotFound would otherwise produce.
func TestCreateTaskBadGovernedByRefIs422(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	projID := seedProjectWithKey(t, st, "WL")

	rr := doReq(t, h, http.MethodPost, "/api/v1/tasks", token, map[string]any{
		"project": projID, "title": "t", "kind": "bug", "priority": "medium",
		"governed_by": []string{"WL-CL-999"},
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad governed_by ref: %d %s, want 422", rr.Code, rr.Body.String())
	}
}
