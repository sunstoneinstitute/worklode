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

// sonnetUsage is a usage bucket priced by migration 0008's seed rate for
// claude-sonnet-5 (standard, $2/$10 per MTok before 2026-09-01):
// 1e6 input + 1e6 1h-cache-write + 1e7 cache read + 1e5 output = $9.000000.
// The classes are deliberately unequal, so a handler that folded them into
// one number could not produce this amount.
var sonnetUsage = map[string]any{
	"day": "2026-07-31", "model": "claude-sonnet-5",
	"input_tokens": 1_000_000, "cache_write_1h_tokens": 1_000_000,
	"cache_read_tokens": 10_000_000, "output_tokens": 100_000,
}

// sonnetUsagePrevDay bills 1e5 output tokens at the same rate: $1.000000, on
// the day before sonnetUsage — the second day a window filter can exclude.
var sonnetUsagePrevDay = map[string]any{
	"day": "2026-07-30", "model": "claude-sonnet-5", "output_tokens": 100_000,
}

// endSessionWithUsage runs the whole path a project's recorded cost arrives
// by: claim a task in project "proj", open an agent session, end it reporting
// usage. It returns the task id, so callers can reopen the session.
func endSessionWithUsage(t *testing.T, st *store.Store, h http.Handler, token string, usage []map[string]any) string {
	t.Helper()
	id := claimedTask(t, st, h, token)
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch: got %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "usage": usage})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end with usage: got %d, body %s", rr.Code, rr.Body.String())
	}
	return id
}

// TestAgentSessionUsageBuckets covers the reported breakdown reaching the
// project rollup priced, and an end that omits usage leaving it alone.
func TestAgentSessionUsageBuckets(t *testing.T) {
	st, h, token := newTestServer(t)
	id := endSessionWithUsage(t, st, h, token, []map[string]any{sonnetUsagePrevDay, sonnetUsage})

	cost := projectCost(t, h, token, "/api/v1/projects/proj")
	if len(cost.Days) != 2 {
		t.Fatalf("days = %+v, want 2", cost.Days)
	}
	if cost.Days[0].Day != "2026-07-30" || cost.Days[0].CostAmount != "1.000000" {
		t.Fatalf("first day = %+v, want 2026-07-30 at 1.000000", cost.Days[0])
	}
	d := cost.Days[1]
	if d.Day != "2026-07-31" || d.Currency != "USD" || d.CostAmount != "9.000000" {
		t.Fatalf("second day = %+v, want 2026-07-31 USD 9.000000", d)
	}
	if d.InputTokens != 1_000_000 || d.CacheWrite1hTokens != 1_000_000 ||
		d.CacheReadTokens != 10_000_000 || d.OutputTokens != 100_000 {
		t.Fatalf("second day tokens = %+v", d)
	}
	if d.UnpricedTokens != 0 {
		t.Fatalf("unpriced_tokens = %d, want 0: claude-sonnet-5 has a seeded rate", d.UnpricedTokens)
	}
	if len(cost.Totals) != 1 || cost.Totals[0].Currency != "USD" || cost.Totals[0].CostAmount != "10.000000" {
		t.Fatalf("totals = %+v, want one USD total of 10.000000", cost.Totals)
	}

	// A heartbeat reopens the closed session; ending it again without a usage
	// field must leave what was already recorded in place.
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("reopen: got %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end without usage: got %d, body %s", rr.Code, rr.Body.String())
	}
	if again := projectCost(t, h, token, "/api/v1/projects/proj"); len(again.Days) != 2 ||
		again.Totals[0].CostAmount != "10.000000" {
		t.Fatalf("cost after an end with no usage = %+v, want unchanged", again)
	}
}

// TestAgentSessionEndRejectsMalformedUsage checks a bad bucket is a 400 and
// is rejected whole: the session stays open and endable afterwards.
func TestAgentSessionEndRejectsMalformedUsage(t *testing.T) {
	st, h, token := newTestServer(t)
	id := claimedTask(t, st, h, token)
	rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("touch: got %d, body %s", rr.Code, rr.Body.String())
	}

	for name, bucket := range map[string]map[string]any{
		"malformed day": {"day": "31-07-2026", "model": "claude-sonnet-5", "output_tokens": 10},
		"missing day":   {"model": "claude-sonnet-5", "output_tokens": 10},
		"missing model": {"day": "2026-07-31", "output_tokens": 10},
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
				map[string]any{"agent": "claude-code", "session_id": "sess-1", "usage": []map[string]any{bucket}})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("got %d, want 400, body %s", rr.Code, rr.Body.String())
			}
		})
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/"+id+"/agent-session/end", token,
		map[string]any{"agent": "claude-code", "session_id": "sess-1", "usage": []map[string]any{sonnetUsage}})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("end after rejected usage: got %d, body %s", rr.Code, rr.Body.String())
	}
	if cost := projectCost(t, h, token, "/api/v1/projects/proj"); len(cost.Days) != 1 ||
		cost.Days[0].CostAmount != "9.000000" {
		t.Fatalf("cost = %+v, want only the accepted bucket", cost)
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
