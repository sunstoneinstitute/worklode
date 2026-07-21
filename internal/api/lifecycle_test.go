package api_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/api"
	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// secondActor creates another actor and returns a bearer token for it.
func secondActor(t *testing.T, st *store.Store, id string) string {
	t.Helper()
	ctx := context.Background()
	if err := st.CreateActor(ctx, id, "agent", id, false); err != nil {
		t.Fatalf("create actor %s: %v", id, err)
	}
	token, err := st.CreateToken(ctx, id, "test token", nil)
	if err != nil {
		t.Fatalf("create token for %s: %v", id, err)
	}
	return token
}

// moveToReview transitions a task from in_progress to in_review via the store,
// simulating the PR-open transition.
func moveToReview(t *testing.T, st *store.Store, taskID string) {
	t.Helper()
	_, _, err := st.RecordEvent(context.Background(), "github", "to-review-"+taskID, "task.review", nil,
		func(tx *sql.Tx, eventID int64) error {
			return store.Transition(tx, st.Now(), taskID, "in_progress", "in_review", eventID)
		})
	if err != nil {
		t.Fatalf("move %s to in_review: %v", taskID, err)
	}
}

func TestSlugifyTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Fix the: Thing!!", "fix-the-thing"},
		{"simple", "simple"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"UPPER Case 123", "upper-case-123"},
		{"!!!", "task"},
		{"", "task"},
		{"æøå unicode überschrift", "unicode-berschrift"},
		{"a very long title that goes on and on and on and on and on", "a-very-long-title-that-goes-on-and-on-an"},
		{"ends-with-punctuation---", "ends-with-punctuation"},
	}
	for _, tc := range cases {
		if got := api.SlugifyTitle(tc.in); got != tc.want {
			t.Errorf("SlugifyTitle(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if got := api.SlugifyTitle(tc.in); len(got) > 40 {
			t.Errorf("SlugifyTitle(%q) = %q, longer than 40 chars", tc.in, got)
		}
	}
}

func TestClaim(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Fix the: Thing!!", "priority": "high", "kind": "bug",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "sess-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["branch"] != "wl/WL-1-fix-the-thing" {
		t.Fatalf("branch = %v, want wl/WL-1-fix-the-thing", got["branch"])
	}
	lease, ok := got["lease"].(map[string]any)
	if !ok {
		t.Fatalf("lease missing: %v", got)
	}
	if lease["task_id"] != "WL-1" || lease["actor_id"] != "alice" || lease["session_id"] != "sess-1" {
		t.Fatalf("lease = %v", lease)
	}
	for _, k := range []string{"acquired_at", "renewed_at", "expires_at"} {
		if s, _ := lease[k].(string); s == "" {
			t.Errorf("lease.%s missing or empty", k)
		}
	}

	// Task moved to in_progress.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got := decodeMap(t, rr); got["state"] != "in_progress" {
		t.Fatalf("state after claim = %v, want in_progress", got["state"])
	}
}

func TestClaimConflict(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Contested", "priority": "high", "kind": "feature",
	})
	bobToken := secondActor(t, st, "bob")

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("first claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", bobToken, map[string]any{"session_id": "s2"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second claim status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["error"] != "task already leased" {
		t.Fatalf("error = %v, want task already leased", got["error"])
	}
	holder, ok := got["holder"].(map[string]any)
	if !ok {
		t.Fatalf("holder missing: %v", got)
	}
	if holder["actor_id"] != "alice" {
		t.Fatalf("holder.actor_id = %v, want alice", holder["actor_id"])
	}
	if s, _ := holder["expires_at"].(string); s == "" {
		t.Fatalf("holder.expires_at missing: %v", holder)
	}
}

func TestClaimErrors(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocker", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Blocked", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Draft", "priority": "low", "kind": "chore", "draft": true})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/edges", token, map[string]any{"to": "WL-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add edge status = %d", rr.Code)
	}

	cases := []struct {
		name, path string
		want       int
	}{
		{"blocked task", "/api/v1/tasks/WL-2/claim", 409},
		{"draft task", "/api/v1/tasks/WL-3/claim", 422},
		{"unknown task", "/api/v1/tasks/WL-99/claim", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := doReq(t, h, "POST", tc.path, token, map[string]any{})
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d; body %s", rr.Code, tc.want, rr.Body.String())
			}
		})
	}
}

func TestRenewRelease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Work", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/renew", token, map[string]any{"ttl_seconds": 3600})
	if rr.Code != http.StatusOK {
		t.Fatalf("renew status = %d, body %s", rr.Code, rr.Body.String())
	}
	lease := decodeMap(t, rr)
	if lease["task_id"] != "WL-1" || lease["actor_id"] != "alice" {
		t.Fatalf("renew lease = %v", lease)
	}
	if s, _ := lease["expires_at"].(string); s == "" {
		t.Fatalf("renew expires_at missing: %v", lease)
	}

	// A non-holder cannot renew.
	bobToken := secondActor(t, st, "bob")
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/renew", bobToken, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("non-holder renew status = %d, want 404", rr.Code)
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/release", token, nil)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("release status = %d, body %s", rr.Code, rr.Body.String())
	}
	// Task went back to ready; lease is gone.
	rr = doReq(t, h, "GET", "/api/v1/tasks/WL-1", token, nil)
	if got := decodeMap(t, rr); got["state"] != "ready" {
		t.Fatalf("state after release = %v, want ready", got["state"])
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/renew", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("renew after release status = %d, want 404", rr.Code)
	}
}

func TestDone(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Ship it", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WL-1")

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/done", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("done status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["state"] != "done" {
		t.Fatalf("state = %v, want done", got["state"])
	}

	// The active lease was auto-released in the same transaction.
	if _, err := st.ActiveLease(context.Background(), "WL-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active lease after done: err = %v, want ErrNotFound", err)
	}
}

func TestDoneBadStates(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Not there yet", "priority": "high", "kind": "feature"})

	// From ready: 422.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/done", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("done from ready status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	// From in_progress: still 422 per the transition table.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d", rr.Code)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/done", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("done from in_progress status = %d, want 422", rr.Code)
	}
	// Unknown task: 404.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-99/done", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("done unknown task status = %d, want 404", rr.Code)
	}
}

func TestAbandon(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "From ready", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "From in_progress", "priority": "low", "kind": "chore"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Terminal", "priority": "low", "kind": "chore"})

	// From ready.
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/abandon", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("abandon from ready status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["state"] != "abandoned" {
		t.Fatalf("state = %v, want abandoned", got["state"])
	}

	// From in_progress (claimed): lease is auto-released.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/claim", token, map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d", rr.Code)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-2/abandon", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("abandon from in_progress status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["state"] != "abandoned" {
		t.Fatalf("state = %v, want abandoned", got["state"])
	}
	if _, err := st.ActiveLease(context.Background(), "WL-2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active lease after abandon: err = %v, want ErrNotFound", err)
	}

	// From done (terminal): 422.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-3/claim", token, map[string]any{})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d", rr.Code)
	}
	moveToReview(t, st, "WL-3")
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-3/done", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("done status = %d", rr.Code)
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-3/abandon", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("abandon from done status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// Unknown task: 404.
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-99/abandon", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("abandon unknown task status = %d, want 404", rr.Code)
	}
}
