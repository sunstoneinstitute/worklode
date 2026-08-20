package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// deleteTask drives DeleteTask through RecordEvent, the way the API does.
func deleteTask(t *testing.T, s *Store, id, actorID, justification string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.deleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return DeleteTask(tx, taskTestNow, id, actorID, justification, eventID)
		})
	return err
}

func undeleteTask(t *testing.T, s *Store, id string) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.undeleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UndeleteTask(tx, taskTestNow, id, eventID)
		})
	return err
}

func deleteDoc(t *testing.T, s *Store, id int64, actorID, justification string) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "delete", "cli", nextExt(t), "doc.deleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return DeleteDoc(tx, s.Now(), id, actorID, justification, eventID)
		})
	return err
}

func undeleteDoc(t *testing.T, s *Store, id int64) error {
	t.Helper()
	_, _, err := s.RecordDocEvent(t.Context(), "delete", "cli", nextExt(t), "doc.undeleted", nil,
		func(tx *sql.Tx, eventID int64) error {
			return UndeleteDoc(tx, s.Now(), id, eventID)
		})
	return err
}

// deleteChanges returns the state_log payloads recording a delete/undelete of
// one entity, oldest first.
func deleteChanges(t *testing.T, s *Store, kind, id string) []map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT change FROM state_log
		  WHERE entity_kind = $1 AND entity_id = $2 AND change->>'field' = 'deleted'
		  ORDER BY id`, kind, id)
	if err != nil {
		t.Fatalf("read state_log for %s %s: %v", kind, id, err)
	}
	defer rows.Close()
	var out []map[string]string
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan state_log: %v", err)
		}
		var change map[string]string
		if err := json.Unmarshal(raw, &change); err != nil {
			t.Fatalf("decode state_log change: %v", err)
		}
		out = append(out, change)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate state_log: %v", err)
	}
	return out
}

func TestDeleteTaskTombstonesClosesLeaseAndLogs(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if err := deleteTask(t, s, task.ID, "stig", "created by mistake"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	// Fetching by id still succeeds and renders the tombstone (044 §4).
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after delete: %v", err)
	}
	if got.Tombstone == nil {
		t.Fatal("GetTask after delete: Tombstone is nil")
	}
	if got.Tombstone.DeletedBy != "stig" || got.Tombstone.Justification != "created by mistake" {
		t.Fatalf("tombstone = %+v", *got.Tombstone)
	}
	if got.Tombstone.DeletedAt.IsZero() {
		t.Fatal("tombstone DeletedAt is zero")
	}

	if _, err := s.ActiveLease(ctx, task.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveLease after delete: got %v, want ErrNotFound", err)
	}

	changes := deleteChanges(t, s, "task", task.ID)
	want := []map[string]string{
		{"field": "deleted", "old": "false", "new": "true", "justification": "created by mistake"},
	}
	if len(changes) != 1 || changes[0]["new"] != want[0]["new"] ||
		changes[0]["justification"] != want[0]["justification"] {
		t.Fatalf("delete state_log:\n got %v\nwant %v", changes, want)
	}
}

func TestDeleteTaskTwiceIsInvalidInput(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := deleteTask(t, s, task.ID, "stig", "noise"); err != nil {
		t.Fatalf("first DeleteTask: %v", err)
	}
	if err := deleteTask(t, s, task.ID, "stig", "noise again"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second DeleteTask: got %v, want ErrInvalidInput", err)
	}
}

func TestDeleteTaskUnknownIsNotFound(t *testing.T) {
	s := openTaskStore(t)
	if err := deleteTask(t, s, "HDB-999", "stig", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteTask on unknown task: got %v, want ErrNotFound", err)
	}
}

func TestUndeleteTaskRestores(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := deleteTask(t, s, task.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if err := undeleteTask(t, s, task.ID); err != nil {
		t.Fatalf("UndeleteTask: %v", err)
	}

	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask after undelete: %v", err)
	}
	if got.Tombstone != nil {
		t.Fatalf("Tombstone after undelete = %+v, want nil", *got.Tombstone)
	}
	live, err := s.ListTasks(ctx, TaskFilter{Project: "horndb"})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(live) != 1 || live[0].ID != task.ID {
		t.Fatalf("ListTasks after undelete = %v, want just %s", taskIDsOf(live), task.ID)
	}

	if got := deleteChanges(t, s, "task", task.ID); len(got) != 2 || got[1]["new"] != "false" {
		t.Fatalf("undelete state_log: got %v", got)
	}
}

func TestUndeleteLiveTaskIsInvalidInput(t *testing.T) {
	s := openTaskStore(t)
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := undeleteTask(t, s, task.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UndeleteTask on a live task: got %v, want ErrInvalidInput", err)
	}
}

func TestListTasksHidesDeletedAndDeletedFilterShowsOnlyThem(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	live := createTask(t, s, taskTestNow, defaultTaskInput())
	gone := createTask(t, s, taskTestNow, defaultTaskInput())

	if err := deleteTask(t, s, gone.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	got, err := s.ListTasks(ctx, TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("ListTasks{} = %v, want just %s", taskIDsOf(got), live.ID)
	}

	got, err = s.ListTasks(ctx, TaskFilter{Deleted: true})
	if err != nil {
		t.Fatalf("ListTasks{Deleted}: %v", err)
	}
	if len(got) != 1 || got[0].ID != gone.ID {
		t.Fatalf("ListTasks{Deleted:true} = %v, want just %s", taskIDsOf(got), gone.ID)
	}
	if got[0].Tombstone == nil {
		t.Fatal("ListTasks{Deleted:true}: Tombstone is nil")
	}
}

func TestProjectWorkFactsHideDeletedTaskAndParent(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := deleteTask(t, s, parent.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	facts, err := s.ListProjectWorkFacts(ctx, "horndb")
	if err != nil {
		t.Fatalf("ListProjectWorkFacts: %v", err)
	}
	if len(facts) != 1 || facts[0].Task.ID != child.ID {
		t.Fatalf("facts = %v, want just %s", factIDs(facts), child.ID)
	}
	if facts[0].Parent != nil {
		t.Fatalf("child names a deleted parent: %+v", *facts[0].Parent)
	}

	pm, err := s.ParentMap(ctx, "horndb")
	if err != nil {
		t.Fatalf("ParentMap: %v", err)
	}
	if _, ok := pm[child.ID]; ok {
		t.Fatalf("ParentMap still maps %s to a deleted parent", child.ID)
	}
}

func TestClaimNextSkipsDeletedTask(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	gone := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := deleteTask(t, s, gone.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	res, err := s.ClaimNext(ctx, ClaimNextOpts{
		ProjectID: "horndb", ActorID: "stig", Worktree: "host:/wt"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if res.Claimed {
		t.Fatalf("ClaimNext handed out %s, which is deleted", res.Task.ID)
	}

	// The live task next to it is still handed out.
	live := createTask(t, s, taskTestNow, defaultTaskInput())
	res, err = s.ClaimNext(ctx, ClaimNextOpts{
		ProjectID: "horndb", ActorID: "stig", Worktree: "host:/wt"})
	if err != nil {
		t.Fatalf("ClaimNext: %v", err)
	}
	if !res.Claimed || res.Task.ID != live.ID {
		t.Fatalf("ClaimNext = %+v, want a claim of %s", res, live.ID)
	}
}

func TestClaimDeletedTaskIsNotFound(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := deleteTask(t, s, task.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Claim on a deleted task: got %v, want ErrNotFound", err)
	}
}

func TestDeletedBlockerDoesNotBlock(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	blocker := createTask(t, s, taskTestNow, defaultTaskInput())
	blocked := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, blocker.ID, blocked.ID, "blocks"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if !isBlocked(t, s, blocked.ID) {
		t.Fatal("target is not blocked before the blocker is deleted")
	}

	if err := deleteTask(t, s, blocker.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	if isBlocked(t, s, blocked.ID) {
		t.Fatal("a deleted blocker still blocks its target")
	}
	ids, err := s.BlockedTaskIDs(ctx)
	if err != nil {
		t.Fatalf("BlockedTaskIDs: %v", err)
	}
	if ids[blocked.ID] {
		t.Fatalf("BlockedTaskIDs still names %s", blocked.ID)
	}
	if _, err := s.Claim(ctx, blocked.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim on the unblocked task: %v", err)
	}

	// The edge itself survives, so undelete restores the block (044 §4).
	if err := undeleteTask(t, s, blocker.ID); err != nil {
		t.Fatalf("UndeleteTask: %v", err)
	}
	if !isBlocked(t, s, blocked.ID) {
		t.Fatal("undeleting the blocker did not restore the block")
	}
}

func TestDeletedChildDoesNotMakeAContainer(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if _, err := s.Claim(ctx, parent.ID, "stig", "host:/wt", 0); !errors.Is(err, ErrBadTransition) {
		t.Fatalf("Claim on a container: got %v, want ErrBadTransition", err)
	}
	progress, err := s.ChildProgress(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if progress.Total != 1 {
		t.Fatalf("ChildProgress before delete = %+v, want Total 1", progress)
	}

	if err := deleteTask(t, s, child.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}

	progress, err = s.ChildProgress(ctx, parent.ID)
	if err != nil {
		t.Fatalf("ChildProgress: %v", err)
	}
	if progress.Total != 0 {
		t.Fatalf("ChildProgress after deleting the only child = %+v, want a zero value", progress)
	}
	if _, err := s.Claim(ctx, parent.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim after deleting the only child: %v", err)
	}
}

func TestDeleteDocTombstonesAndLogs(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	if err := deleteDoc(t, s, doc.ID, "stig", "imported twice"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}

	got, err := s.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDoc after delete: %v", err)
	}
	if got.Tombstone == nil {
		t.Fatal("GetDoc after delete: Tombstone is nil")
	}
	if got.Tombstone.DeletedBy != "stig" || got.Tombstone.Justification != "imported twice" {
		t.Fatalf("tombstone = %+v", *got.Tombstone)
	}

	changes := deleteChanges(t, s, "doc", strconv.FormatInt(doc.ID, 10))
	if len(changes) != 1 || changes[0]["new"] != "true" ||
		changes[0]["justification"] != "imported twice" {
		t.Fatalf("delete state_log: got %v", changes)
	}
}

func TestDeleteDocTwiceIsInvalidInputAndUnknownIsNotFound(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	if err := deleteDoc(t, s, doc.ID, "stig", "noise"); err != nil {
		t.Fatalf("first DeleteDoc: %v", err)
	}
	if err := deleteDoc(t, s, doc.ID, "stig", "noise"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("second DeleteDoc: got %v, want ErrInvalidInput", err)
	}
	if err := deleteDoc(t, s, 9999, "stig", "noise"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteDoc on an unknown doc: got %v, want ErrNotFound", err)
	}
}

func TestUndeleteDocRestoresAndLiveIsInvalidInput(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	if err := undeleteDoc(t, s, doc.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("UndeleteDoc on a live doc: got %v, want ErrInvalidInput", err)
	}
	if err := deleteDoc(t, s, doc.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}
	if err := undeleteDoc(t, s, doc.ID); err != nil {
		t.Fatalf("UndeleteDoc: %v", err)
	}

	got, err := s.GetDoc(ctx, doc.ID)
	if err != nil {
		t.Fatalf("GetDoc after undelete: %v", err)
	}
	if got.Tombstone != nil {
		t.Fatalf("Tombstone after undelete = %+v, want nil", *got.Tombstone)
	}
}

func TestListDocsHidesDeletedAndDeletedFilterShowsOnlyThem(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	live := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	gone := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 26, Slug: "026-elsewhere",
		Body: specBody, CreatedBy: "stig",
	})
	if err := deleteDoc(t, s, gone.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}

	docs, err := s.ListDocs(ctx, DocFilter{})
	if err != nil {
		t.Fatalf("ListDocs: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != live.ID {
		t.Fatalf("ListDocs{} = %v, want just %d", docIDsOf(docs), live.ID)
	}

	docs, err = s.ListDocs(ctx, DocFilter{Deleted: true})
	if err != nil {
		t.Fatalf("ListDocs{Deleted}: %v", err)
	}
	if len(docs) != 1 || docs[0].ID != gone.ID {
		t.Fatalf("ListDocs{Deleted:true} = %v, want just %d", docIDsOf(docs), gone.ID)
	}
	if docs[0].Tombstone == nil {
		t.Fatal("ListDocs{Deleted:true}: Tombstone is nil")
	}
}

// TestDeletedTaskClosedIsStateOnly pins 044 §1's "the tombstone is orthogonal
// to state": Closed keeps meaning delivered-or-abandoned, so a tombstoned draft
// answers false. Folding "deleted" into taskClosed would put "closed": true on
// the wire for a row that never even reached ready.
func TestDeletedTaskClosedIsStateOnly(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()

	draftIn := defaultTaskInput()
	draftIn.Draft = true
	draft := createTask(t, s, taskTestNow, draftIn)
	if err := deleteTask(t, s, draft.ID, "stig", "mistyped"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	got, err := s.GetTask(ctx, draft.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "draft" {
		t.Fatalf("state after delete = %q, want draft (delete is not a state)", got.State)
	}
	if got.Tombstone == nil {
		t.Fatal("GetTask on a deleted task: Tombstone is nil")
	}
	if got.Closed {
		t.Fatal("a tombstoned draft reports Closed true; the tombstone is orthogonal to state (044 §1)")
	}

	// The other half: deleting does not *un*-close an abandoned task either.
	abandoned := createTask(t, s, taskTestNow, defaultTaskInput())
	walkTo(t, s, abandoned.ID, "abandoned")
	if err := deleteTask(t, s, abandoned.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	got, err = s.GetTask(ctx, abandoned.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.Closed {
		t.Fatal("a tombstoned abandoned task reports Closed false")
	}
}

// TestListTasksHasChildrenIgnoresDeletedChildren keeps the list filter's
// container predicate in step with hasChildren, which the claim path uses.
func TestListTasksHasChildrenIgnoresDeletedChildren(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	child := createTask(t, s, taskTestNow, defaultTaskInput())
	if err := addEdge(t, s, child.ID, parent.ID, "child_of"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	got, err := s.ListTasks(ctx, TaskFilter{HasChildren: true})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 1 || got[0].ID != parent.ID {
		t.Fatalf("ListTasks{HasChildren} = %v, want just %s", taskIDsOf(got), parent.ID)
	}

	if err := deleteTask(t, s, child.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	got, err = s.ListTasks(ctx, TaskFilter{HasChildren: true})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("ListTasks{HasChildren} = %v after deleting the only child, want none "+
			"(hasChildren already says it is not a container)", taskIDsOf(got))
	}
}

// TestDeleteInProgressTaskReturnsItToReady covers the strand DeleteTask would
// otherwise leave: CloseActiveLease never touches task state, so an in_progress
// task would come back from undelete with no lease, invisible to the expiry
// sweeper and unclaimable.
func TestDeleteInProgressTaskReturnsItToReady(t *testing.T) {
	s := openTaskStore(t)
	ctx := t.Context()
	task := createTask(t, s, taskTestNow, defaultTaskInput())
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt", 0); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	if err := deleteTask(t, s, task.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	got, err := s.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.State != "ready" {
		t.Fatalf("state after deleting an in_progress task = %q, want ready", got.State)
	}

	if err := undeleteTask(t, s, task.ID); err != nil {
		t.Fatalf("UndeleteTask: %v", err)
	}
	if _, err := s.Claim(ctx, task.ID, "stig", "host:/wt2", 0); err != nil {
		t.Fatalf("Claim after delete/undelete: %v", err)
	}
}

// TestDeleteChildReResolvesParent: only Transition otherwise re-runs the
// roll-up, so without an explicit re-resolve a parent would keep the state its
// now-hidden child put it in.
func TestDeleteChildReResolvesParent(t *testing.T) {
	s := openTaskStore(t)
	parent := createTask(t, s, taskTestNow, defaultTaskInput())
	started := createTask(t, s, taskTestNow, defaultTaskInput())
	idle := createTask(t, s, taskTestNow, defaultTaskInput())
	for _, c := range []string{started.ID, idle.ID} {
		if err := addEdge(t, s, c, parent.ID, "child_of"); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
	}
	if err := transition(t, s, taskTestNow, started.ID, "ready", "in_progress"); err != nil {
		t.Fatalf("start the child: %v", err)
	}
	if got := taskState(t, s, parent.ID); got != "in_progress" {
		t.Fatalf("parent state = %q, want in_progress once a child started", got)
	}

	if err := deleteTask(t, s, started.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if got := taskState(t, s, parent.ID); got != "ready" {
		t.Fatalf("parent state = %q after deleting the only started child, want ready", got)
	}

	// Undelete is the mirror: the child comes back ready (DeleteTask released
	// it from in_progress), so the parent stays ready and is re-resolved rather
	// than left at whatever the delete implied.
	if err := undeleteTask(t, s, started.ID); err != nil {
		t.Fatalf("UndeleteTask: %v", err)
	}
	if err := transition(t, s, taskTestNow, started.ID, "ready", "in_progress"); err != nil {
		t.Fatalf("restart the child: %v", err)
	}
	if got := taskState(t, s, parent.ID); got != "in_progress" {
		t.Fatalf("parent state = %q after the restored child restarted, want in_progress", got)
	}
}

// taskState reads one task's stored state, tombstoned or not.
func taskState(t *testing.T, s *Store, id string) string {
	t.Helper()
	task, err := s.GetTask(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTask(%s): %v", id, err)
	}
	return task.State
}

// TestDeletedDocReleasesSlugAndNumber covers the workflow 044 §0 names as the
// reason delete exists: a wrong corpus number or a duplicate import is fixed by
// deleting and re-creating, which an unconditional unique index would refuse
// with a collision against a row the operator cannot see.
func TestDeletedDocReleasesSlugAndNumber(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	in := DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	}
	gone := mustCreateDoc(t, s, in)

	if _, err := createDoc(t, s, in); !errors.Is(err, ErrDocExists) {
		t.Fatalf("re-create while live: got %v, want ErrDocExists", err)
	}
	if err := deleteDoc(t, s, gone.ID, "stig", "imported twice"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}

	live := mustCreateDoc(t, s, in)
	if live.ID == gone.ID {
		t.Fatal("re-create returned the tombstoned row")
	}

	// All three resolveDocRef arms prefer the live row; the tombstone is
	// neither a shadow nor a rival that makes the corpus number ambiguous.
	for _, ref := range []string{
		"025-documents-in-the-backbone", "025-documents-in-the-backbone.md", "25", "P1-SPEC-25",
	} {
		if got := mustResolveDocRef(t, s, "p1", ref); got != live.ID {
			t.Fatalf("resolveDocRef(%q) = %d, want the live doc %d (tombstone is %d)",
				ref, got, live.ID, gone.ID)
		}
	}

	// The tombstone is still reachable by id — 044 §4's `lode show`.
	if _, err := s.GetDoc(ctx, gone.ID); err != nil {
		t.Fatalf("GetDoc on the tombstone: %v", err)
	}
}

// TestDeletedDocStillResolvesWhenNothingReplacedIt: the live-first preference
// is a preference, not a filter — with no live rival the tombstone still
// resolves, which is what keeps an undelete addressable by ref.
func TestDeletedDocStillResolvesWhenNothingReplacedIt(t *testing.T) {
	s := openDocStore(t)
	doc := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	if err := deleteDoc(t, s, doc.ID, "stig", "noise"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}
	for _, ref := range []string{"025-documents-in-the-backbone", "25", "P1-SPEC-25"} {
		if got := mustResolveDocRef(t, s, "p1", ref); got != doc.ID {
			t.Fatalf("resolveDocRef(%q) = %d, want the tombstoned doc %d", ref, got, doc.ID)
		}
	}
}

// TestRepointExternalEdgesSkipsDeletedDocs: the sweep finds referring
// documents for the caller rather than being named by them, which is the case
// 044 §4 says a tombstone stops. It must not rewrite a hidden document's edges
// or log an `edges` change against it.
func TestRepointExternalEdgesSkipsDeletedDocs(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()

	// The plan's `covers` names a spec that does not exist yet, so the edge is
	// stored unresolved (to_external).
	plan := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "plan", Slug: "025-plan-1", Body: planBody, CreatedBy: "stig",
	})
	if err := deleteDoc(t, s, plan.ID, "stig", "wrong corpus number"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}

	// Creating the target runs repointExternalEdges over the project.
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25,
		Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})

	out, _, err := s.ListDocEdges(ctx, plan.ID)
	if err != nil {
		t.Fatalf("ListDocEdges: %v", err)
	}
	for _, e := range out {
		if e.ToDoc != 0 {
			t.Fatalf("the tombstoned plan's %s edge was re-pointed at doc %d", e.Type, e.ToDoc)
		}
	}
	if got := docChangeFields(t, s, plan.ID); slices.Contains(got, "edges") {
		t.Fatalf("state_log for the tombstoned plan = %v, want no edges entry", got)
	}
}

// TestSupersedeReplacedDocsSkipsDeletedTarget: accepting a live successor must
// not flip a tombstoned target to superseded or log against it.
func TestSupersedeReplacedDocsSkipsDeletedTarget(t *testing.T) {
	s := openDocStore(t)
	ctx := t.Context()
	old := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 6, Slug: "006-old", Body: specBody,
		CreatedBy: "stig", Status: "accepted",
	})
	successor := mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-new", CreatedBy: "stig",
		Body: "---\nstatus: draft\nreplaces:\n  \".\":\n    - 006-old.md\n---\n\n" +
			"# New\n\n## 1. Scope {#sec-1}\n\na\n",
	})
	if err := deleteDoc(t, s, old.ID, "stig", "duplicate import"); err != nil {
		t.Fatalf("DeleteDoc: %v", err)
	}
	before := docChangeFields(t, s, old.ID)

	if _, _, err := acceptDoc(t, s, successor.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}

	got, err := s.GetDoc(ctx, old.ID)
	if err != nil {
		t.Fatalf("GetDoc: %v", err)
	}
	if got.Status != "accepted" {
		t.Fatalf("tombstoned target status = %q, want accepted (a tombstone is not mutated "+
			"by a sweep that found it)", got.Status)
	}
	if after := docChangeFields(t, s, old.ID); !slices.Equal(before, after) {
		t.Fatalf("state_log for the tombstoned target grew from %v to %v", before, after)
	}
}

// docChangeFields returns the `field` of every state_log row against a
// document, oldest first.
func docChangeFields(t *testing.T, s *Store, docID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT change->>'field' FROM state_log
		  WHERE entity_kind = 'doc' AND entity_id = $1 ORDER BY id`,
		strconv.FormatInt(docID, 10))
	if err != nil {
		t.Fatalf("read state_log for doc %d: %v", docID, err)
	}
	out, err := scanColumn[string](rows, "doc change fields")
	if err != nil {
		t.Fatalf("scan state_log for doc %d: %v", docID, err)
	}
	return out
}

// mustResolveDocRef runs resolveDocRef in a transaction and fails unless the
// reference resolved.
func mustResolveDocRef(t *testing.T, s *Store, project, ref string) int64 {
	t.Helper()
	var id int64
	err := s.Tx(t.Context(), func(tx *sql.Tx) error {
		var ok bool
		var err error
		id, ok, err = resolveDocRef(tx, project, ref)
		if err == nil && !ok {
			return fmt.Errorf("reference %q resolved to nothing", ref)
		}
		return err
	})
	if err != nil {
		t.Fatalf("resolveDocRef(%q): %v", ref, err)
	}
	return id
}

func taskIDsOf(tasks []model.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.ID
	}
	return out
}

func factIDs(facts []ProjectWorkFact) []string {
	out := make([]string, len(facts))
	for i, f := range facts {
		out[i] = f.Task.ID
	}
	return out
}

func docIDsOf(docs []model.Doc) []int64 {
	out := make([]int64, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}
