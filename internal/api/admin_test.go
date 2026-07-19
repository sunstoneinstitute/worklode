package api_test

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

func TestCreateAndListProjects(t *testing.T) {
	_, h, token := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/projects", token, map[string]any{
		"id": "proj", "name": "Project", "deploy_gated": true,
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create project status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["id"] != "proj" || got["name"] != "Project" || got["deploy_gated"] != true {
		t.Fatalf("create project body = %v", got)
	}
	if repos, ok := got["repos"].([]any); !ok || len(repos) != 0 {
		t.Fatalf("create project repos = %v, want empty array", got["repos"])
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add repo status = %d, body %s", rr.Code, rr.Body.String())
	}

	// Adding the same repo again (any project) is a conflict.
	rr = doReq(t, h, "POST", "/api/v1/projects/proj/repos", token, map[string]any{"repo": "acme/widgets"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("re-add repo status = %d, want 409; body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/projects/nosuch/repos", token, map[string]any{"repo": "acme/other"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("add repo to unknown project status = %d, want 404", rr.Code)
	}

	rr = doReq(t, h, "GET", "/api/v1/projects", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list projects status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			DeployGated bool     `json:"deploy_gated"`
			Repos       []string `json:"repos"`
		} `json:"projects"`
	}
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 {
		t.Fatalf("projects = %v, want 1", body.Projects)
	}
	p := body.Projects[0]
	if p.ID != "proj" || !p.DeployGated || len(p.Repos) != 1 || p.Repos[0] != "acme/widgets" {
		t.Fatalf("project = %+v", p)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	for name, body := range map[string]map[string]any{
		"missing id":   {"name": "n"},
		"missing name": {"id": "i"},
	} {
		t.Run(name, func(t *testing.T) {
			rr := doReq(t, h, "POST", "/api/v1/projects", token, body)
			if rr.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestCreateActorAndTokenLifecycle(t *testing.T) {
	_, h, token := newTestServer(t)

	rr := doReq(t, h, "POST", "/api/v1/actors", token, map[string]any{
		"id": "bob", "kind": "agent", "display_name": "Bob",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create actor status = %d, body %s", rr.Code, rr.Body.String())
	}
	if got := decodeMap(t, rr); got["id"] != "bob" || got["kind"] != "agent" {
		t.Fatalf("create actor body = %v", got)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors", token, map[string]any{"id": "x", "kind": "nonsense"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad kind status = %d, want 422", rr.Code)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors/bob/tokens", token, map[string]any{"description": "bob's token"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create token status = %d, body %s", rr.Code, rr.Body.String())
	}
	tok, _ := decodeMap(t, rr)["token"].(string)
	if !strings.HasPrefix(tok, "wt_") {
		t.Fatalf("token = %q, want wt_ prefix", tok)
	}

	rr = doReq(t, h, "POST", "/api/v1/actors/nosuch/tokens", token, map[string]any{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("token for unknown actor status = %d, want 404", rr.Code)
	}

	// The new token authenticates.
	rr = doReq(t, h, "GET", "/api/v1/tasks", tok, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("auth with new token status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "DELETE", "/api/v1/tokens", token, map[string]any{"token": tok})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "GET", "/api/v1/tasks", tok, nil)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth with revoked token status = %d, want 401", rr.Code)
	}

	rr = doReq(t, h, "DELETE", "/api/v1/tokens", token, map[string]any{"token": tok})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("revoke already-revoked status = %d, want 404", rr.Code)
	}
}

func TestInboxListPromoteDismiss(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	if err := st.AddRepo(context.Background(), "proj", "acme/widgets"); err != nil {
		t.Fatalf("add repo: %v", err)
	}
	seedIssue(t, st, "acme/widgets", 1, "Fix the frobnicator")

	rr := doReq(t, h, "GET", "/api/v1/inbox", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list inbox status = %d, body %s", rr.Code, rr.Body.String())
	}
	var listBody struct {
		Issues []struct {
			Repo        string `json:"repo"`
			Number      int64  `json:"number"`
			Title       string `json:"title"`
			TriageState string `json:"triage_state"`
		} `json:"issues"`
	}
	decodeInto(t, rr, &listBody)
	if len(listBody.Issues) != 1 || listBody.Issues[0].TriageState != "new" {
		t.Fatalf("issues = %+v", listBody.Issues)
	}

	rr = doReq(t, h, "GET", "/api/v1/inbox?state=promoted", token, nil)
	decodeInto(t, rr, &listBody)
	if len(listBody.Issues) != 0 {
		t.Fatalf("promoted issues before promote = %+v, want none", listBody.Issues)
	}

	// Promote without a title: defaults to the issue's title.
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "high", "kind": "bug",
		"applies_to_versions": []string{"v1.2"},
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("promote status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	if got["title"] != "Fix the frobnicator" || got["project"] != "proj" || got["priority"] != "high" {
		t.Fatalf("promoted task = %v", got)
	}
	if got["id"] != "WT-1" {
		t.Fatalf("promoted task id = %v, want WT-1", got["id"])
	}

	// A second promote of the same issue fails: no longer 'new'.
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "high", "kind": "bug",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-promote status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}

	// Unmapped repo -> 404.
	seedIssue(t, st, "acme/unmapped", 5, "Orphan issue")
	rr = doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/unmapped", "number": 5, "priority": "low", "kind": "chore",
	})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("promote unmapped repo status = %d, want 404; body %s", rr.Code, rr.Body.String())
	}

	// Dismiss a fresh issue.
	seedIssue(t, st, "acme/widgets", 2, "Not worth doing")
	rr = doReq(t, h, "POST", "/api/v1/inbox/dismiss", token, map[string]any{"repo": "acme/widgets", "number": 2})
	if rr.Code != http.StatusNoContent {
		t.Fatalf("dismiss status = %d, body %s", rr.Code, rr.Body.String())
	}
	rr = doReq(t, h, "POST", "/api/v1/inbox/dismiss", token, map[string]any{"repo": "acme/widgets", "number": 2})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("re-dismiss status = %d, want 422; body %s", rr.Code, rr.Body.String())
	}
}

func TestInboxPromoteValidation(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "POST", "/api/v1/inbox/promote", token, map[string]any{
		"repo": "acme/widgets", "number": 1, "priority": "nonsense", "kind": "bug",
	})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad priority status = %d, want 422", rr.Code)
	}
}

func TestBoard(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")

	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Ready one", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Will be blocked", "priority": "high", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Claimed", "priority": "medium", "kind": "feature"})
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "In review", "priority": "low", "kind": "chore"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WT-1/edges", token, map[string]any{"to": "WT-2", "type": "blocks"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("add blocking edge status = %d", rr.Code)
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WT-3/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WT-3 status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WT-4/claim", token, map[string]any{"session_id": "s2"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim WT-4 status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WT-4")

	rr = doReq(t, h, "GET", "/api/v1/board?project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			ID         string `json:"id"`
			InProgress []struct {
				ID     string `json:"id"`
				Holder *struct {
					ActorID   string `json:"actor_id"`
					ExpiresAt string `json:"expires_at"`
				} `json:"holder"`
			} `json:"in_progress"`
			InReview []struct {
				ID string `json:"id"`
			} `json:"in_review"`
			Ready []struct {
				ID string `json:"id"`
			} `json:"ready"`
			Blocked []struct {
				ID string `json:"id"`
			} `json:"blocked"`
		} `json:"projects"`
		RecentFailures []any `json:"recent_failures"`
	}
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 {
		t.Fatalf("projects = %+v", body.Projects)
	}
	p := body.Projects[0]
	if len(p.Ready) != 1 || p.Ready[0].ID != "WT-1" {
		t.Fatalf("ready = %+v", p.Ready)
	}
	if len(p.Blocked) != 1 || p.Blocked[0].ID != "WT-2" {
		t.Fatalf("blocked = %+v", p.Blocked)
	}
	if len(p.InProgress) != 1 || p.InProgress[0].ID != "WT-3" {
		t.Fatalf("in_progress = %+v", p.InProgress)
	}
	if p.InProgress[0].Holder == nil || p.InProgress[0].Holder.ActorID != "alice" {
		t.Fatalf("in_progress holder = %+v", p.InProgress[0].Holder)
	}
	if len(p.InReview) != 1 || p.InReview[0].ID != "WT-4" {
		t.Fatalf("in_review = %+v", p.InReview)
	}
	// project filter set -> recent_failures omitted (nil, not empty array).
	if body.RecentFailures != nil {
		t.Fatalf("recent_failures with project filter = %v, want omitted", body.RecentFailures)
	}

	// No project filter: recent_failures included (possibly empty).
	rr = doReq(t, h, "GET", "/api/v1/board", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board (all) status = %d, body %s", rr.Code, rr.Body.String())
	}
	var allBody struct {
		Projects       []any `json:"projects"`
		RecentFailures []any `json:"recent_failures"`
	}
	decodeInto(t, rr, &allBody)
	if allBody.RecentFailures == nil {
		t.Fatalf("recent_failures without project filter = nil, want present (possibly empty)")
	}
}

// TestBoardInProgressWithoutLease covers the board's lease-lookup error
// handling: an in_progress task with no active lease (in_review ->
// in_progress via the review flow, no claim) must render with no holder, not
// fail. The other half of that handling — a real DB error surfacing as 500
// via mapStoreErr — is impractical to force through the public API and is
// covered by code-path match with getTask.
func TestBoardInProgressWithoutLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Reopened", "priority": "high", "kind": "bug"})

	rr := doReq(t, h, "POST", "/api/v1/tasks/WT-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}
	moveToReview(t, st, "WT-1")
	// Review sends it back to in_progress; the original lease was not renewed.
	_, _, err := st.RecordEvent(context.Background(), "github", "reopen-WT-1", "task.reopened", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := st.Now()
			if err := store.CloseActiveLease(tx, now, "WT-1"); err != nil {
				return err
			}
			return store.Transition(tx, now, "WT-1", "in_review", "in_progress", eventID)
		})
	if err != nil {
		t.Fatalf("move WT-1 back to in_progress without lease: %v", err)
	}

	rr = doReq(t, h, "GET", "/api/v1/board?project=proj", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("board status = %d, body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		Projects []struct {
			InProgress []struct {
				ID     string `json:"id"`
				Holder *struct {
					ActorID string `json:"actor_id"`
				} `json:"holder"`
			} `json:"in_progress"`
		} `json:"projects"`
	}
	decodeInto(t, rr, &body)
	if len(body.Projects) != 1 || len(body.Projects[0].InProgress) != 1 {
		t.Fatalf("board = %+v", body.Projects)
	}
	ip := body.Projects[0].InProgress[0]
	if ip.ID != "WT-1" || ip.Holder != nil {
		t.Fatalf("in_progress = %+v, want WT-1 with no holder", ip)
	}
}

func TestBoardUnknownProject(t *testing.T) {
	_, h, token := newTestServer(t)
	rr := doReq(t, h, "GET", "/api/v1/board?project=nosuch", token, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestGetTaskIncludesLease(t *testing.T) {
	st, h, token := newTestServer(t)
	createProject(t, st, "proj")
	createTaskViaAPI(t, h, token, map[string]any{"project": "proj", "title": "Leased", "priority": "high", "kind": "feature"})

	rr := doReq(t, h, "GET", "/api/v1/tasks/WT-1", token, nil)
	if got := decodeMap(t, rr)["lease"]; got != nil {
		t.Fatalf("lease before claim = %v, want absent", got)
	}

	rr = doReq(t, h, "POST", "/api/v1/tasks/WT-1/claim", token, map[string]any{"session_id": "s1"})
	if rr.Code != http.StatusOK {
		t.Fatalf("claim status = %d, body %s", rr.Code, rr.Body.String())
	}

	rr = doReq(t, h, "GET", "/api/v1/tasks/WT-1", token, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("get task status = %d, body %s", rr.Code, rr.Body.String())
	}
	got := decodeMap(t, rr)
	lease, ok := got["lease"].(map[string]any)
	if !ok {
		t.Fatalf("lease after claim missing: %v", got)
	}
	if lease["actor_id"] != "alice" || lease["task_id"] != "WT-1" {
		t.Fatalf("lease = %v", lease)
	}
}

// seedIssue inserts a fresh (triage_state='new') inbox issue directly via
// the store, as a webhook delivery would.
func seedIssue(t *testing.T, st *store.Store, repo string, number int64, title string) {
	t.Helper()
	err := st.Tx(context.Background(), func(tx *sql.Tx) error {
		return store.UpsertIssue(tx, store.Issue{
			Repo: repo, Number: number, Title: title, State: "open",
			URL: "https://github.com/" + repo + "/issues/" + strconv.FormatInt(number, 10),
		})
	})
	if err != nil {
		t.Fatalf("seed issue %s#%d: %v", repo, number, err)
	}
}
