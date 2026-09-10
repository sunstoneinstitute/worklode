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
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: number, Title: title, State: "open",
			HeadRef: taskID + "-approval-seed", HeadSHA: revision,
			URL:      fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
			OpenedAt: now, Author: seed.Author,
		}, ""); err != nil {
			return err
		}
		_, err := store.InsertAwaitingApproval(tx, now, "pr",
			store.PREntityID(repo, number), revision, "", requiredRole, nil, nil)
		return err
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

// seedAwaitingApprovalRow inserts one awaiting approval of any kind and
// returns its id, read back through the queue reader for the same reason
// seedPRApproval does: the row it returns is the row /reviews shows.
func seedAwaitingApprovalRow(t *testing.T, st *store.Store,
	kind, entityID, revision, lane string) int64 {
	t.Helper()
	n := approvalSeedSeq.Add(1)
	seedEvent(t, st, fmt.Sprintf("approval-seed-row-%d", n), func(tx *sql.Tx, _ int64) error {
		_, err := store.InsertAwaitingApproval(tx, st.Now(), kind, entityID,
			revision, lane, nil, nil, nil)
		return err
	})
	rows, err := st.ListAwaitingApprovals(context.Background())
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	for _, row := range rows {
		if row.EntityKind == kind && row.EntityID == entityID && row.Lane == lane {
			return row.ID
		}
	}
	t.Fatalf("seeded %s approval %s is not in the awaiting queue", kind, entityID)
	return 0
}

// seedDecidedThenCandidate seeds one pr-kind entity with a settled history:
// decidedRev already approved (by actor "alice", the admin newTestServer
// always seeds), then designated forward to candidateRev the way a new push
// really moves it (store.DesignateRevision, not a hand-rolled second insert)
// — so the approval detail page's decision history and compare link are
// exercised against the real designation flow, not a shortcut that merely
// looks like it. Returns the candidate row's id, the one /approvals/{id}
// shows as still awaiting.
func seedDecidedThenCandidate(t *testing.T, st *store.Store, entityID, decidedRev, candidateRev string) int64 {
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
	if err := st.CreateProject(context.Background(), project, project,
		fmt.Sprintf("AQ%d", n)); err != nil {
		t.Fatalf("create project %s: %v", project, err)
	}

	var taskID string
	seedEvent(t, st, fmt.Sprintf("approval-seed-task-%d", n), func(tx *sql.Tx, eventID int64) error {
		task, err := store.CreateTask(tx, st.Now(), store.TaskInput{
			ProjectID: project, Title: "seed pr", Priority: "medium", Kind: "feature",
		}, eventID)
		if err != nil {
			return err
		}
		taskID = task.ID
		return nil
	})

	now := st.Now()
	seedEvent(t, st, fmt.Sprintf("approval-seed-pr-%d", n), func(tx *sql.Tx, _ int64) error {
		if _, _, err := store.UpsertPR(tx, store.PullRequest{
			Repo: repo, Number: number, Title: "seed pr", State: "open",
			HeadRef: taskID + "-approval-seed", HeadSHA: candidateRev,
			URL:      fmt.Sprintf("https://github.com/%s/pull/%d", repo, number),
			OpenedAt: now,
		}, ""); err != nil {
			return err
		}
		if _, err := store.InsertAwaitingApproval(tx, now, "pr", entityID,
			decidedRev, "", nil, nil, nil); err != nil {
			return err
		}
		decided, err := store.OpenApprovalForLane(tx, "pr", entityID, "")
		if err != nil {
			return err
		}
		actor := "alice"
		if err := store.ResolveApproval(tx, decided.ID, "approved", &actor, now); err != nil {
			return err
		}
		_, _, err = store.DesignateRevision(tx, now, "pr", entityID, candidateRev)
		return err
	})

	rows, err := st.ListAwaitingApprovals(context.Background())
	if err != nil {
		t.Fatalf("list awaiting approvals: %v", err)
	}
	for _, row := range rows {
		if row.EntityID == entityID && row.SubjectRevision == candidateRev {
			return row.ID
		}
	}
	t.Fatalf("seeded candidate approval %s@%s is not in the awaiting queue", entityID, candidateRev)
	return 0
}

// seedAwaitingDeliverableLane seeds a deliverable-kind approval on a lane
// with no designated revision (029 §7.2): the flow requires a decision, the
// subject it is granted against has not been named yet.
func seedAwaitingDeliverableLane(t *testing.T, st *store.Store, name, lane string) seededApproval {
	t.Helper()
	return seedDeliverableApproval(t, st, name, lane, "")
}

// seedDeliverableApproval seeds one deliverable behind its own project and an
// awaiting approval on it. The deliverable declares no URL, so the queue
// resolves its address to the project's deliverables page.
func seedDeliverableApproval(t *testing.T, st *store.Store,
	name, lane, revision string) seededApproval {
	t.Helper()
	n := approvalSeedSeq.Add(1)
	project := fmt.Sprintf("approval-seed-%d", n)
	if err := st.CreateProject(context.Background(), project, project,
		fmt.Sprintf("AQ%d", n)); err != nil {
		t.Fatalf("create project %s: %v", project, err)
	}
	var delID string
	seedEvent(t, st, fmt.Sprintf("approval-seed-del-%d", n), func(tx *sql.Tx, _ int64) error {
		del, err := store.CreateDeliverable(tx, st.Now(), store.DeliverableInput{
			ProjectID: project, Name: name,
		})
		if err != nil {
			return err
		}
		delID = del.ID
		return nil
	})
	return seededApproval{
		ID:        seedAwaitingApprovalRow(t, st, "deliverable", delID, revision, lane),
		ProjectID: project,
	}
}
