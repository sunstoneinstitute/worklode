package api_test

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// poseBody is the smallest legal body for each response type, so a test that
// only cares about the endpoint does not restate §10.1's option rules.
func poseBody(key, responseType string) map[string]any {
	b := map[string]any{"key": key, "question": "Which way?", "response_type": responseType}
	switch responseType {
	case "single_select", "multi_select", "single_select_notes", "pick_or_freetext":
		b["options"] = []map[string]any{{"label": "a"}, {"label": "b", "description": "the other one"}}
	}
	return b
}

// decisionsOf reads the decisions GET /api/v1/tasks/{id} reports.
func decisionsOf(t *testing.T, h http.Handler, token, id string) []map[string]any {
	t.Helper()
	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id, token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task %s status = %d, body %s", id, rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	raw, _ := got["decisions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.(map[string]any))
	}
	return out
}

// TestReadDecisions covers GET /api/v1/tasks/{id}/decisions and
// .../decisions/{key}: an empty list before anything is posed, the rows in
// authored order after, one row by key, 404 on an unknown key or task, and
// the answer fields appearing only once the row is answered.
func TestReadDecisions(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	id := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})["id"].(string)

	rr := doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list on a bare task status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := strings.TrimSpace(rr.Body.String()); got != "[]" {
		t.Fatalf("list on a bare task = %s, want []", got)
	}

	for _, key := range []string{"x-distribution", "ship-date"} {
		if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody(key, "yes_no")); rr.Code != http.StatusCreated {
			t.Fatalf("pose %s status = %d, body %s", key, rr.Code, rr.Body.String())
		}
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body %s", rr.Code, rr.Body.String())
	}
	var rows []map[string]any
	decodeInto(t, rr, &rows)
	if len(rows) != 2 || rows[0]["key"] != "x-distribution" || rows[1]["key"] != "ship-date" {
		t.Fatalf("list = %v, want the two rows in authored order", rows)
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions/ship-date", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["key"] != "ship-date" || got["task"] != id || got["response_type"] != "yes_no" {
		t.Fatalf("got row = %v", got)
	}
	if _, ok := got["answer"]; ok {
		t.Fatalf("unanswered row carries an answer: %v", got)
	}

	answerDecision(t, st, id, "ship-date")
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions/ship-date", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get answered status = %d, body %s", rr.Code, rr.Body.String())
	}
	got = decodeMap(t, rr)
	answer, _ := got["answer"].(map[string]any)
	if answer["value"] != "yes" || got["decided_at"] == nil {
		t.Fatalf("answered row = %v", got)
	}

	if rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions/nope", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("get unknown key status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if rr = doReq(t, h, "GET", "/api/v1/tasks/WL-999/decisions", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("list on unknown task status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
	if rr = doReq(t, h, "GET", "/api/v1/tasks/WL-999/decisions/k", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("get on unknown task status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestPoseDecisionEveryResponseType: POST /api/v1/tasks/{id}/decisions takes
// one row of each of §10.1's six response types and returns 201 with the row.
func TestPoseDecisionEveryResponseType(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})
	id := created["id"].(string)

	types := []string{
		"single_select", "multi_select", "single_select_notes",
		"pick_or_freetext", "yes_no", "freetext",
	}
	for i, rt := range types {
		key := strings.ReplaceAll(rt, "_", "-")
		rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody(key, rt))
		if rr.Code != http.StatusCreated {
			t.Fatalf("pose %s status = %d, body %s", rt, rr.Code, rr.Body.String())
		}
		got := decodeMap(t, rr)
		if got["key"] != key || got["response_type"] != rt || got["task"] != id {
			t.Fatalf("posed %s row = %v", rt, got)
		}
		// position defaults to max+1, starting at 1 for the first row.
		if got["position"] != float64(i+1) {
			t.Fatalf("posed %s position = %v, want %d", rt, got["position"], i+1)
		}
		if _, ok := got["answer"]; ok {
			t.Fatalf("posed %s carries an answer: %v", rt, got)
		}
	}
	if rows := decisionsOf(t, h, token, id); len(rows) != len(types) {
		t.Fatalf("task decisions = %d rows, want %d", len(rows), len(types))
	}
}

// TestPoseDecisionOnFeatureTask: any kind may carry rows (§10.1).
func TestPoseDecisionOnFeatureTask(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature",
	})
	id := created["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody("x-distribution", "yes_no"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("pose on a feature task status = %d, body %s", rr.Code, rr.Body.String())
	}
}

// TestPoseDecisionRejections: a spec violation is 422 naming the field, and a
// key already used on the task is 409.
func TestPoseDecisionRejections(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})
	id := created["id"].(string)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token,
		map[string]any{"key": "Bad Key", "question": "q", "response_type": "yes_no"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad key status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token,
		map[string]any{"key": "k", "question": "q", "response_type": "single_select"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing options status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	if rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody("k", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("first pose status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody("k", "freetext"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate key status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

// TestEditUnansweredDecision: PATCH rewords an unanswered row and returns it.
func TestEditUnansweredDecision(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})
	id := created["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody("k", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("pose status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/"+id+"/decisions/k", token,
		map[string]any{"question": "Do we ship in October?", "context": "budget is fixed", "group": "scope"})
	if rr.Code != http.StatusOK {
		t.Fatalf("edit status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["question"] != "Do we ship in October?" || got["context"] != "budget is fixed" || got["group"] != "scope" {
		t.Fatalf("edited row = %v", got)
	}
	// Untouched fields survive the edit.
	if got["response_type"] != "yes_no" {
		t.Fatalf("edit changed response_type: %v", got)
	}

	// Retyping replaces the whole answer-shape group.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/"+id+"/decisions/k", token,
		map[string]any{"response_type": "single_select", "options": []map[string]any{{"label": "yes"}, {"label": "no"}}})
	if rr.Code != http.StatusOK {
		t.Fatalf("retype status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got = decodeMap(t, rr); got["response_type"] != "single_select" {
		t.Fatalf("retyped row = %v", got)
	}

	// An edit onto an unknown key is a 404, not a silent insert.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/"+id+"/decisions/nope", token,
		map[string]any{"question": "q"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("edit unknown key status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}

// TestEditAnsweredDecisionRefused: an answered row is immutable (§10.1). The
// answer is written directly because the recording endpoint is WL-640's.
func TestEditAnsweredDecisionRefused(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	created := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})
	id := created["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token, poseBody("k", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("pose status = %d, body %s", rr.Code, rr.Body.String())
	}
	answerDecision(t, st, id, "k")

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/"+id+"/decisions/k", token,
		map[string]any{"question": "reworded"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("edit answered row status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

// answerDecision stamps an answer on a posed row through the store's own
// transaction. WL-640 owns the endpoint that does this properly; this test
// only needs a row that is answered.
func answerDecision(t *testing.T, st *store.Store, taskID, key string) {
	t.Helper()
	err := st.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`UPDATE decisions SET answer = '{"value":"yes"}'::jsonb, decided_at = now()
			  WHERE task_id = $1 AND key = $2`, taskID, key)
		return err
	})
	if err != nil {
		t.Fatalf("stamp answer on %s/%s: %v", taskID, key, err)
	}
}

// TestReparentDecision: "task" in the PATCH body moves an unanswered row to
// another task, and the old task stops listing it (§10.1).
func TestReparentDecision(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	from := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Today's meeting", "priority": "high", "kind": "decision",
	})["id"].(string)
	to := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Follow-up", "priority": "high", "kind": "decision",
	})["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+from+"/decisions", token, poseBody("k", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("pose status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/"+from+"/decisions/k", token, map[string]any{"task": to})
	if rr.Code != http.StatusOK {
		t.Fatalf("re-parent status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["task"] != to {
		t.Fatalf("re-parented row = %v, want task %s", got, to)
	}
	if rows := decisionsOf(t, h, token, from); len(rows) != 0 {
		t.Fatalf("old task still lists %d rows: %v", len(rows), rows)
	}
	rows := decisionsOf(t, h, token, to)
	if len(rows) != 1 || rows[0]["key"] != "k" {
		t.Fatalf("new task decisions = %v", rows)
	}
}

// TestPatchTaskKindDecisionRefused: kind is fixed at pose. A task cannot be
// retyped into or out of "decision" — the closing rule differs, and the rows
// would change meaning under it (§10, §10.1).
func TestPatchTaskKindDecisionRefused(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	feature := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature",
	})["id"].(string)
	call := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})["id"].(string)

	rr := doReq(t, h, "PATCH", "/api/v1/tasks/"+feature, token, map[string]any{"kind": "decision"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("retype into decision status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/"+call, token, map[string]any{"kind": "chore"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("retype out of decision status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// Other edits on a decision task, and a no-op kind, still work.
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/"+call, token, map[string]any{"kind": "decision", "title": "Scope call, round two"})
	if rr.Code != http.StatusOK {
		t.Fatalf("no-op kind status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/"+feature, token, map[string]any{"kind": "chore"})
	if rr.Code != http.StatusOK {
		t.Fatalf("retype between non-decision kinds status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
}

// TestCreateTaskWithDecisions: the initial list is the second door onto the
// same write, and a bare decision-kind task with no rows is legal while
// drafting.
func TestCreateTaskWithDecisions(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	bare := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})["id"].(string)
	if rows := decisionsOf(t, h, token, bare); len(rows) != 0 {
		t.Fatalf("bare decision task lists %v, want none", rows)
	}

	withRows := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
		"decisions": []map[string]any{
			poseBody("x-distribution", "single_select"),
			poseBody("ship-date", "freetext"),
		},
	})["id"].(string)
	rows := decisionsOf(t, h, token, withRows)
	if len(rows) != 2 || rows[0]["key"] != "x-distribution" || rows[1]["key"] != "ship-date" {
		t.Fatalf("created rows = %v", rows)
	}
	if rows[0]["position"] != float64(1) || rows[1]["position"] != float64(2) {
		t.Fatalf("created rows are out of authored order: %v", rows)
	}

	// A row on a feature task's create is accepted too, and an invalid one
	// fails the whole create.
	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature",
		"decisions": []map[string]any{poseBody("k", "yes_no")},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create feature task with a row status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Bad", "priority": "high", "kind": "decision",
		"decisions": []map[string]any{{"key": "k", "question": "q", "response_type": "nonsense"}},
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("create with an invalid row status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

// TestDecideDecision covers POST /api/v1/tasks/{id}/decisions/{key}/decide:
// the 200 body is the answered row, a decision-kind task closes on its last
// answer, and the store's refusals map to 409/422/404.
func TestDecideDecision(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	// Answering an unassigned task assigns it to the decider, and an
	// assignee must be on the project's crew (029 §6.1).
	if rr := doReq(t, h, "POST", "/api/v1/projects/proj/participants", token,
		map[string]any{"actor": "alice"}); rr.Code != http.StatusCreated {
		t.Fatalf("add alice to the crew status = %d, body %s", rr.Code, rr.Body.String())
	}
	id := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token,
		poseBody("ship-date", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("pose status = %d, body %s", rr.Code, rr.Body.String())
	}

	path := "/api/v1/tasks/" + id + "/decisions/ship-date/decide"
	rr := doReq(t, h, "POST", path, token, map[string]any{"value": "yes"})
	if rr.Code != http.StatusOK {
		t.Fatalf("decide status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	answer, _ := got["answer"].(map[string]any)
	if answer["value"] != "yes" || got["decided_by"] != "alice" || got["decided_at"] == nil {
		t.Fatalf("answered row = %v", got)
	}
	task := doReq(t, h, "GET", "/api/v1/tasks/"+id, token, nil)
	if state := decodeMap(t, task)["state"]; state != "merged" {
		t.Fatalf("task state = %v, want merged: the last answer closes the task", state)
	}

	// Recording is terminal, and a closed task is past deciding: both are
	// conflicts, not bad input.
	if rr = doReq(t, h, "POST", path, token, map[string]any{"value": "no"}); rr.Code != http.StatusConflict {
		t.Fatalf("re-decide status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
}

// TestDecideDecisionRejectsAnAnswerTheTypeDoesNotDefine: a spec violation is
// 422, and the row stays unanswered.
func TestDecideDecisionRejectsAnAnswerTheTypeDoesNotDefine(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	id := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Scope call", "priority": "high", "kind": "decision",
	})["id"].(string)
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions", token,
		poseBody("ship-date", "yes_no")); rr.Code != http.StatusCreated {
		t.Fatalf("pose status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions/ship-date/decide", token,
		map[string]any{"freetext": "sometime"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks/"+id+"/decisions/ship-date", token, nil)
	if _, ok := decodeMap(t, rr)["answer"]; ok {
		t.Fatalf("a refused answer was written: %s", rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/decisions/nope/decide", token,
		map[string]any{"value": "yes"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown key status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
