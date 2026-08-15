package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// recordMerge runs RecordLocalMerge in its own committed transaction and
// returns the outcomes keyed by task id.
func recordMerge(t *testing.T, s *Store, repo, sha string, tasks []string) map[string]string {
	t.Helper()
	var got map[string]string
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		out, err := RecordLocalMerge(tx, time.Now(), repo, sha, tasks, seedEvent(t, tx))
		if err != nil {
			return err
		}
		got = map[string]string{}
		for _, o := range out {
			got[o.TaskID] = o.Result
		}
		return nil
	}); err != nil {
		t.Fatalf("RecordLocalMerge: %v", err)
	}
	return got
}

// seedEvent inserts a throwaway event row and returns its id: Transition
// records the event that caused it, so a delivery needs one to point at.
func seedEvent(t *testing.T, tx *sql.Tx) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRow(
		`INSERT INTO events (source, external_id, type, received_at)
		 VALUES ('cli', gen_random_uuid()::text, 'merge.local', now()) RETURNING id`).Scan(&id); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return id
}

// TestRecordLocalMergeAdvances is the whole point: a merge nobody pushed to
// GitHub still moves the task to merged, through the same three store calls
// the default-branch push webhook makes.
func TestRecordLocalMergeAdvances(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)

	got := recordMerge(t, s, "acme/app", "abc1", []string{taskID})
	if got[taskID] != LocalMergeAdvanced {
		t.Fatalf("result = %q, want %q", got[taskID], LocalMergeAdvanced)
	}
	if state := taskStateNow(t, s, taskID); state != "merged" {
		t.Fatalf("state = %q, want merged", state)
	}
}

// TestRecordLocalMergeDuplicate: the second reporter of the same merge — a
// webhook, or the same clone reporting twice — changes nothing and says so.
// This is the healthy steady state, not an error.
func TestRecordLocalMergeDuplicate(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)

	if got := recordMerge(t, s, "acme/app", "abc1", []string{taskID}); got[taskID] != LocalMergeAdvanced {
		t.Fatalf("first report = %q, want %q", got[taskID], LocalMergeAdvanced)
	}
	got := recordMerge(t, s, "acme/app", "abc1", []string{taskID})
	if got[taskID] != LocalMergeDuplicate {
		t.Fatalf("second report = %q, want %q", got[taskID], LocalMergeDuplicate)
	}
	if state := taskStateNow(t, s, taskID); state != "merged" {
		t.Fatalf("state = %q, want merged (unchanged)", state)
	}
	// One attribution row, not two.
	var n int
	if err := s.db.QueryRow(
		`SELECT count(*) FROM task_commits WHERE task_id = $1 AND sha = 'abc1'`, taskID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("task_commits rows = %d, want 1", n)
	}
}

// TestRecordLocalMergeUnknownTask: a correlation miss is reported, never an
// error — a laptop's guess about which branches landed must not fail the
// whole report, nor abort the transaction the way an FK violation would.
func TestRecordLocalMergeUnknownTask(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)

	got := recordMerge(t, s, "acme/app", "abc1", []string{taskID, "P1-404"})
	if got["P1-404"] != LocalMergeUnknownTask {
		t.Fatalf("unknown task result = %q, want %q", got["P1-404"], LocalMergeUnknownTask)
	}
	if got[taskID] != LocalMergeAdvanced {
		t.Fatalf("real task alongside an unknown one = %q, want %q", got[taskID], LocalMergeAdvanced)
	}
	if state := taskStateNow(t, s, taskID); state != "merged" {
		t.Fatalf("state = %q, want merged", state)
	}
}

// TestRecordLocalMergeSourceIsLocalMerge: provenance is the point of the
// separate source value — the log must say a laptop asserted this, not a
// signed webhook.
func TestRecordLocalMergeSourceIsLocalMerge(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)
	recordMerge(t, s, "acme/app", "abc1", []string{taskID})

	var source string
	if err := s.db.QueryRow(
		`SELECT source FROM task_commits WHERE task_id = $1 AND sha = 'abc1'`, taskID).Scan(&source); err != nil {
		t.Fatal(err)
	}
	if source != "local_merge" {
		t.Fatalf("source = %q, want local_merge", source)
	}
}

// TestRecordLocalMergeRepeatedTaskID: a caller naming the same task twice in
// one report gets one row and one outcome, not two.
func TestRecordLocalMergeRepeatedTaskID(t *testing.T) {
	s := OpenTestStore(t)
	taskID := seedDeliveryTask(t, s)

	var outcomes int
	if err := s.Tx(context.Background(), func(tx *sql.Tx) error {
		out, err := RecordLocalMerge(tx, time.Now(), "acme/app", "abc1",
			[]string{taskID, taskID}, seedEvent(t, tx))
		outcomes = len(out)
		return err
	}); err != nil {
		t.Fatalf("RecordLocalMerge: %v", err)
	}
	if outcomes != 1 {
		t.Fatalf("outcomes = %d, want 1", outcomes)
	}
}

func taskStateNow(t *testing.T, s *Store, taskID string) string {
	t.Helper()
	var state string
	if err := s.db.QueryRow(`SELECT state FROM tasks WHERE id = $1`, taskID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}
