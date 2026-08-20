package api_test

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// approvalSeedSeq gives each seedAwaitingPRApproval call its own project and
// task, so two calls in the same test never collide creating a project.
var approvalSeedSeq atomic.Int64

// prApprovalSeed describes one seeded approval. EntityID is repo#number,
// spelled the way store.PREntityID renders it (e.g. "acme/site#7"). Author
// and RequiredRole are optional: "" leaves the corresponding column NULL,
// which is what the self-approval and role checks must treat as "cannot
// refuse" and "everyone qualifies".
type prApprovalSeed struct {
	EntityID     string
	Title        string
	Author       string
	RequiredRole string
}

// seededApproval is what a seeded row's tests need to address it: the
// approvals id the decide route takes, and the task and project it hangs off.
type seededApproval struct {
	ID        int64
	TaskID    string
	ProjectID string
}

// seedAwaitingPRApproval seeds one plain awaiting approval — no PR author, no
// required role — for the tests that only need a row in the queue.
func seedAwaitingPRApproval(t *testing.T, st *store.Store, entityID, title string) seededApproval {
	t.Helper()
	return seedPRApproval(t, st, prApprovalSeed{EntityID: entityID, Title: title})
}

// seedPRApproval seeds one PR-kind approval in the 'awaiting' state behind a
// real task and project, so a Reviews queue reader has something to join
// against and the decide route has something to resolve.
func seedPRApproval(t *testing.T, st *store.Store, seed prApprovalSeed) seededApproval {
	t.Helper()
	entityID, title := seed.EntityID, seed.Title

	i := strings.LastIndex(entityID, "#")
	if i < 0 {
		t.Fatalf("entity id %q is not repo#number", entityID)
	}
	repo := entityID[:i]
	number, err := strconv.ParseInt(entityID[i+1:], 10, 64)
	if err != nil {
		t.Fatalf("entity id %q: %v", entityID, err)
	}

	n := approvalSeedSeq.Add(1)
	project := fmt.Sprintf("approval-seed-%d", n)
	key := fmt.Sprintf("AQ%d", n)
	if err := st.CreateProject(context.Background(), project, project, key); err != nil {
		t.Fatalf("create project %s: %v", project, err)
	}

	var taskID string
	seedEvent(t, st, fmt.Sprintf("approval-seed-task-%d", n), func(tx *sql.Tx, eventID int64) error {
		// No CreatedBy: the seed must work on any store, including one whose
		// only actors are the ones a test logs in as.
		task, err := store.CreateTask(tx, st.Now(), store.TaskInput{
			ProjectID: project, Title: title, Priority: "medium", Kind: "feature",
		}, eventID)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	})

	now := st.Now()
	revision := fmt.Sprintf("seedsha%d", n)
	var requiredRole *string
	if seed.RequiredRole != "" {
		requiredRole = &seed.RequiredRole
	}
	seedEvent(t, st, fmt.Sprintf("approval-seed-pr-%d", n), func(tx *sql.Tx, _ int64) error {
		if _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: number, Title: title, State: "open",
			HeadRef: taskID + "-approval-seed", HeadSHA: revision,
			URL:      fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
			OpenedAt: now, Author: seed.Author,
		}, ""); err != nil {
			return err
		}
		return store.InsertAwaitingApproval(tx, now, "pr",
			store.PREntityID(repo, number), revision, requiredRole, nil)
	})

	// Read the id back through the queue reader rather than a hand-rolled
	// query: the seed then proves the row it returns is the one /reviews
	// shows, which is the row the decide form posts against.
	rows, err := st.ListAwaitingApprovals(context.Background())
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	for _, row := range rows {
		if row.EntityID == store.PREntityID(repo, number) {
			return seededApproval{ID: row.ID, TaskID: taskID, ProjectID: project}
		}
	}
	t.Fatalf("seeded approval %s is not in the awaiting queue", entityID)
	return seededApproval{}
}
