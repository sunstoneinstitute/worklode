package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// extSeq feeds unique external ids so every RecordEvent call in the tests is
// a distinct event (idempotency never kicks in by accident).
var extSeq atomic.Int64

func nextExt(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), extSeq.Add(1))
}

// openTaskStore opens a test store with the fixtures task tests need: a
// project ("horndb") and an actor ("stig").
func openTaskStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "horndb", "HornDB", "HDB"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := s.CreateActor(ctx, "stig", "human", "Stig", false); err != nil {
		t.Fatalf("CreateActor: %v", err)
	}
	return s
}

var taskTestNow = time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)

func defaultTaskInput() TaskInput {
	return TaskInput{
		ProjectID: "horndb",
		Title:     "a task",
		Body:      "body",
		Priority:  "medium",
		Kind:      "feature",
		CreatedBy: "stig",
	}
}

// createTask drives CreateTask through RecordEvent, the way production code
// will use it.
func createTask(t *testing.T, s *Store, now time.Time, in TaskInput) *Task {
	t.Helper()
	var task *Task
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			task, err = CreateTask(tx, now, in)
			return err
		})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task
}

// transition drives Transition through RecordEvent and returns its error.
func transition(t *testing.T, s *Store, now time.Time, taskID, from, to string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, eventID int64) error {
			return Transition(tx, now, taskID, from, to, eventID)
		})
	return err
}

// walkTo moves a task (created in "ready") to the given state via legal
// transitions only.
func walkTo(t *testing.T, s *Store, taskID, state string) {
	t.Helper()
	paths := map[string][]string{
		"ready":         {},
		"in_progress":   {"in_progress"},
		"in_review":     {"in_progress", "in_review"},
		"merged":        {"in_progress", "in_review", "merged"},
		"deployed_dev":  {"in_progress", "in_review", "merged", "deployed_dev"},
		"deployed_prod": {"in_progress", "in_review", "merged", "deployed_dev", "deployed_prod"},
		"released":      {"in_progress", "in_review", "merged", "released"},
		"abandoned":     {"abandoned"},
	}
	steps, ok := paths[state]
	if !ok {
		t.Fatalf("walkTo: no path to state %q", state)
	}
	cur := "ready"
	for _, next := range steps {
		if err := transition(t, s, taskTestNow, taskID, cur, next); err != nil {
			t.Fatalf("walkTo %s: transition %s -> %s: %v", state, cur, next, err)
		}
		cur = next
	}
}

func addEdge(t *testing.T, s *Store, fromTask, toTask, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "edge.add", nil,
		func(tx *sql.Tx, eventID int64) error {
			return AddEdge(tx, taskTestNow, fromTask, toTask, typ)
		})
	return err
}

func removeEdge(t *testing.T, s *Store, fromTask, toTask, typ string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "edge.remove", nil,
		func(tx *sql.Tx, eventID int64) error {
			return RemoveEdge(tx, fromTask, toTask, typ)
		})
	return err
}

func isBlocked(t *testing.T, s *Store, taskID string) bool {
	t.Helper()
	var blocked bool
	err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var err error
		blocked, err = IsBlocked(tx, taskID)
		return err
	})
	if err != nil {
		t.Fatalf("IsBlocked(%s): %v", taskID, err)
	}
	return blocked
}

func TestCreateTaskSequentialIDsAndDefaults(t *testing.T) {
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	if t1.ID != "HDB-1" {
		t.Fatalf("first task id: got %q, want HDB-1", t1.ID)
	}
	if t1.State != "ready" {
		t.Fatalf("first task state: got %q, want ready", t1.State)
	}

	in2 := defaultTaskInput()
	in2.Draft = true
	t2 := createTask(t, s, taskTestNow, in2)
	if t2.ID != "HDB-2" {
		t.Fatalf("second task id: got %q, want HDB-2", t2.ID)
	}
	if t2.State != "draft" {
		t.Fatalf("draft task state: got %q, want draft", t2.State)
	}

	// Round-trip through GetTask matches what CreateTask returned.
	got, err := s.GetTask(t.Context(), "HDB-1")
	if err != nil {
		t.Fatalf("GetTask HDB-1: %v", err)
	}
	if !reflect.DeepEqual(got, t1) {
		t.Fatalf("GetTask: got %+v, want %+v", got, t1)
	}
	if got.ProjectID != "horndb" || got.Title != "a task" || got.Body != "body" ||
		got.Priority != "medium" || got.Kind != "feature" || got.CreatedBy != "stig" {
		t.Fatalf("GetTask fields: got %+v", got)
	}
	if !got.CreatedAt.Equal(taskTestNow) || !got.UpdatedAt.Equal(taskTestNow) {
		t.Fatalf("GetTask timestamps: got created=%v updated=%v, want %v", got.CreatedAt, got.UpdatedAt, taskTestNow)
	}
}

func TestTransitionLegal(t *testing.T) {
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "ready"},
		{"ready", "in_progress"},
		{"in_progress", "in_review"},
		{"in_progress", "ready"},
		{"in_review", "in_progress"},
		{"ready", "merged"},
		{"in_progress", "merged"},
		{"in_review", "merged"},
		{"merged", "deployed_dev"},
		{"merged", "deployed_prod"},
		{"merged", "released"},
		{"deployed_dev", "deployed_prod"},
		{"deployed_dev", "released"},
		{"draft", "abandoned"},
		{"ready", "abandoned"},
		{"in_progress", "abandoned"},
		{"in_review", "abandoned"},
		{"merged", "ready"},
		{"deployed_dev", "ready"},
		{"deployed_prod", "ready"},
		{"released", "ready"},
		{"abandoned", "ready"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		if err := transition(t, s, taskTestNow, task.ID, c.from, c.to); err != nil {
			t.Fatalf("transition %s -> %s: %v", c.from, c.to, err)
		}
		got, err := s.GetTask(t.Context(), task.ID)
		if err != nil {
			t.Fatalf("GetTask %s: %v", task.ID, err)
		}
		if got.State != c.to {
			t.Fatalf("after %s -> %s: state is %q", c.from, c.to, got.State)
		}
	}
}

func TestTransitionIllegal(t *testing.T) {
	s := openTaskStore(t)

	cases := []struct{ from, to string }{
		{"draft", "merged"},
		{"draft", "in_progress"},
		{"merged", "abandoned"},
		{"released", "deployed_dev"},
		{"abandoned", "merged"},
		{"abandoned", "in_progress"},
	}
	for _, c := range cases {
		in := defaultTaskInput()
		in.Draft = c.from == "draft"
		task := createTask(t, s, taskTestNow, in)
		if !in.Draft {
			walkTo(t, s, task.ID, c.from)
		}
		err := transition(t, s, taskTestNow, task.ID, c.from, c.to)
		if !errors.Is(err, ErrBadTransition) {
			t.Fatalf("transition %s -> %s: want ErrBadTransition, got %v", c.from, c.to, err)
		}
	}
}

func TestTransitionWrongCurrentState(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput()) // state: ready
	err := transition(t, s, taskTestNow, task.ID, "in_progress", "in_review")
	if !errors.Is(err, ErrBadTransition) {
		t.Fatalf("transition with wrong from: want ErrBadTransition, got %v", err)
	}
	// The task is untouched.
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state after failed transition: got %q, want ready", got.State)
	}
}

func TestTransitionUnknownTask(t *testing.T) {
	s := openTaskStore(t)

	err := transition(t, s, taskTestNow, "HDB-999", "ready", "in_progress")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("transition unknown task: want ErrNotFound, got %v", err)
	}
}

func TestTransitionWritesStateLogAndBumpsUpdatedAt(t *testing.T) {
	s := openTaskStore(t)

	created := taskTestNow
	moved := taskTestNow.Add(5 * time.Minute)

	task := createTask(t, s, created, defaultTaskInput())

	var eventID int64
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.transition", nil,
		func(tx *sql.Tx, evID int64) error {
			eventID = evID
			return Transition(tx, moved, task.ID, "ready", "in_progress", evID)
		})
	if err != nil {
		t.Fatalf("transition: %v", err)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("CreatedAt: got %v, want %v", got.CreatedAt, created)
	}
	if !got.UpdatedAt.Equal(moved) {
		t.Fatalf("UpdatedAt: got %v, want %v (bumped)", got.UpdatedAt, moved)
	}

	var kind, entityID, changeJSON string
	var loggedEventID int64
	row := s.db.QueryRow(
		`SELECT entity_kind, entity_id, change, event_id FROM state_log WHERE entity_id = $1`, task.ID)
	if err := row.Scan(&kind, &entityID, &changeJSON, &loggedEventID); err != nil {
		t.Fatalf("read state_log: %v", err)
	}
	if kind != "task" || entityID != task.ID || loggedEventID != eventID {
		t.Fatalf("state_log row: kind=%q entity=%q event_id=%d, want task/%s/%d",
			kind, entityID, loggedEventID, task.ID, eventID)
	}
	var change map[string]string
	if err := json.Unmarshal([]byte(changeJSON), &change); err != nil {
		t.Fatalf("unmarshal change %q: %v", changeJSON, err)
	}
	want := map[string]string{"field": "state", "old": "ready", "new": "in_progress"}
	if !reflect.DeepEqual(change, want) {
		t.Fatalf("state_log change: got %v, want %v", change, want)
	}
}

func TestBlocksEdgeAndBlockedTaskIDs(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	blocker := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-1
	blocked := createTask(t, s, taskTestNow, defaultTaskInput()) // HDB-2

	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}

	// Blocked while the blocker is ready.
	ids, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] || ids[blocker.ID] {
		t.Fatalf("BlockedTaskIDs with blocker ready: got %v, want only %s", ids, blocked.ID)
	}
	if !isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want true while blocker ready", blocked.ID)
	}

	// Still blocked while the blocker is in_progress.
	walkTo(t, s, blocker.ID, "in_progress")
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if !ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker in_progress: %s missing from %v", blocked.ID, ids)
	}

	// Unblocked once the blocker is merged (legal walk: in_review then merged).
	if err := transition(t, s, taskTestNow, blocker.ID, "in_progress", "in_review"); err != nil {
		t.Fatalf("transition to in_review: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "in_review", "merged"); err != nil {
		t.Fatalf("transition to merged: %v", err)
	}
	ids, err = s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs with blocker merged: %s should be unblocked, got %v", blocked.ID, ids)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked(%s): want false after blocker merged", blocked.ID)
	}
}

// TestBlockedTaskIDsDeliveredBlocker pins taskClosed for a blocker with no commit
// attribution: it gates on DefaultDoneState, so every state from merged onward
// leaves it unblocking. Narrowing that back to merged-or-abandoned would make
// these dependents block again.
func TestBlockedTaskIDsDeliveredBlocker(t *testing.T) {
	for _, state := range []string{"deployed_dev", "deployed_prod", "released"} {
		t.Run(state, func(t *testing.T) {
			s := openTaskStore(t)
			ctx := t.Context()

			blocker := createTask(t, s, taskTestNow, defaultTaskInput())
			blocked := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
				t.Fatalf("AddEdge blocks: %v", err)
			}
			walkTo(t, s, blocker.ID, state)

			ids, err := s.BlockedTaskIDs(ctx)
			if err != nil {
				t.Fatalf("BlockedTaskIDs: %v", err)
			}
			if ids[blocked.ID] {
				t.Fatalf("BlockedTaskIDs with blocker %s: %s should be unblocked, got %v",
					state, blocked.ID, ids)
			}
			if isBlocked(t, s, blocked.ID) {
				t.Fatalf("IsBlocked(%s): want false with blocker %s", blocked.ID, state)
			}
		})
	}
}

func TestBlockedTaskIDsAbandonedBlocker(t *testing.T) {
	s := openTaskStore(t)

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "ready", "abandoned"); err != nil {
		t.Fatalf("abandon blocker: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: abandoned blocker must not block")
	}
}

// TestDeliveredStateSetCoversDeliveryRanks pins deliveredStateSet against the
// delivery axis it is derived from: every ranked state plus abandoned, and
// nothing else. A state added to deliveryRanks without reaching the set would
// leave assign.go's state-only guards and taskClosed disagreeing on what
// "delivered" means.
func TestDeliveredStateSetCoversDeliveryRanks(t *testing.T) {
	want := append(slices.Sorted(maps.Keys(deliveryRanks)), "abandoned")
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(deliveredStateSet)); !slices.Equal(got, want) {
		t.Fatalf("deliveredStateSet = %v, want %v", got, want)
	}
}

// TestDeliveryRanksMatchLegalTransitions pins the reason the two terminals
// share a rank: a state ranked *below* another it cannot legally transition to
// is a wedge — taskClosed would hold a task short of some repo's done_state
// with no move left to make. Every ranked state must therefore be able to
// reach any strictly higher-ranked one.
func TestDeliveryRanksMatchLegalTransitions(t *testing.T) {
	for from, fromRank := range deliveryRanks {
		for to, toRank := range deliveryRanks {
			if toRank <= fromRank {
				continue
			}
			if !legalTransitions[[2]string{from, to}] {
				t.Errorf("%s ranks %d, below %s at %d, but %s -> %s is not a legal transition: "+
					"a task at %s in a repo gating on %s could never close",
					from, fromRank, to, toRank, from, to, from, to)
			}
		}
	}
}

// mapRepo maps repo to projectID with the given done_state, creating the
// project if it is not the default "horndb" fixture.
func mapRepo(t *testing.T, s *Store, projectID, repo, doneState string) {
	t.Helper()
	ctx := t.Context()
	if projectID != "horndb" {
		if err := s.CreateProject(ctx, projectID, projectID, strings.ToUpper(projectID)); err != nil {
			t.Fatalf("CreateProject %s: %v", projectID, err)
		}
	}
	if err := s.AddRepo(ctx, projectID, repo); err != nil {
		t.Fatalf("AddRepo %s: %v", repo, err)
	}
	if err := s.SetRepoDoneState(ctx, repo, doneState); err != nil {
		t.Fatalf("SetRepoDoneState %s=%s: %v", repo, doneState, err)
	}
}

// landCommit attributes a commit in repo to taskID *and* records it on that
// repo's default branch, which is what makes the task's closed predicate join
// through the repo's mapping. Both halves are needed: taskClosed reads the
// landed set (task_commits ⋈ main_commits), the same one LandedMainID does.
func landCommit(t *testing.T, s *Store, taskID, repo, sha string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "commit.land", nil,
		func(tx *sql.Tx, eventID int64) error {
			if err := InsertTaskCommit(tx, TaskCommit{
				TaskID: taskID, Repo: repo, SHA: sha,
				Source: "branch_push", SeenAt: taskTestNow,
			}); err != nil {
				return err
			}
			_, err := AppendMainCommit(tx, repo, sha, taskTestNow)
			return err
		})
	if err != nil {
		t.Fatalf("land commit %s in %s for %s: %v", sha, repo, taskID, err)
	}
}

// pushBranchCommit attributes a commit to taskID without landing it: the row a
// task-branch push writes for work that may never reach the default branch.
func pushBranchCommit(t *testing.T, s *Store, taskID, repo, sha string) {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "commit.attribute", nil,
		func(tx *sql.Tx, eventID int64) error {
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: taskID, Repo: repo, SHA: sha,
				Source: "branch_push", SeenAt: taskTestNow,
			})
		})
	if err != nil {
		t.Fatalf("attribute commit %s to %s: %v", sha, taskID, err)
	}
}

// TestBlockedTaskIDsPerRepoDoneState pins the per-repo closed predicate
// (spec 004 §1.3): a blocker stops blocking at or past *its repo mapping's*
// done_state, not at one fixed tuple of states. The same merged blocker is
// closed in a repo that gates on merged and still open in one that gates on
// released.
func TestBlockedTaskIDsPerRepoDoneState(t *testing.T) {
	cases := []struct {
		doneState string
		state     string
		want      bool // want blocked
	}{
		{"merged", "merged", false},
		{"merged", "deployed_prod", false},
		{"deployed_prod", "merged", true},
		{"deployed_prod", "deployed_dev", true},
		{"deployed_prod", "deployed_prod", false},
		{"released", "merged", true},
		{"released", "deployed_dev", true},
		{"released", "released", false},
		// The two terminals are peers, not ordered (deliveryRanks): §5.1's
		// branches never meet, and there is no legal transition between them,
		// so treating either as short of the other would leave a task that
		// reached the wrong one blocking forever with nowhere to advance.
		{"released", "deployed_prod", false},
		{"deployed_prod", "released", false},
		// Abandoned is closed everywhere: cancelled work blocks nothing.
		{"released", "abandoned", false},
	}
	for _, tc := range cases {
		t.Run(tc.doneState+"/"+tc.state, func(t *testing.T) {
			s := openTaskStore(t)
			repo := "acme/" + tc.doneState
			mapRepo(t, s, "horndb", repo, tc.doneState)

			blocker := createTask(t, s, taskTestNow, defaultTaskInput())
			blocked := createTask(t, s, taskTestNow, defaultTaskInput())
			if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
				t.Fatalf("AddEdge blocks: %v", err)
			}
			landCommit(t, s, blocker.ID, repo, "sha-"+blocker.ID)
			walkTo(t, s, blocker.ID, tc.state)

			ids, err := s.BlockedTaskIDs(t.Context())
			if err != nil {
				t.Fatalf("BlockedTaskIDs: %v", err)
			}
			if ids[blocked.ID] != tc.want {
				t.Errorf("BlockedTaskIDs[%s] = %v, want %v (repo done_state %s, blocker %s)",
					blocked.ID, ids[blocked.ID], tc.want, tc.doneState, tc.state)
			}
			if got := isBlocked(t, s, blocked.ID); got != tc.want {
				t.Errorf("IsBlocked(%s) = %v, want %v (repo done_state %s, blocker %s)",
					blocked.ID, got, tc.want, tc.doneState, tc.state)
			}
		})
	}
}

// TestBlockedTaskIDsMultiRepoBlocker pins the multi-repo reading: a task whose
// work landed in two repos is closed only once it satisfies the strictest of
// them. Landing in a merged-gated repo does not release the release-gated one.
func TestBlockedTaskIDsMultiRepoBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/lib", "merged")
	mapRepo(t, s, "horndb", "acme/app", "released")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/lib", "sha-lib")
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")

	walkTo(t, s, blocker.ID, "merged")
	if !isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: merged blocker with a release-gated repo must still block")
	}
	if err := transition(t, s, taskTestNow, blocker.ID, "merged", "released"); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: released blocker must not block")
	}
}

// TestBlockedTaskIDsContainerBlocker pins the one state-fixed case (004 §6.4):
// a task with children has no commit of its own, cannot advance past merged,
// and is therefore closed at merged in every repo — including one whose
// mapping gates on released.
func TestBlockedTaskIDsContainerBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge child_of: %v", err)
	}
	if err := addEdge(t, s, parent.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, child.ID, "acme/app", "sha-child")

	// The child closing rolls the parent up to merged (004 §6.5).
	walkTo(t, s, child.ID, "released")
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a container at merged must not block, whatever its repo gates on")
	}
}

// TestBlockedTaskIDsUnmappedRepoBlocker pins the fallback: a commit in a repo
// no project maps takes DefaultDoneState, so the blocker closes at merged
// rather than blocking forever on a done_state nobody configured.
func TestBlockedTaskIDsUnmappedRepoBlocker(t *testing.T) {
	s := openTaskStore(t)

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/unmapped", "sha-unmapped")
	walkTo(t, s, blocker.ID, "merged")

	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: merged blocker in an unmapped repo must not block")
	}
}

// TestBlockedTaskIDsUnlandedCommitBlocker pins that taskClosed gates on the
// *landed* repo set, not on attribution: a task branch pushed to a
// release-gated repo writes a task_commits row even when that approach is
// abandoned and the work lands elsewhere. Gating on it would block the
// blocker's dependents forever, since ResolveDelivery walks the same
// task_commits ⋈ main_commits join and would never advance the task either.
func TestBlockedTaskIDsUnlandedCommitBlocker(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")
	mapRepo(t, s, "horndb", "acme/lib", "merged")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	pushBranchCommit(t, s, blocker.ID, "acme/app", "sha-abandoned")
	landCommit(t, s, blocker.ID, "acme/lib", "sha-landed")
	walkTo(t, s, blocker.ID, "merged")

	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a branch push that never landed must not gate the blocker on that repo")
	}
}

// TestBlockedTaskIDsContainerWithOwnCommits pins §6.4's state-fixed case
// against the case the "a container has no commits" reading misses: AddEdge
// happily gives children to a task that already landed some. Such a parent is
// barred from every state past merged (containerForbiddenStates), so gating it
// on a release-based repo would block its dependents forever.
func TestBlockedTaskIDsContainerWithOwnCommits(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "released")

	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, parent.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, parent.ID, "acme/app", "sha-parent")
	walkTo(t, s, parent.ID, "merged")

	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge child_of: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a container at merged must not block, even carrying its own commits")
	}
}

// TestBlockedTaskIDsDoneStateFlipAfterDelivery pins that raising a repo's
// done_state after a task delivered cannot strand that task's dependents.
// Discovery runs only at add-repo (004 §5.4), so `lode project set-repo
// --done-state` on a repo that started cutting releases is the expected path,
// and a task already at deployed_prod has no legal transition left.
func TestBlockedTaskIDsDoneStateFlipAfterDelivery(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "horndb", "acme/app", "deployed_prod")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")
	walkTo(t, s, blocker.ID, "deployed_prod")
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: a blocker at its repo's done_state must not block")
	}

	if err := s.SetRepoDoneState(t.Context(), "acme/app", "released"); err != nil {
		t.Fatalf("SetRepoDoneState: %v", err)
	}
	if isBlocked(t, s, blocked.ID) {
		t.Fatalf("IsBlocked: raising done_state must not strand an already-delivered blocker " +
			"(deployed_prod -> released is not a legal transition)")
	}
}

// TestOpenBlockersPerRepoDoneState covers the three other queries taskClosed
// is rendered into — each with a different set of enclosing aliases, so an
// alias collision or a mis-parenthesised predicate shows up here rather than
// only in IsBlocked. The blocker's repo belongs to a *second* project, which
// is what the deliberately project-unscoped repo join has to tolerate:
// 'blocks' edges are not project-scoped.
func TestOpenBlockersPerRepoDoneState(t *testing.T) {
	s := openTaskStore(t)
	mapRepo(t, s, "otherproj", "acme/app", "released")

	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge blocks: %v", err)
	}
	landCommit(t, s, blocker.ID, "acme/app", "sha-app")
	walkTo(t, s, blocker.ID, "merged")

	// brief.go: openBlockers.
	got, err := s.openBlockers(t.Context(), blocked.ID)
	if err != nil {
		t.Fatalf("openBlockers: %v", err)
	}
	if len(got) != 1 || got[0].ID != blocker.ID {
		t.Fatalf("openBlockers = %+v, want [%s] (merged, repo gates on released)", got, blocker.ID)
	}

	// project_work.go: attachOpenBlockers.
	facts := map[string]*ProjectWorkFact{blocked.ID: {}}
	if err := s.attachOpenBlockers(t.Context(), "horndb", facts); err != nil {
		t.Fatalf("attachOpenBlockers: %v", err)
	}
	if n := len(facts[blocked.ID].OpenBlockers); n != 1 {
		t.Fatalf("attachOpenBlockers left %d open blockers, want 1", n)
	}

	// ranking.go: readyCandidates, which renders blockedCondition into a
	// different query again.
	ready, err := s.readyCandidates(t.Context(), "horndb")
	if err != nil {
		t.Fatalf("readyCandidates: %v", err)
	}
	for _, cand := range ready {
		if cand.ID == blocked.ID {
			t.Fatalf("readyCandidates offered %s, which an undelivered blocker still blocks", blocked.ID)
		}
	}

	// Releasing the blocker clears it from all three.
	if err := transition(t, s, taskTestNow, blocker.ID, "merged", "released"); err != nil {
		t.Fatalf("release blocker: %v", err)
	}
	got, err = s.openBlockers(t.Context(), blocked.ID)
	if err != nil {
		t.Fatalf("openBlockers after release: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("openBlockers after release = %+v, want none", got)
	}
	ready, err = s.readyCandidates(t.Context(), "horndb")
	if err != nil {
		t.Fatalf("readyCandidates after release: %v", err)
	}
	if !slices.ContainsFunc(ready, func(c Task) bool { return c.ID == blocked.ID }) {
		t.Fatalf("readyCandidates after release omitted %s", blocked.ID)
	}
}

func TestChildOfCycleRejected(t *testing.T) {
	s := openTaskStore(t)

	// t1, t2, and t3 each stand in as a child_of parent below; since 029 §2
	// any ordinary task may be one.
	t1 := createTask(t, s, taskTestNow, containerInput())
	t2 := createTask(t, s, taskTestNow, containerInput())
	t3 := createTask(t, s, taskTestNow, containerInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t1.ID, t2.ID, err)
	}
	err := addEdge(t, s, t2.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("direct cycle: want ErrCycle, got %v", err)
	}

	// Transitive cycle: t2 child_of t3 makes the chain t1 -> t2 -> t3;
	// t3 child_of t1 would close the loop.
	if err := addEdge(t, s, t2.ID, t3.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge %s child_of %s: %v", t2.ID, t3.ID, err)
	}
	err = addEdge(t, s, t3.ID, t1.ID, "child_of")
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("transitive cycle: want ErrCycle, got %v", err)
	}
}

func TestAddEdgeDuplicateRejected(t *testing.T) {
	s := openTaskStore(t)

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("duplicate edge: want ErrEdgeExists, got %v", err)
	}
}

func TestAddEdgeSelfRejected(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, typ := range []string{"child_of", "blocks"} {
		if err := addEdge(t, s, task.ID, task.ID, typ); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("self-edge %s: want ErrInvalidInput, got %v", typ, err)
		}
	}
}

func TestAddEdgeUnknownTask(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, task.ID, "HDB-999", "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge to unknown task: want ErrNotFound, got %v", err)
	}
	if err := addEdge(t, s, "HDB-999", task.ID, "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEdge from unknown task: want ErrNotFound, got %v", err)
	}
}

func TestRemoveEdgeAndListEdges(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	t1 := createTask(t, s, taskTestNow, defaultTaskInput())
	t2 := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := addEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	out, in, err := s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t1.ID, err)
	}
	wantOut := []Edge{{FromTask: t1.ID, ToTask: t2.ID, Type: "blocks"}}
	if !reflect.DeepEqual(out, wantOut) || len(in) != 0 {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=%v in=[]", t1.ID, out, in, wantOut)
	}
	out, in, err = s.ListEdges(ctx, t2.ID)
	if err != nil {
		t.Fatalf("ListEdges %s: %v", t2.ID, err)
	}
	if len(out) != 0 || !reflect.DeepEqual(in, wantOut) {
		t.Fatalf("ListEdges %s: out=%v in=%v, want out=[] in=%v", t2.ID, out, in, wantOut)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); err != nil {
		t.Fatalf("RemoveEdge: %v", err)
	}
	out, in, err = s.ListEdges(ctx, t1.ID)
	if err != nil {
		t.Fatalf("ListEdges after remove: %v", err)
	}
	if len(out) != 0 || len(in) != 0 {
		t.Fatalf("ListEdges after remove: out=%v in=%v, want both empty", out, in)
	}

	if err := removeEdge(t, s, t1.ID, t2.ID, "blocks"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RemoveEdge absent: want ErrNotFound, got %v", err)
	}
}

func TestListTasksFiltersAndOrdering(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	if err := s.CreateProject(ctx, "other", "Other", "OT"); err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}

	mk := func(project, priority string) *Task {
		in := defaultTaskInput()
		in.ProjectID = project
		in.Priority = priority
		return createTask(t, s, taskTestNow, in)
	}
	tLow := mk("horndb", "low")          // HDB-1
	tCrit := mk("horndb", "critical")    // HDB-2
	tMed := mk("horndb", "medium")       // HDB-3
	tHigh := mk("horndb", "high")        // HDB-4
	tCrit2 := mk("horndb", "critical")   // HDB-5
	tOther := mk("other", "high")        // OT-1
	walkTo(t, s, tMed.ID, "in_progress") // HDB-3 -> in_progress

	idsOf := func(tasks []Task) []string {
		var ids []string
		for _, task := range tasks {
			ids = append(ids, task.ID)
		}
		return ids
	}

	// No filter: priority order (critical first), then id within a priority —
	// key lexically (HDB before OT), then the numeric suffix.
	all, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks all: %v", err)
	}
	wantAll := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tOther.ID, tMed.ID, tLow.ID}
	if got := idsOf(all); !reflect.DeepEqual(got, wantAll) {
		t.Fatalf("ListTasks order: got %v, want %v", got, wantAll)
	}

	// Project filter.
	horn, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks project: %v", err)
	}
	wantHorn := []string{tCrit.ID, tCrit2.ID, tHigh.ID, tMed.ID, tLow.ID}
	if got := idsOf(horn); !reflect.DeepEqual(got, wantHorn) {
		t.Fatalf("ListTasks project=horndb: got %v, want %v", got, wantHorn)
	}

	// States filter.
	inProg, err := s.ListTasks(ctx, TaskFilter{States: []string{"in_progress"}})
	if err != nil {
		t.Fatalf("ListTasks states: %v", err)
	}
	if got := idsOf(inProg); !reflect.DeepEqual(got, []string{tMed.ID}) {
		t.Fatalf("ListTasks states=[in_progress]: got %v, want [%s]", got, tMed.ID)
	}

	// Priority filter.
	crit, err := s.ListTasks(ctx, TaskFilter{Priority: "critical"})
	if err != nil {
		t.Fatalf("ListTasks priority: %v", err)
	}
	if got := idsOf(crit); !reflect.DeepEqual(got, []string{tCrit.ID, tCrit2.ID}) {
		t.Fatalf("ListTasks priority=critical: got %v, want [%s %s]", got, tCrit.ID, tCrit2.ID)
	}

	// Combined filters.
	combo, err := s.ListTasks(ctx, TaskFilter{Project: "other", States: []string{"ready", "draft"}, Priority: "high"})
	if err != nil {
		t.Fatalf("ListTasks combined: %v", err)
	}
	if got := idsOf(combo); !reflect.DeepEqual(got, []string{tOther.ID}) {
		t.Fatalf("ListTasks combined: got %v, want [%s]", got, tOther.ID)
	}
}

func TestGetTaskNotFound(t *testing.T) {
	s := openTaskStore(t)

	_, err := s.GetTask(t.Context(), "HDB-999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetTask unknown: want ErrNotFound, got %v", err)
	}
}

func TestCreateTaskConcern(t *testing.T) {
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Concern = "security"
	task := createTask(t, s, taskTestNow, in)
	if task.Concern != "security" {
		t.Fatalf("CreateTask concern: got %q, want security", task.Concern)
	}
	if task.NeedsDecomposition {
		t.Fatalf("CreateTask needs_decomposition: want false by default")
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "security" {
		t.Fatalf("GetTask concern: got %q, want security", got.Concern)
	}
	if got.NeedsDecomposition {
		t.Fatalf("GetTask needs_decomposition: want false by default")
	}
}

func TestCreateTaskNoConcern(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if task.Concern != "" {
		t.Fatalf("CreateTask concern: got %q, want empty", task.Concern)
	}

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("GetTask concern: got %q, want empty", got.Concern)
	}
}

func TestCreateTaskInvalidConcernRejected(t *testing.T) {
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Concern = "not-a-concern"
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateTask invalid concern: want ErrInvalidInput, got %v", err)
	}
}

// updateTaskFields drives UpdateTaskFields through RecordEvent.
func updateTaskFields(t *testing.T, s *Store, now time.Time, id string, title, body, priority, concern *string, needsDecomposition *bool) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.update", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UpdateTaskFields(tx, now, id, title, body, priority, concern, needsDecomposition)
		})
	return err
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestUpdateTaskFieldsConcernAndNeedsDecomposition(t *testing.T) {
	s := openTaskStore(t)

	task := createTask(t, s, taskTestNow, defaultTaskInput())

	// Set concern.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("performance"), nil); err != nil {
		t.Fatalf("set concern: %v", err)
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "performance" {
		t.Fatalf("concern after set: got %q, want performance", got.Concern)
	}

	// Clear with "".
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr(""), nil); err != nil {
		t.Fatalf("clear concern with \"\": %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("concern after clear with \"\": got %q, want empty", got.Concern)
	}

	// Set again, then clear with "none".
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("usability"), nil); err != nil {
		t.Fatalf("set concern again: %v", err)
	}
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("none"), nil); err != nil {
		t.Fatalf("clear concern with none: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Concern != "" {
		t.Fatalf("concern after clear with none: got %q, want empty", got.Concern)
	}

	// needs_decomposition true then false.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, nil, boolPtr(true)); err != nil {
		t.Fatalf("set needs_decomposition true: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.NeedsDecomposition {
		t.Fatalf("needs_decomposition: want true")
	}
	if err := updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, nil, boolPtr(false)); err != nil {
		t.Fatalf("set needs_decomposition false: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.NeedsDecomposition {
		t.Fatalf("needs_decomposition: want false")
	}

	// Invalid concern rejected.
	err = updateTaskFields(t, s, taskTestNow, task.ID, nil, nil, nil, strPtr("not-a-concern"), nil)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("update with invalid concern: want ErrInvalidInput, got %v", err)
	}
}

// TestUpdateTaskFieldsRejectsBlankTitle pins the invariant CreateTask already
// enforces: a task carries a title for its whole life, so an update must not
// be able to blank one out.
func TestUpdateTaskFieldsRejectsBlankTitle(t *testing.T) {
	s := openTaskStore(t)
	in := defaultTaskInput()
	task := createTask(t, s, taskTestNow, in)

	for _, blank := range []string{"", "   ", "\n\t"} {
		err := updateTaskFields(t, s, taskTestNow, task.ID, strPtr(blank), nil, nil, nil, nil)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("update with title %q: want ErrInvalidInput, got %v", blank, err)
		}
	}
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Title != in.Title {
		t.Fatalf("title after rejected updates = %q, want %q", got.Title, in.Title)
	}

	// A non-blank title still goes through.
	if err := updateTaskFields(t, s, taskTestNow, task.ID, strPtr("Renamed"), nil, nil, nil, nil); err != nil {
		t.Fatalf("update with valid title: %v", err)
	}
	if got, err = s.GetTask(t.Context(), task.ID); err != nil {
		t.Fatalf("GetTask: %v", err)
	} else if got.Title != "Renamed" {
		t.Fatalf("title = %q, want Renamed", got.Title)
	}
}

// TestCreateTaskNormalizesPins: CreateTask used to store TaskInput.Skills
// verbatim while SetTaskSkills cleaned them, so a task created with padded,
// duplicated or empty pins produced a "pinned skill not found" warning — one
// of them for the empty string — in every recommendation and brief it served.
// Both paths now share normalizePins, and an over-cap list is rejected rather
// than truncated behind the caller's back.
func TestCreateTaskNormalizesPins(t *testing.T) {
	s := openTaskStore(t)

	in := defaultTaskInput()
	in.Skills = []string{"  tdd  ", "tdd", "", "   ", "tdd", "debugging"}
	task := createTask(t, s, taskTestNow, in)
	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("pins stored verbatim: %+v", got.Skills)
	}
	// The returned Task must agree with what was persisted.
	if len(task.Skills) != 2 || task.Skills[0] != "tdd" {
		t.Fatalf("returned task pins: %+v", task.Skills)
	}

	over := make([]string, maxTaskPins+1)
	for i := range over {
		over[i] = fmt.Sprintf("skill-%02d", i)
	}
	in = defaultTaskInput()
	in.Skills = over
	_, _, err = s.RecordEvent(t.Context(), "cli", nextExt(t), "task.create", nil,
		func(tx *sql.Tx, eventID int64) error {
			_, err := CreateTask(tx, taskTestNow, in)
			return err
		})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-cap pins on create: want ErrInvalidInput, got %v", err)
	}
	if err := setTaskSkills(t, s, taskTestNow, task.ID, over); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("over-cap pins on set: want ErrInvalidInput, got %v", err)
	}
}

// setTaskSkills drives SetTaskSkills through RecordEvent, the way production
// code will use it.
func setTaskSkills(t *testing.T, s *Store, now time.Time, id string, skills []string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.skills_set", nil,
		func(tx *sql.Tx, eventID int64) error {
			return SetTaskSkills(tx, now, id, skills)
		})
	return err
}

// rawSkills reads the tasks.skills column as text, bypassing scanTask's
// nil-normalization, so a test can tell a stored JSON null from "[]".
func rawSkills(t *testing.T, s *Store, id string) string {
	t.Helper()
	var raw string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT skills::text FROM tasks WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("read raw skills: %v", err)
	}
	return raw
}

func TestTaskSkills(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	got, err := s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 0 {
		t.Fatalf("default skills: %+v err=%v", got.Skills, err)
	}

	// CreateTask persists TaskInput.Skills too, not just SetTaskSkills.
	in := defaultTaskInput()
	in.Skills = []string{"tdd"}
	seeded := createTask(t, s, taskTestNow, in)
	got, err = s.GetTask(t.Context(), seeded.ID)
	if err != nil || len(got.Skills) != 1 || got.Skills[0] != "tdd" {
		t.Fatalf("skills from CreateTask: %+v err=%v", got.Skills, err)
	}

	setNow := taskTestNow.Add(time.Hour)
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd", "debugging"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("skills: %+v", got.Skills)
	}
	if !got.UpdatedAt.Equal(setNow.UTC()) {
		t.Fatalf("updated_at after set = %v, want %v", got.UpdatedAt, setNow.UTC())
	}

	// Blank entries are dropped and duplicates removed, preserving order.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd", "", "  ", "tdd", "debugging", "tdd"}); err != nil {
		t.Fatalf("set with blanks and dupes: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 2 || got.Skills[0] != "tdd" || got.Skills[1] != "debugging" {
		t.Fatalf("skills after blanks/dupes: %+v err=%v", got.Skills, err)
	}

	// Clearing with an empty slice replaces, rather than merges.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil || len(got.Skills) != 0 {
		t.Fatalf("skills after clear: %+v err=%v", got.Skills, err)
	}

	// nil normalizes to a jsonb "[]", not a JSON null: SkillsByNames reads
	// this column with jsonb_array_elements_text, which errors on null. The
	// Go-side len() check above is not enough to catch a regression here —
	// read the raw column instead.
	if err := setTaskSkills(t, s, setNow, task.ID, []string{"tdd"}); err != nil {
		t.Fatalf("set again: %v", err)
	}
	clearNow := setNow.Add(time.Hour)
	if err := setTaskSkills(t, s, clearNow, task.ID, nil); err != nil {
		t.Fatalf("clear with nil: %v", err)
	}
	if raw := rawSkills(t, s, task.ID); raw != "[]" {
		t.Fatalf("stored skills after nil clear = %q, want []", raw)
	}
	got, err = s.GetTask(t.Context(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Skills == nil {
		t.Fatal("Task.Skills is nil, violating its documented never-nil invariant")
	}
	if len(got.Skills) != 0 {
		t.Fatalf("skills after nil clear: %+v", got.Skills)
	}
	if !got.UpdatedAt.Equal(clearNow.UTC()) {
		t.Fatalf("updated_at after nil clear = %v, want %v", got.UpdatedAt, clearNow.UTC())
	}

	if err := setTaskSkills(t, s, clearNow, "WL-999", []string{"tdd"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("set skills on unknown task: want ErrNotFound, got %v", err)
	}
}

// TestTaskColumnsEntriesAreCommaFree guards prefixedTaskColumns' naive
// strings.Split(taskColumns, ", "): a call expression or a bare comma in any
// entry silently mangles the alias-qualified SELECT list it builds for
// readyCandidates, surfacing only as a SQLSTATE 42601 deep inside ClaimNext.
func TestTaskColumnsEntriesAreCommaFree(t *testing.T) {
	for _, c := range strings.Split(taskColumns, ", ") {
		if strings.ContainsAny(c, "(),") {
			t.Fatalf("taskColumns entry %q contains a call expression or comma; "+
				"prefixedTaskColumns splits naively on \", \" and would mangle it", c)
		}
	}
}
