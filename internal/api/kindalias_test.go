package api_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestCreateTaskAliasesDeprecatedKind proves POST /api/v1/tasks accepts the
// retired "spec" spelling (migration 0025 renamed it to "design"), stores
// and returns "design", and counts the alias use on
// worklode_task_kind_alias_uses_total.
func TestCreateTaskAliasesDeprecatedKind(t *testing.T) {
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Old spelling", "priority": "high", "kind": "spec",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["kind"] != "design" {
		t.Fatalf("kind = %v, want design", got["kind"])
	}

	task, err := st.GetTask(context.Background(), got["id"].(string))
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if task.Kind != "design" {
		t.Fatalf("stored kind = %q, want design", task.Kind)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="create"} 1`) {
		t.Errorf("metrics missing kind alias counter: %s", metrics)
	}
}

// TestCreateTaskUnknownKindStays422 proves the alias gate does not loosen the
// existing validKinds check, and that an unrecognized kind never bumps the
// alias counter.
func TestCreateTaskUnknownKindStays422(t *testing.T) {
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Nonsense kind", "priority": "high", "kind": "nonsense",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "invalid kind") {
		t.Fatalf("body = %s, want the invalid-kind message", rr.Body.String())
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if strings.Contains(metrics, `alias="nonsense"`) {
		t.Errorf("metrics counted an unknown kind as an alias: %s", metrics)
	}
	// Pre-initialised, so "nothing sends the alias any more" reads as a flat
	// zero rather than as no-data — which is the evidence the alias is dropped
	// on.
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="create"} 0`) {
		t.Errorf("alias counter is not pre-initialised to zero: %s", metrics)
	}
}

// TestCreateTaskFromFormAliasesDeprecatedKind proves the web form normalizes
// "spec" the same way the JSON API does, and counts the alias use on the
// web_form surface.
func TestCreateTaskFromFormAliasesDeprecatedKind(t *testing.T) {
	st, h, admin, _ := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doForm(t, h, "/projects/proj/tasks", url.Values{
		"title":    {"Old spelling via form"},
		"priority": {"high"},
		"kind":     {"spec"},
	}, nil)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %s", rr.Code, rr.Body.String())
	}
	task, err := st.GetTask(context.Background(), "WL-1")
	if err != nil {
		t.Fatalf("get created task: %v", err)
	}
	if task.Kind != "design" {
		t.Fatalf("kind = %q, want design", task.Kind)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="web_form"} 1`) {
		t.Errorf("metrics missing kind alias counter: %s", metrics)
	}
}

// TestListTasksByDeprecatedKind proves `?kind=spec` still returns the design
// tasks migration 0025 rewrote, rather than an empty set, and counts the
// alias use on the list surface.
func TestListTasksByDeprecatedKind(t *testing.T) {
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "A design doc task", "priority": "medium", "kind": "design",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Unrelated bug", "priority": "medium", "kind": "bug",
	})

	rr := doReq(t, h, "GET", "/api/v1/tasks?project=proj&kind=spec", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Tasks []map[string]any `json:"tasks"`
	}
	decodeInto(t, rr, &resp)
	if len(resp.Tasks) != 1 || resp.Tasks[0]["kind"] != "design" {
		t.Fatalf("tasks = %+v, want exactly the one design task", resp.Tasks)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="list"} 1`) {
		t.Errorf("metrics missing kind alias counter: %s", metrics)
	}
}

// TestClaimNextAliasesDeprecatedKind proves claim-next's kind filter accepts
// "spec" and picks a design task.
func TestClaimNextAliasesDeprecatedKind(t *testing.T) {
	st, h, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Critical feature", "priority": "critical", "kind": "feature",
	})
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "A design task", "priority": "low", "kind": "design",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/claim-next", token, map[string]any{
		"project": "proj", "kind": "spec", "worktree": "host:/wt-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	task, ok := got["task"].(map[string]any)
	if !ok {
		t.Fatalf("task missing: %v", got)
	}
	// The claim-next pick shape (model.ClaimNextPick) carries no kind field,
	// so the claimed task is fetched back to check what was actually picked.
	claimed, err := st.GetTask(context.Background(), task["id"].(string))
	if err != nil {
		t.Fatalf("get claimed task: %v", err)
	}
	if claimed.Kind != "design" {
		t.Fatalf("claimed kind = %q, want design", claimed.Kind)
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="claim_next"} 1`) {
		t.Errorf("metrics missing kind alias counter: %s", metrics)
	}
}

// TestPromoteInboxAliasesDeprecatedKind proves promote-inbox accepts "spec",
// creates a design task, and counts the alias use on the promote surface.
func TestPromoteInboxAliasesDeprecatedKind(t *testing.T) {
	st, h, admin, token := newTestServerWithAdmin(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "spec",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["kind"] != "design" {
		t.Fatalf("kind = %v, want design", got["kind"])
	}

	metrics := doReq(t, admin, "GET", "/metrics", "", nil).Body.String()
	if !strings.Contains(metrics, `worklode_task_kind_alias_uses_total{alias="spec",surface="promote"} 1`) {
		t.Errorf("metrics missing kind alias counter: %s", metrics)
	}
}
