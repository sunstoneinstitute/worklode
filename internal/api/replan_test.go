package api_test

// replan_test.go covers POST /api/v1/work/replan (S28): the surface a stale
// plan document reaches the caller through when the CLI's `lode work next
// --replan` asks for it. The store-level behaviour (mint-or-reuse, the
// concurrency guard) is internal/store/replan_test.go's; what is checked
// here is the HTTP shape.

import (
	"context"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestReplanClaimsStalePlan: a stale plan in a seeded project is handed out
// as a claimed task; GET on the claimed id confirms it is a design task
// about the plan document (ClaimNextPick carries no kind of its own).
func TestReplanClaimsStalePlan(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	plan := createDocViaAPI(t, h, token, model.CreateDocInput{
		Project: "proj", Kind: "plan", Slug: "mint-plan", Body: docPlanMintBody,
	})
	acceptDocViaAPI(t, h, token, plan.ID)
	if _, err := st.DBForTests().ExecContext(context.Background(),
		`UPDATE docs SET status = 'stale' WHERE id = $1`, plan.ID); err != nil {
		t.Fatalf("mark plan stale: %v", err)
	}

	rr := doReq(t, h, "POST", "/api/v1/work/replan", token, map[string]any{
		"project": "proj", "worktree": "host:/wt-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("replan status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["claimed"] != true {
		t.Fatalf("claimed = %v, want true", got["claimed"])
	}
	task, ok := got["task"].(map[string]any)
	if !ok {
		t.Fatalf("task missing: %v", got)
	}
	taskID, _ := task["id"].(string)

	rr = doReq(t, h, "GET", "/api/v1/tasks/"+taskID, token, nil)
	full := decodeMap(t, rr)
	if full["kind"] != "design" {
		t.Fatalf("kind = %v, want design", full["kind"])
	}
	if int64(full["about_doc"].(float64)) != plan.ID {
		t.Fatalf("about_doc = %v, want %d", full["about_doc"], plan.ID)
	}
}

// TestReplanNoStalePlan covers the not-claimed shape: no stale plan in the
// project yields claimed:false with a replan-specific reason, not the
// ordinary claim-next "no-ready-task".
func TestReplanNoStalePlan(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/work/replan", token, map[string]any{
		"project": "proj", "worktree": "host:/wt-1",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("replan status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["claimed"] != false || got["reason"] != "no stale plan" {
		t.Fatalf("body = %v, want claimed:false reason:\"no stale plan\"", got)
	}
}

// TestReplanMissingProject covers the 400 guard.
func TestReplanMissingProject(t *testing.T) {
	t.Parallel()
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/work/replan", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rr.Code, rr.Body.String())
	}
}

// TestReplanUnknownRefIs404: a named plan ref that resolves to nothing is
// 404, not a 200 claimed:false — a typoed ref must not read as an empty
// queue (store.ReplanNext's ErrNotFound-with-a-ref case).
func TestReplanUnknownRefIs404(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	rr := doReq(t, h, "POST", "/api/v1/work/replan", token, map[string]any{
		"project": "proj", "plan": "WL-PLAN-99", "worktree": "host:/wt-1",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}
}
