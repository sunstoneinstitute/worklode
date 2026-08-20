package store

import (
	"database/sql"
	"encoding/json"
	"errors"
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
