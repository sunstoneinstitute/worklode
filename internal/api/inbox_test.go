package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestListInboxProjectFilter(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "alpha", "AL", "acme/alpha-app")
	mapRepo(t, h, token, "beta", "BE", "acme/beta-app")
	seedIssue(t, st, "acme/alpha-app", 1, "issue")
	seedIssue(t, st, "acme/beta-app", 2, "issue")

	rec := doReq(t, h, http.MethodGet, "/api/v1/inbox?project=alpha", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list inbox: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Issues []struct {
			Repo   string `json:"repo"`
			Number int64  `json:"number"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Issues) != 1 || resp.Issues[0].Repo != "acme/alpha-app" {
		t.Fatalf("project=alpha returned %+v; want only acme/alpha-app#1", resp.Issues)
	}
}

func TestLinkInbox(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	taskID := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "already tracked", "priority": "low", "kind": "bug",
	})["id"].(string)

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/link", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": taskID,
	})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}

	issues, err := st.ListIssues(context.Background(), "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if issues[0].TriageState != "promoted" || issues[0].TaskID == nil || *issues[0].TaskID != taskID {
		t.Fatalf("issue = %+v, want promoted and linked to %s", issues[0], taskID)
	}

	// Linking twice is a bad transition, not a silent overwrite.
	// mapStoreErr (internal/api/server.go:609) maps ErrBadTransition to 422.
	rr = doReq(t, h, http.MethodPost, "/api/v1/inbox/link", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": taskID,
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("second link status = %d, want 422", rr.Code)
	}
}

func TestLinkInboxUnknownTask(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/link", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "task_id": "PR-999",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s, want 404", rr.Code, rr.Body)
	}
	// Not just any 404: it must be the named pre-check in linkInbox
	// (internal/api/admin.go), not an anonymous 404 that LinkIssue's own
	// ErrNotFound would produce just as well.
	if !strings.Contains(rr.Body.String(), "task not found: PR-999") {
		t.Fatalf("body = %s, want it to name the missing task", rr.Body)
	}
}

func TestPromoteDraft(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "draft": true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["state"] != "draft" {
		t.Fatalf("state = %v, want draft — a bulk-promoted backlog must be stageable", got["state"])
	}
}

// TestPromoteRejectsInvalidKind pins the validKinds gate on promote and, with
// it, that a rejected promote writes nothing. There is no kind-specific rule
// beyond that gate.
func TestPromoteRejectsInvalidKind(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "saga",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rr.Code)
	}

	issues, err := st.ListIssues(context.Background(), "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if issues[0].TriageState != "new" || issues[0].TaskID != nil {
		t.Fatalf("issue = %+v, want unchanged — a rejected promote must not write anything", issues[0])
	}
}

func TestPromoteUnderParent(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	container := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "backlog", "priority": "low", "kind": "feature",
	})["id"].(string)

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "parent": container,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	parent, err := st.ParentOf(context.Background(), got["id"].(string))
	if err != nil {
		t.Fatalf("parent of promoted task: %v", err)
	}
	if parent == nil || parent.ID != container {
		t.Fatalf("parent = %v, want %s", parent, container)
	}
}

func TestPromoteUnknownParentIs404(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "parent": "PR-999",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	// Not just any 404: it must be the named pre-check in promoteInbox
	// (internal/api/admin.go), not an anonymous 404 that AddEdge's own
	// ErrNotFound would produce just as well.
	if !strings.Contains(rr.Body.String(), "parent not found: PR-999") {
		t.Fatalf("body = %s, want it to name the missing parent", rr.Body)
	}

	issues, err := st.ListIssues(context.Background(), "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if issues[0].TriageState != "new" || issues[0].TaskID != nil {
		t.Fatalf("issue = %+v, want unchanged — a 404'd promote must not write anything", issues[0])
	}
}

// TestPromoteUnderOrdinaryParent pins 004 §6.1 on the promote path: any
// ordinary task may be a parent, since checkHierarchy requires no particular
// kind, so promoting under one is filed rather than rejected. The remaining
// spec-004 invariants (one project, one parent, no cycle, depth cap) still
// reject through the ErrInvalidInput -> 422 mapping.
func TestPromoteUnderOrdinaryParent(t *testing.T) {
	st, h, token := newTestServer(t)
	mapRepo(t, h, token, "proj", "PR", "acme/widgets")
	seedIssue(t, st, "acme/widgets", 1, "an issue")

	plain := createTaskViaAPI(t, h, token, map[string]any{
		"project": "proj", "title": "an ordinary task", "priority": "low", "kind": "bug",
	})["id"].(string)

	rr := doReq(t, h, http.MethodPost, "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "low", "kind": "bug", "parent": plain,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 under ordinary parent %s; body %s", rr.Code, plain, rr.Body.String())
	}

	issues, err := st.ListIssues(context.Background(), "", "")
	if err != nil {
		t.Fatalf("list issues: %v", err)
	}
	if issues[0].TriageState == "new" || issues[0].TaskID == nil {
		t.Fatalf("issue = %+v, want promoted and linked to the new task", issues[0])
	}

	detail := doReq(t, h, http.MethodGet, "/api/v1/tasks/"+*issues[0].TaskID, token, nil)
	hier := decodeMap(t, detail)["hierarchy"].(map[string]any)
	parent := hier["parent"].(map[string]any)
	if parent["id"] != plain {
		t.Fatalf("parent = %v, want %s", parent["id"], plain)
	}
}
