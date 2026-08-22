package api_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// mintTaskToken drives POST /api/v1/tasks/{id}/tokens and fails the test on
// anything but 201.
func mintTaskToken(t *testing.T, h http.Handler, token, taskID string, body any) model.TaskTokenResponse {
	t.Helper()
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+taskID+"/tokens", token, body)
	if rr.Code != http.StatusCreated {
		t.Fatalf("mint task token status = %d, body %s", rr.Code, rr.Body.String())
	}
	var resp model.TaskTokenResponse
	decodeInto(t, rr, &resp)
	return resp
}

func TestMintTaskTokenDefaults(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "T"})
	id := task["id"].(string)

	resp := mintTaskToken(t, h, token, id, nil)
	if resp.Actor != "sandbox" {
		t.Fatalf("actor = %q, want sandbox (auto-provisioned default)", resp.Actor)
	}
	if resp.Task != id {
		t.Fatalf("task = %q, want %q", resp.Task, id)
	}
	if !strings.HasPrefix(resp.Token, "wl_") {
		t.Fatalf("token %q lacks wl_ prefix", resp.Token)
	}
	if resp.ExpiresAt.IsZero() {
		t.Fatal("expires_at is zero; want the lease-TTL default")
	}

	// The sandbox actor now exists, as a non-admin agent.
	a, err := st.GetActor(t.Context(), "sandbox")
	if err != nil {
		t.Fatalf("GetActor sandbox: %v", err)
	}
	if a.Kind != "agent" || a.Admin {
		t.Fatalf("sandbox actor kind=%q admin=%v; want agent, non-admin", a.Kind, a.Admin)
	}
}

func TestMintTaskTokenRefusals(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "T"})
	id := task["id"].(string)

	// Unknown task.
	if rr := doReq(t, h, "POST", "/api/v1/tasks/WL-9999/tokens", token, nil); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task: status = %d, want 404", rr.Code)
	}
	// Unknown actor.
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/tokens", token, model.TaskTokenInput{Actor: "nobody"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown actor: status = %d, want 404", rr.Code)
	}
	// TTL beyond the 24h cap.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/tokens", token, model.TaskTokenInput{TTLSeconds: 25 * 3600})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("over-long ttl: status = %d, want 422", rr.Code)
	}
}

// TestTaskTokenScope is the 001 §2.1 narrowing: a task-scoped token reaches
// its own task's routes and the unbound worker surface, and nothing else.
func TestTaskTokenScope(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	mine := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Mine"})["id"].(string)
	other := createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Other"})["id"].(string)
	scoped := mintTaskToken(t, h, token, mine, nil).Token

	// Bound route, matching task: allowed.
	if rr := doReq(t, h, "GET", "/api/v1/tasks/"+mine, scoped, nil); rr.Code != http.StatusOK {
		t.Fatalf("own task read: status = %d, body %s", rr.Code, rr.Body.String())
	}
	// Bound route, another task: refused.
	rr := doReq(t, h, "GET", "/api/v1/tasks/"+other, scoped, nil)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("other task read: status = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "scoped to task "+mine) {
		t.Fatalf("refusal body %q does not name the bound task", rr.Body.String())
	}
	// Unmarked route (mint another task token): refused even for its own task.
	if rr := doReq(t, h, "POST", "/api/v1/tasks/"+mine+"/tokens", scoped, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("mint via task token: status = %d, want 403", rr.Code)
	}
	// Unmarked admin route: refused before permission is even weighed.
	if rr := doReq(t, h, "POST", "/api/v1/actors/sandbox/tokens", scoped, nil); rr.Code != http.StatusForbidden {
		t.Fatalf("actor token mint via task token: status = %d, want 403", rr.Code)
	}
	// Unbound worker surface (taskScopeAny): allowed.
	if rr := doReq(t, h, "GET", "/api/v1/tasks?project=proj", scoped, nil); rr.Code != http.StatusOK {
		t.Fatalf("task list: status = %d, body %s", rr.Code, rr.Body.String())
	}
	if rr := doReq(t, h, "GET", "/api/v1/whoami", scoped, nil); rr.Code != http.StatusOK {
		t.Fatalf("whoami: status = %d, body %s", rr.Code, rr.Body.String())
	}
	_ = st
}

func TestTaskTokenMetric(t *testing.T) {
	st, main, admin, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")
	id := createTaskViaAPI(t, main, token, map[string]any{"project": "proj", "title": "T"})["id"].(string)
	mintTaskToken(t, main, token, id, nil)

	rr := doReq(t, admin, "GET", "/metrics", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("metrics status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `worklode_task_tokens_total{outcome="ok"} 1`) {
		t.Fatal("worklode_task_tokens_total ok=1 not found in /metrics")
	}
}
