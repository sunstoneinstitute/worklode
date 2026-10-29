package store

import (
	"database/sql"
	"errors"
	"slices"
	"testing"
)

func openInvitationStore(t *testing.T) *Store {
	t.Helper()
	s := openTestStore(t)
	ctx := t.Context()
	if err := s.CreateProject(ctx, "p1", "P1", "INV1"); err != nil {
		t.Fatal(err)
	}
	for id, kind := range map[string]string{"ada": "human", "eva": "human", "bot": "agent"} {
		if err := s.CreateActor(ctx, id, kind, id, false); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func recordInv(t *testing.T, s *Store, typ string, apply func(tx *sql.Tx, eventID int64) error) error {
	t.Helper()
	_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), typ, nil, apply)
	return err
}

func invite(t *testing.T, s *Store, name, email, role string) (int64, error) {
	t.Helper()
	var id int64
	err := recordInv(t, s, "crew.invited", func(tx *sql.Tx, eventID int64) error {
		var err error
		id, err = InviteParticipant(tx, s.Now(), "p1", name, email, role, "ada", eventID)
		return err
	})
	return id, err
}

func TestInvitationLifecycle(t *testing.T) {
	t.Parallel()
	s := openInvitationStore(t)
	ctx := t.Context()

	invID, err := invite(t, s, "Dr. Eva Ng", "eva@example.org", "domain-expert")
	if err != nil {
		t.Fatal(err)
	}

	// Linking adds the Crew row and preserves the invitation (WL-REQ-1605).
	err = recordInv(t, s, "crew.invite_linked", func(tx *sql.Tx, eventID int64) error {
		return LinkInvitation(tx, s.Now(), invID, "eva", "ada", eventID)
	})
	if err != nil {
		t.Fatal(err)
	}
	invs, err := s.ListInvitations(ctx, "p1")
	if err != nil || len(invs) != 1 || invs[0].LinkedActor != "eva" || invs[0].LinkedAt.IsZero() {
		t.Fatalf("invitation must survive linking: %+v (%v)", invs, err)
	}
	crew, err := s.ListParticipants(ctx, "p1")
	if err != nil || len(crew) != 1 || crew[0].ActorID != "eva" ||
		!slices.Equal(crew[0].Roles, []string{"domain-expert"}) {
		t.Fatalf("linked member wrong: %+v (%v)", crew, err)
	}

	// A linked invitation cannot be withdrawn or linked again.
	err = recordInv(t, s, "crew.invite_withdrawn", func(tx *sql.Tx, eventID int64) error {
		return WithdrawInvitation(tx, s.Now(), invID, "ada", eventID)
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("withdraw after link: got %v", err)
	}
	err = recordInv(t, s, "crew.invite_linked", func(tx *sql.Tx, eventID int64) error {
		return LinkInvitation(tx, s.Now(), invID, "eva", "ada", eventID)
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("link twice: got %v", err)
	}
}

func TestInvitationRefusals(t *testing.T) {
	t.Parallel()
	s := openInvitationStore(t)

	if _, err := invite(t, s, "A", "a@example.org", "domain-expert"); err != nil {
		t.Fatal(err)
	}
	if _, err := invite(t, s, "A2", "A@Example.org", "editor"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate open email: got %v", err)
	}
	if _, err := invite(t, s, "B", "b@example.org", "wizard"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown role: got %v", err)
	}
	if _, err := invite(t, s, "  ", "c@example.org", "member"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("blank name: got %v", err)
	}
	// Empty email stores NULL, so two email-less invitations coexist.
	if _, err := invite(t, s, "D", "", "member"); err != nil {
		t.Fatal(err)
	}
	if _, err := invite(t, s, "E", "", "member"); err != nil {
		t.Fatal(err)
	}

	err := recordInv(t, s, "crew.invite_linked", func(tx *sql.Tx, eventID int64) error {
		return LinkInvitation(tx, s.Now(), 999999, "eva", "ada", eventID)
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("link unknown invitation: got %v", err)
	}
}

func TestInvitationWithdraw(t *testing.T) {
	t.Parallel()
	s := openInvitationStore(t)
	id, err := invite(t, s, "A", "a@example.org", "member")
	if err != nil {
		t.Fatal(err)
	}
	err = recordInv(t, s, "crew.invite_withdrawn", func(tx *sql.Tx, eventID int64) error {
		return WithdrawInvitation(tx, s.Now(), id, "ada", eventID)
	})
	if err != nil {
		t.Fatal(err)
	}
	invs, err := s.ListInvitations(t.Context(), "p1")
	if err != nil || len(invs) != 0 {
		t.Fatalf("withdrawn invitation must be gone: %+v (%v)", invs, err)
	}
}

func TestInvitationLinkRefusesAgent(t *testing.T) {
	t.Parallel()
	s := openInvitationStore(t)
	id, err := invite(t, s, "A", "a@example.org", "member")
	if err != nil {
		t.Fatal(err)
	}
	err = recordInv(t, s, "crew.invite_linked", func(tx *sql.Tx, eventID int64) error {
		return LinkInvitation(tx, s.Now(), id, "bot", "ada", eventID)
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("link agent: got %v", err)
	}
	invs, _ := s.ListInvitations(t.Context(), "p1")
	if len(invs) != 1 || invs[0].LinkedActor != "" {
		t.Fatalf("invitation must stay open: %+v", invs)
	}
}
