package api_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// TestAssignDefaultsToCaller covers POST .../assign with no body: the caller
// becomes the assignee.
func TestAssignDefaultsToCaller(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Assign me", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["assignee"]; got != "alice" {
		t.Fatalf("assignee = %v, want alice", got)
	}
}

// TestAssignExplicitBody covers POST .../assign with an explicit assignee.
func TestAssignExplicitBody(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Assign to bob", "priority": "high", "kind": "feature",
	})
	secondActor(t, st, "bob")
	store.SeedCrewForTests(t, st, "proj", "bob")

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "bob"})
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["assignee"]; got != "bob" {
		t.Fatalf("assignee = %v, want bob", got)
	}
}

// TestUnassign covers POST .../unassign clearing the assignee.
func TestUnassign(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Unassign me", "priority": "high", "kind": "feature",
	})
	setup := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("setup assign status = %d, body %s", setup.Code, setup.Body.String())
	}
	if got := decodeMap(t, setup)["assignee"]; got != "alice" {
		t.Fatalf("setup assignee = %v, want alice", got)
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/unassign", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("unassign status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["assignee"]; got != "" {
		t.Fatalf("assignee = %v, want empty", got)
	}
}

// TestStartAutoAssignsNoLease covers POST .../start: the task moves to
// in_progress, auto-assigning the caller, without taking a lease row.
func TestStartAutoAssignsNoLease(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Start me", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["state"] != "in_progress" {
		t.Fatalf("state = %v, want in_progress", got["state"])
	}
	if got["assignee"] != "alice" {
		t.Fatalf("assignee = %v, want alice", got["assignee"])
	}
	if _, err := st.ActiveLease(context.Background(), "WL-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("active lease after start: err = %v, want ErrNotFound (no lease row)", err)
	}
}

// TestStartAssignedToAnother covers POST .../start on a task already assigned
// to someone else: 422.
func TestStartAssignedToAnother(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Bob's task", "priority": "high", "kind": "feature",
	})
	secondActor(t, st, "bob")
	store.SeedCrewForTests(t, st, "proj", "bob")
	setup := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, map[string]any{"assignee": "bob"})
	if setup.Code != http.StatusOK {
		t.Fatalf("setup assign status = %d, body %s", setup.Code, setup.Body.String())
	}

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("start status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "bob") {
		t.Fatalf("start 422 body = %q, want it to name the current assignee bob", body)
	}
}

// TestStop covers POST .../stop moving an assigned, lease-free in_progress
// task back to ready.
func TestStop(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Stop me", "priority": "high", "kind": "feature",
	})
	doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", token, nil)

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/stop", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["state"]; got != "ready" {
		t.Fatalf("state after stop = %v, want ready", got)
	}
}

// TestStopClaimedTask covers POST .../stop on a task held by an active
// lease: it must 422 through the lease guard, not the state/assignee guard,
// so the task is first assigned to the caller and then claimed (which
// re-transitions ready -> in_progress and takes a lease).
func TestStopClaimedTask(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Leased", "priority": "high", "kind": "feature",
	})
	setup := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("setup assign status = %d, body %s", setup.Code, setup.Body.String())
	}
	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/claim", token, map[string]any{"worktree": "host:/wt-1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/stop", token, nil)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stop status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); !strings.Contains(body, "held by an active lease") {
		t.Fatalf("stop 422 body = %q, want it to mention the lease guard", body)
	}
}

// TestPatchSubmitForReview covers PATCH state=in_review, legal only from
// in_progress.
func TestPatchSubmitForReview(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Submit me", "priority": "high", "kind": "feature",
	})

	// From ready: 422.
	rr := doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "in_review"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("patch from ready status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", token, nil)

	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "in_review"})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch from in_progress status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["state"]; got != "in_review" {
		t.Fatalf("state = %v, want in_review", got)
	}
}

// TestHumanLifecycle covers the full lease-free human path: assign -> start
// -> submit (PATCH in_review) -> done, ending merged.
func TestHumanLifecycle(t *testing.T) {
	t.Parallel()
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	store.SeedCrewForTests(t, st, "proj", "alice")
	createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "Full human lifecycle", "priority": "high", "kind": "feature",
	})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WL-1/assign", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("assign status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/start", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("start status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "PATCH", "/api/v1/tasks/WL-1", token, map[string]any{"state": "in_review"})
	if rr.Code != http.StatusOK {
		t.Fatalf("patch to in_review status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/tasks/WL-1/state", token, map[string]any{"state": "merged"})
	if rr.Code != http.StatusOK {
		t.Fatalf("done status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr)["state"]; got != "merged" {
		t.Fatalf("final state = %v, want merged", got)
	}
}
