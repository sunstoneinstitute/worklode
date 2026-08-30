package store

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// countTasks returns how many task rows exist. Promote creates a task before
// it writes the issue, so a triage call that loses a race and fails to roll
// back shows up here as an orphan.
func countTasks(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&n); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return n
}

// getIssue returns the single stored issue.
func getIssue(t *testing.T, s *Store) model.Issue {
	t.Helper()
	list, err := s.ListIssues(t.Context(), "", "")
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListIssues: got %d issues, want 1", len(list))
	}
	return list[0]
}

// waitForBlockedBackend blocks until a backend on this test's database is
// waiting on a lock — i.e. the loser transaction has read triage_state='new'
// and is now stuck on the winner's uncommitted row. That is the interleaving
// the guard exists for, and waiting for it is what makes the test deterministic
// rather than dependent on goroutine timing.
func waitForBlockedBackend(t *testing.T, s *Store) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM pg_stat_activity
			 WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&n); err != nil {
			t.Fatalf("poll pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no backend blocked on a lock within 10s")
}

// TestTriageLostUpdate holds one triage transaction open, lets a second one
// read triage_state='new' and block on the first's row lock, then commits the
// first. Under READ COMMITTED the loser's UPDATE re-evaluates its WHERE clause
// against the newly committed row: with `AND triage_state = 'new'` it matches
// nothing and fails; without it, it silently overwrites the winner's outcome.
func TestTriageLostUpdate(t *testing.T) {
	t.Parallel()
	cases := []struct{ winner, loser string }{
		{"dismiss", "promote"},
		{"promote", "link"},
		{"link", "dismiss"},
	}
	for _, tc := range cases {
		t.Run(tc.winner+"-beats-"+tc.loser, func(t *testing.T) {
			s := openInboxStore(t)
			is := defaultIssue()
			if err := upsertIssue(t, s, is); err != nil {
				t.Fatalf("upsert issue: %v", err)
			}
			existing := createTask(t, s, inboxTestNow, defaultTaskInput())

			ctx := t.Context()
			// The winner and loser races run on raw sql.Tx, outside RecordEvent,
			// so PromoteIssue's eventID must be reserved separately here to
			// satisfy state_log's FK on events.
			promoteEventID, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "race.promote", nil, nil)
			if err != nil {
				t.Fatalf("reserve promote event id: %v", err)
			}

			call := func(name string) func(*sql.Tx) error {
				switch name {
				case "promote":
					return func(tx *sql.Tx) error {
						_, err := PromoteIssue(tx, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), nil, promoteEventID)
						return err
					}
				case "dismiss":
					return func(tx *sql.Tx) error { return DismissIssue(tx, is.Repo, is.Number) }
				default:
					return func(tx *sql.Tx) error { return LinkIssue(tx, is.Repo, is.Number, existing.ID) }
				}
			}

			winTx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin winner tx: %v", err)
			}
			defer winTx.Rollback()
			if err := call(tc.winner)(winTx); err != nil {
				t.Fatalf("winner %s: %v", tc.winner, err)
			}

			loserErr := make(chan error, 1)
			go func() {
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					loserErr <- err
					return
				}
				if err := call(tc.loser)(tx); err != nil {
					tx.Rollback()
					loserErr <- err
					return
				}
				loserErr <- tx.Commit()
			}()

			waitForBlockedBackend(t, s)
			if err := winTx.Commit(); err != nil {
				t.Fatalf("commit winner tx: %v", err)
			}
			if err := <-loserErr; !errors.Is(err, ErrBadTransition) {
				t.Fatalf("loser %s: want ErrBadTransition, got %v", tc.loser, err)
			}

			got := getIssue(t, s)
			switch tc.winner {
			case "dismiss":
				if got.TriageState != "dismissed" || got.TaskID != "" {
					t.Fatalf("after dismiss won: triage_state=%q task_id=%v", got.TriageState, got.TaskID)
				}
			case "link":
				if got.TriageState != "promoted" || got.TaskID != existing.ID {
					t.Fatalf("after link won: triage_state=%q task_id=%v, want promoted/%s",
						got.TriageState, got.TaskID, existing.ID)
				}
			case "promote":
				if got.TriageState != "promoted" || got.TaskID == existing.ID {
					t.Fatalf("after promote won: triage_state=%q task_id=%v, want promoted with a new task",
						got.TriageState, got.TaskID)
				}
			}

			// The fixture task, plus one more only if the winning verb was promote.
			want := 1
			if tc.winner == "promote" {
				want = 2
			}
			if n := countTasks(t, s); n != want {
				t.Fatalf("tasks after race: got %d, want %d (a losing promote must roll its task back)", n, want)
			}
		})
	}
}

// TestPromoteIssueRace fires n concurrent promotes at one new issue: exactly
// one wins, every loser gets ErrBadTransition, and no loser leaves the task it
// created behind. Whether the losers fail on the read or on the guarded UPDATE
// depends on timing — TestTriageLostUpdate pins the second case down.
func TestPromoteIssueRace(t *testing.T) {
	t.Parallel()
	s := openInboxStore(t)
	is := defaultIssue()
	if err := upsertIssue(t, s, is); err != nil {
		t.Fatalf("upsert issue: %v", err)
	}

	const n = 8
	var wins, losses atomic.Int32
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := promoteIssue(t, s, inboxTestNow, is.Repo, is.Number, defaultTaskInput(), nil)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, ErrBadTransition):
				losses.Add(1)
			default:
				t.Errorf("goroutine %d: unexpected promote error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if wins.Load() != 1 || losses.Load() != n-1 {
		t.Fatalf("promotes: wins=%d losses=%d, want 1 and %d", wins.Load(), losses.Load(), n-1)
	}
	got := getIssue(t, s)
	if got.TriageState != "promoted" || got.TaskID == "" {
		t.Fatalf("issue after race: triage_state=%q task_id=%v", got.TriageState, got.TaskID)
	}
	if n := countTasks(t, s); n != 1 {
		t.Fatalf("tasks after race: got %d, want 1 (losers must roll back)", n)
	}
}
