package api_test

// inbox_page_test.go exercises GET /inbox (spec 056 §3) through the same
// newOIDCServer + webLogin session harness web_test.go's actor-tier tests
// use. It is a separate file from inbox_test.go (spec 020's /api/v1/inbox
// triage suite) and inbox_assemble_test.go (inbox.go's own package-internal
// pure-assembly tests) — same word, three different things.

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ibCreateTask creates one task via store.CreateTask directly (so createdBy
// and assignee can be set beyond what the HTTP create endpoint accepts),
// inside a recorded event exactly like every other seeding helper here.
func ibCreateTask(t *testing.T, st *store.Store, extID string, now time.Time, projectID, title, createdBy, assignee string) *model.Task {
	t.Helper()
	var task *model.Task
	_, _, err := st.RecordEvent(context.Background(), "cli", extID, "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = store.CreateTask(tx, now, store.TaskInput{
				ProjectID: projectID, Title: title, Priority: "medium", Kind: "feature", CreatedBy: createdBy,
			}, eventID)
			if err != nil {
				return err
			}
			if assignee == "" {
				return nil
			}
			return store.AssignTask(tx, now, task.ID, assignee, eventID)
		})
	if err != nil {
		t.Fatalf("create task %s: %v", title, err)
	}
	return task
}

// TestInboxPage seeds spec 056 §3.2's spread — an assigned review, an
// unassigned review in a led project, an owned PR, an assigned task, a
// created task, and a neighbour task — and checks a lead sees all six
// buckets, in the spec's fixed order, each linking the item it holds.
func TestInboxPage(t *testing.T) {
	t.Parallel()
	st, h, iss := newOIDCServer(t, api.Config{})
	createProject(t, st, "alpha")
	if err := st.CreateActor(context.Background(), "zed", "human", "Zed", false); err != nil {
		t.Fatalf("create actor zed: %v", err)
	}

	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	st.SetNowFunc(func() time.Time { return now })

	iss.TokenClaims = map[string]any{
		"preferred_username": "grace", "name": "Grace", "aud": iss.ClientID,
		"groups": []string{"user"}, "github_username": "grace-gh",
	}
	graceSession := webLogin(t, h, "grace")

	iss.TokenClaims = map[string]any{
		"preferred_username": "bob", "name": "Bob", "aud": iss.ClientID,
		"groups": []string{"user"},
	}
	bobSession := webLogin(t, h, "bob")

	// grace leads alpha; bob and zed are plain members (AssignTask requires
	// an assignee be on the task project's crew). addedBy "" stores NULL —
	// no acting actor needed for the seed.
	seedEvent(t, st, "crew", func(tx *sql.Tx, eventID int64) error {
		if err := store.AddParticipant(tx, now, "alpha", "grace", "engineer", true, false, "", eventID); err != nil {
			return err
		}
		if err := store.AddParticipant(tx, now, "alpha", "bob", "engineer", false, false, "", eventID); err != nil {
			return err
		}
		return store.AddParticipant(tx, now, "alpha", "zed", "engineer", false, false, "", eventID)
	})

	// A review's project comes from its PR's task_id, resolved from HeadRef
	// (store.TaskIDFromRef) — so every review PR below carries a HeadRef
	// naming this task, the same correlation TestListInboxReviews uses.
	reviewCtx := ibCreateTask(t, st, "review-ctx", now, "alpha", "Review context", "zed", "")

	// Bucket 1: a review required of grace.
	seedEvent(t, st, "pr-assigned", func(tx *sql.Tx, eventID int64) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "acme/alpha", Number: 1, Title: "Assigned PR", State: "open",
			HeadRef: reviewCtx.ID + "-assigned", HeadSHA: "sha-assigned",
			URL: "https://github.com/acme/alpha/pull/1", OpenedAt: now, Author: "zed-gh",
		}, ""); err != nil {
			return err
		}
		actor := "grace"
		return store.InsertAwaitingApproval(tx, now, "pr", store.PREntityID("acme/alpha", 1), "sha-assigned", nil, &actor)
	})

	// Bucket 2: an open review with no required actor, in a project grace
	// leads.
	seedEvent(t, st, "pr-unassigned", func(tx *sql.Tx, eventID int64) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "acme/alpha", Number: 2, Title: "Unassigned PR", State: "open",
			HeadRef: reviewCtx.ID + "-unassigned", HeadSHA: "sha-unassigned",
			URL: "https://github.com/acme/alpha/pull/2", OpenedAt: now, Author: "zed-gh",
		}, ""); err != nil {
			return err
		}
		return store.InsertAwaitingApproval(tx, now, "pr", store.PREntityID("acme/alpha", 2), "sha-unassigned", nil, nil)
	})

	// Bucket 3: grace's own pull request, required of bob.
	seedEvent(t, st, "pr-owned", func(tx *sql.Tx, eventID int64) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: "acme/alpha", Number: 3, Title: "Owned PR", State: "open",
			HeadRef: reviewCtx.ID + "-owned", HeadSHA: "sha-owned",
			URL: "https://github.com/acme/alpha/pull/3", OpenedAt: now, Author: "grace-gh",
		}, ""); err != nil {
			return err
		}
		actor := "bob"
		return store.InsertAwaitingApproval(tx, now, "pr", store.PREntityID("acme/alpha", 3), "sha-owned", nil, &actor)
	})

	assigned := ibCreateTask(t, st, "task-assigned", now, "alpha", "Assigned task", "zed", "grace")
	created := ibCreateTask(t, st, "task-created", now, "alpha", "Created task", "grace", "")
	neighbour := ibCreateTask(t, st, "task-neighbour", now, "alpha", "Neighbour task", "zed", "zed")

	rr := withSession(t, h, "GET", "/inbox", graceSession, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertNoAriaCurrent(t, body)
	bodyContains(t, body, "<h1>Inbox</h1>")

	for _, label := range []string{
		"Reviews assigned to you", "Unassigned reviews in projects you lead", "Reviews you own",
		"Work assigned to you", "Work you own", "Other in-progress work",
	} {
		heading := "<h3>" + label + "</h3>"
		if n := strings.Count(body, heading); n != 1 {
			t.Errorf("heading %q count = %d, want 1:\n%s", heading, n, body)
		}
	}
	assertOrder(t, body,
		"Reviews assigned to you", `href="https://github.com/acme/alpha/pull/1"`,
		"Unassigned reviews in projects you lead", `href="https://github.com/acme/alpha/pull/2"`,
		"Reviews you own", `href="https://github.com/acme/alpha/pull/3"`,
		"Work assigned to you", `href="/tasks/`+assigned.ID+`"`,
		"Work you own", `href="/tasks/`+created.ID+`"`,
		"Other in-progress work", `href="/tasks/`+neighbour.ID+`"`,
	)

	// bob is a plain member of alpha, not its lead: bucket 2 never reaches
	// him.
	rr = withSession(t, h, "GET", "/inbox", bobSession, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("bob status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	bobBody := rr.Body.String()
	if strings.Contains(bobBody, "Unassigned reviews in projects you lead") {
		t.Errorf("non-lead member sees the unassigned-review bucket:\n%s", bobBody)
	}
}

// TestInboxPageSignedOut covers the open-mode stack (newTestServer, no
// login): 200, the honest "nothing waiting" line, and no bucket heading —
// the page fetches nothing when there is no actor to fetch for.
func TestInboxPageSignedOut(t *testing.T) {
	t.Parallel()
	_, h, _ := newTestServer(t)

	rr := doReq(t, h, "GET", "/inbox", "", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	assertShell(t, body)
	assertNoAriaCurrent(t, body)
	bodyContains(t, body, "<h1>Inbox</h1>", "Nothing is waiting on you.")
	if strings.Contains(body, "<h3>") {
		t.Errorf("signed-out inbox rendered a bucket heading:\n%s", body)
	}
}
