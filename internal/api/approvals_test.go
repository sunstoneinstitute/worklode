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

// seedAwaitingPRApproval seeds one PR-kind approval in the 'awaiting' state
// behind a real task and project, so a Reviews queue reader has something to
// join against. entityID is repo#number, spelled the way store.PREntityID
// renders it (e.g. "acme/site#7"). Shared with the decide-route tests a
// later task adds.
func seedAwaitingPRApproval(t *testing.T, st *store.Store, entityID, title string) {
	t.Helper()

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
		task, err := store.CreateTask(tx, st.Now(), store.TaskInput{
			ProjectID: project, Title: title, Priority: "medium", Kind: "feature", CreatedBy: "alice",
		}, eventID)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	})

	now := st.Now()
	revision := fmt.Sprintf("seedsha%d", n)
	seedEvent(t, st, fmt.Sprintf("approval-seed-pr-%d", n), func(tx *sql.Tx, _ int64) error {
		if _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: number, Title: title, State: "open",
			HeadRef: taskID + "-approval-seed", HeadSHA: revision,
			URL:      fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
			OpenedAt: now,
		}, ""); err != nil {
			return err
		}
		return store.InsertAwaitingApproval(tx, now, "pr", store.PREntityID(repo, number), revision, nil, nil)
	})
}
