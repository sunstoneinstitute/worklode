package api_test

import (
	"context"
	"encoding/json"
	"net/http"
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

	issues, _ := st.ListIssues(context.Background(), "", "")
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
}
