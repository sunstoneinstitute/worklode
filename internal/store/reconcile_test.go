package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// recordGitHubEvent inserts one github-source event with the given type and
// payload, returning its id. apply is nil: applied_at stays NULL, as it does
// for a real *.ignored delivery.
func recordGitHubEvent(t *testing.T, s *Store, externalID, typ, payload string) int64 {
	t.Helper()
	id, _, err := s.RecordEvent(context.Background(), "github", externalID, typ,
		[]byte(payload), nil)
	if err != nil {
		t.Fatalf("record event %s: %v", externalID, err)
	}
	return id
}

func TestMarkEventAppliedAndUnappliedQuery(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()

	early := recordGitHubEvent(t, s, "d-1", "issues.opened.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	late := recordGitHubEvent(t, s, "d-2", "push.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	other := recordGitHubEvent(t, s, "d-3", "push.ignored",
		`{"repository":{"full_name":"acme/other"}}`)
	applied := recordGitHubEvent(t, s, "d-4", "issues.opened.ignored",
		`{"repository":{"full_name":"acme/app"}}`)
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return MarkEventApplied(tx, applied, s.Now())
	}); err != nil {
		t.Fatalf("mark applied: %v", err)
	}
	// A non-github source is never a replay candidate.
	if _, _, err := s.RecordEvent(ctx, "cli", "d-5", "task.created", nil, nil); err != nil {
		t.Fatalf("record cli event: %v", err)
	}

	got, err := s.UnappliedGitHubEvents(ctx, UnappliedFilter{})
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	if ids := eventIDs(got); len(ids) != 3 || ids[0] != early || ids[1] != late || ids[2] != other {
		t.Fatalf("unfiltered ids = %v; want [%d %d %d] in id order", ids, early, late, other)
	}

	got, err = s.UnappliedGitHubEvents(ctx, UnappliedFilter{Repo: "acme/app"})
	if err != nil {
		t.Fatalf("repo filter: %v", err)
	}
	if ids := eventIDs(got); len(ids) != 2 || ids[0] != early || ids[1] != late {
		t.Fatalf("repo filter ids = %v; want [%d %d]", ids, early, late)
	}

	cutoff := time.Now().Add(time.Hour)
	got, err = s.UnappliedGitHubEvents(ctx, UnappliedFilter{Since: &cutoff})
	if err != nil {
		t.Fatalf("since filter: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("since-in-the-future returned %d events; want 0", len(got))
	}
}

func eventIDs(evs []Event) []int64 {
	out := make([]int64, len(evs))
	for i, e := range evs {
		out[i] = e.ID
	}
	return out
}
