package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// TestAssignVsCrewRemovalRace pins the serialisation between assignment and
// crew removal (WL-374). RemoveParticipant's open-work guard reads tasks
// inside its own transaction; before requireCrewMember took FOR SHARE on the
// member's project_participants rows, an assignment contended on no row the
// removal held, so the two could commit at the same instant and leave a
// removed non-member owning open work. Whichever transaction goes second now
// blocks on those rows and then loses: a removal sees the fresh assignment,
// an assignment finds the crew rows gone.
func TestAssignVsCrewRemovalRace(t *testing.T) {
	for _, winner := range []string{"assign", "remove"} {
		t.Run(winner+"-wins", func(t *testing.T) {
			s := openTaskStore(t)
			ctx := t.Context()
			if err := s.CreateActor(ctx, "bob", "human", "Bob", false); err != nil {
				t.Fatalf("CreateActor bob: %v", err)
			}
			seedParticipant(t, s, "horndb", "bob", "member", false)
			task := createTask(t, s, taskTestNow, defaultTaskInput())

			// Both sides run on raw sql.Tx, outside RecordEvent, so their
			// event ids must be reserved separately to satisfy state_log's
			// FK on events.
			assignEventID, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "race.assign", nil, nil)
			if err != nil {
				t.Fatalf("reserve assign event id: %v", err)
			}
			removeEventID, _, err := s.RecordEvent(ctx, "cli", nextExt(t), "race.remove", nil, nil)
			if err != nil {
				t.Fatalf("reserve remove event id: %v", err)
			}

			call := map[string]func(*sql.Tx) error{
				"assign": func(tx *sql.Tx) error {
					return AssignTask(tx, taskTestNow, task.ID, "bob", assignEventID)
				},
				"remove": func(tx *sql.Tx) error {
					return RemoveParticipant(tx, taskTestNow, "horndb", "bob", "stig", removeEventID)
				},
			}
			loser := "remove"
			if winner == "remove" {
				loser = "assign"
			}

			winTx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatalf("begin winner tx: %v", err)
			}
			defer winTx.Rollback()
			if err := call[winner](winTx); err != nil {
				t.Fatalf("winner %s: %v", winner, err)
			}

			loserErr := make(chan error, 1)
			go func() {
				tx, err := s.db.BeginTx(ctx, nil)
				if err != nil {
					loserErr <- err
					return
				}
				if err := call[loser](tx); err != nil {
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
			err = <-loserErr
			if !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrNotFound) {
				t.Fatalf("loser %s: want a refusal, got %v", loser, err)
			}

			// The outcome the guard exists for: never a removed non-member
			// owning open work.
			got, gerr := s.GetTask(ctx, task.ID)
			if gerr != nil {
				t.Fatalf("GetTask: %v", gerr)
			}
			crew, cerr := s.ListParticipants(ctx, "horndb")
			if cerr != nil {
				t.Fatalf("ListParticipants: %v", cerr)
			}
			onCrew := false
			for _, m := range crew {
				if m.ActorID == "bob" {
					onCrew = true
				}
			}
			switch winner {
			case "assign":
				if got.Assignee != "bob" || !onCrew {
					t.Fatalf("assign won: assignee=%q onCrew=%v, want bob/true", got.Assignee, onCrew)
				}
				if !strings.Contains(err.Error(), task.ID) {
					t.Fatalf("losing removal must name the open work: %v", err)
				}
			case "remove":
				if got.Assignee != "" || onCrew {
					t.Fatalf("remove won: assignee=%q onCrew=%v, want empty/false", got.Assignee, onCrew)
				}
			}
		})
	}
}
