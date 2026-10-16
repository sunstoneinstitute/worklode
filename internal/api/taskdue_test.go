package api_test

import (
	"net/http"
	"testing"
)

// TestTaskDueRoundTrip covers WL-REQ-1796: post a due date, patch it to "" to
// clear it, and get 400 on a date that is not YYYY-MM-DD.
func TestTaskDueRoundTrip(t *testing.T) {
	t.Parallel()
	st, h, _, token := newTestServerWithAdmin(t)
	createProject(t, st, "proj")

	rr := doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Due", "priority": "high", "kind": "feature", "due": "2026-11-03",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["due"] != "2026-11-03" {
		t.Fatalf("due = %v", got["due"])
	}
	path := "/api/v1/tasks/" + got["id"].(string)

	if rr = doReq(t, h, "PATCH", path, token, map[string]any{"due": "2026-13-01"}); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad patch: %d %s", rr.Code, rr.Body.String())
	}
	if rr = doReq(t, h, "POST", "/api/v1/tasks", token, map[string]any{
		"project": "proj", "title": "Bad", "priority": "high", "kind": "feature", "due": "2026-13-01",
	}); rr.Code != http.StatusBadRequest {
		t.Fatalf("bad create: %d %s", rr.Code, rr.Body.String())
	}
	if rr = doReq(t, h, "PATCH", path, token, map[string]any{"due": ""}); rr.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rr.Code, rr.Body.String())
	}
	if d := decodeMap(t, rr)["due"]; d != "" {
		t.Fatalf("due after clear = %v", d)
	}
}
