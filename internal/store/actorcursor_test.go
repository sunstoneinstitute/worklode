package store

import (
	"context"
	"errors"
	"testing"
)

// seedCursorActor inserts the actor the cursor tests advance a boundary for.
func seedCursorActor(t *testing.T, s *Store, id string) {
	t.Helper()
	if _, err := s.db.ExecContext(context.Background(),
		`INSERT INTO actors (id, kind) VALUES ($1,'human')`, id); err != nil {
		t.Fatal(err)
	}
}

func TestActorEventCursor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := openTestStore(t)
	seedCursorActor(t, s, "stig")

	got, err := s.ActorEventCursor(ctx, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("cursor for an actor who never reviewed = %d, want 0", got)
	}

	advanced, err := s.AdvanceActorEventCursor(ctx, "stig", 40)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatal("advance to 40 from 0: advanced = false, want true")
	}
	got, err = s.ActorEventCursor(ctx, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if got != 40 {
		t.Fatalf("cursor after advancing to 40 = %d, want 40", got)
	}

	advanced, err = s.AdvanceActorEventCursor(ctx, "stig", 25)
	if err != nil {
		t.Fatal(err)
	}
	if advanced {
		t.Fatal("advance to 25 (below stored 40): advanced = true, want false (forward-only)")
	}
	got, err = s.ActorEventCursor(ctx, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if got != 40 {
		t.Fatalf("cursor after a rejected rewind to 25 = %d, want unchanged 40", got)
	}

	advanced, err = s.AdvanceActorEventCursor(ctx, "stig", 41)
	if err != nil {
		t.Fatal(err)
	}
	if !advanced {
		t.Fatal("advance to 41 from 40: advanced = false, want true")
	}

	if _, err := s.AdvanceActorEventCursor(ctx, "no-such-actor", 5); err == nil {
		t.Fatal("advance for unknown actor: got nil error, want the FK violation to surface")
	}

	if _, err := s.AdvanceActorEventCursor(ctx, "stig", 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("advance to 0: err = %v, want ErrInvalidInput", err)
	}

	if _, err := s.AdvanceActorEventCursor(ctx, "", 5); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("advance with empty actorID: err = %v, want ErrInvalidInput", err)
	}
}
