package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// claimedTask creates a project and a task and claims it as alice, returning
// the task id. Built from the same helpers TestClaim uses in lifecycle_test.go.
func claimedTask(t *testing.T, st *store.Store, h http.Handler, token string) string {
	t.Helper()
	createProject(t, st, "proj")
	task := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Agent session task", "priority": "high", "kind": "bug",
	})
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatalf("created task has no id: %v", task)
	}
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/claim", token,
		map[string]any{"worktree": "host:/wt/one"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim: got %d, body %s", rr.Code, rr.Body.String())
	}
	return id
}

func TestAgentSessionEndpoints(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, st, h, token)

	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "agent_version": "2.0.1", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch: got %d, body %s", rr.Code, rr.Body.String())
	}

	var got struct {
		LeaseID    int64  `json:"lease_id"`
		Agent      string `json:"agent"`
		SessionID  string `json:"session_id"`
		StartedAt  string `json:"started_at"`
		LastSeenAt string `json:"last_seen_at"`
		EndedAt    string `json:"ended_at"`
	}
	decodeInto(t, rr, &got)
	if got.Agent != "claude-code" || got.SessionID != "sess-1" {
		t.Fatalf("touch body: %+v", got)
	}
	if got.LastSeenAt == "" {
		t.Fatal("touch body: last_seen_at missing")
	}
	if got.EndedAt != "" {
		t.Fatalf("touch body: ended_at = %q, want empty for a running session", got.EndedAt)
	}

	// A second touch is a heartbeat: same session identity (lease_id,
	// started_at unchanged), not a new row.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "agent_version": "2.0.1", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat: got %d, body %s", rr.Code, rr.Body.String())
	}
	var heartbeat struct {
		LeaseID   int64  `json:"lease_id"`
		StartedAt string `json:"started_at"`
	}
	decodeInto(t, rr, &heartbeat)
	if heartbeat.LeaseID != got.LeaseID || heartbeat.StartedAt != got.StartedAt {
		t.Fatalf("heartbeat identity: got %+v, want lease_id=%d started_at=%s",
			heartbeat, got.LeaseID, got.StartedAt)
	}

	// A malformed cost amount is rejected before the session is touched, so
	// the still-open session below is unaffected.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "cost_amount": "abc"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed cost_amount: got %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "input_tokens": 42})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end: got %d, body %s", rr.Code, rr.Body.String())
	}

	// The 204 response carries no usage fields, so confirm input_tokens
	// actually landed by reading the row back through the store.
	sess, err := st.AgentSession(context.Background(), got.LeaseID, "claude-code", "sess-1")
	if err != nil {
		t.Fatalf("read back session: %v", err)
	}
	if sess.InputTokens == nil || *sess.InputTokens != 42 {
		t.Fatalf("input_tokens after end: got %v, want 42", sess.InputTokens)
	}
}

func TestAgentSessionRejectsNonHolderAndBadAgent(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, st, h, token)

	other := secondActor(t, st, "bob")
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", other,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-holder: got %d, want 404, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "not-a-tool", "session_id": "sess-1"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad agent: got %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing session id: got %d, want 422, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-999/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown task: got %d, want 404, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", "",
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated: got %d, want 401, body %s", rr.Code, rr.Body.String())
	}

	// Start a session so the /end cases below have one to act on.
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch for end cases: got %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "no-such-session"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("end unknown session: got %d, want 404, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", other,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("end as non-holder: got %d, want 404, body %s", rr.Code, rr.Body.String())
	}
}
