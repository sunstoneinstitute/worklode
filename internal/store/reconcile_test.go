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

func TestPollCandidates(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map repo: %v", err)
	}

	// Seed through RecordEvent: Transition logs to state_log, whose event_id
	// is a NOT NULL FK to events (0001_baseline.up.sql:177), so it needs a
	// real event id.
	var inReview, merged string
	if _, _, err := s.RecordEvent(ctx, "cli", "seed-"+t.Name(), "test.seed", nil,
		func(tx *sql.Tx, eventID int64) error {
			now := s.Now()
			t1, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "a", Priority: "medium", Kind: "bug"}, eventID)
			if err != nil {
				return err
			}
			inReview = t1.ID
			t2, err := CreateTask(tx, now, TaskInput{ProjectID: "demo", Title: "b", Priority: "medium", Kind: "bug"}, eventID)
			if err != nil {
				return err
			}
			merged = t2.ID
			// t1: in_review with an open PR. t2: only a task commit, ready.
			if err := Transition(tx, now, inReview, "ready", "in_progress", eventID); err != nil {
				return err
			}
			if err := Transition(tx, now, inReview, "in_progress", "in_review", eventID); err != nil {
				return err
			}
			if _, err := UpsertPR(tx, PullRequest{
				Repo: "acme/app", Number: 12, Title: "fix", State: "open",
				HeadRef: inReview + "-fix",
				HeadSHA: "1111111111111111111111111111111111111111",
				URL:     "u", OpenedAt: now,
			}, ""); err != nil {
				return err
			}
			return InsertTaskCommit(tx, TaskCommit{
				TaskID: merged, Repo: "acme/app",
				SHA: "5555555555555555555555555555555555555555", Source: "pr", SeenAt: now,
			})
		}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	all, err := s.PollCandidates(ctx, "", "", nil)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("candidates = %+v; want both tasks", all)
	}

	one, err := s.PollCandidates(ctx, "", inReview, nil)
	if err != nil {
		t.Fatalf("task-bounded: %v", err)
	}
	if len(one) != 1 || one[0].TaskID != inReview || one[0].Repo != "acme/app" {
		t.Fatalf("task-bounded = %+v; want only %s", one, inReview)
	}

	none, err := s.PollCandidates(ctx, "other/repo", "", nil)
	if err != nil {
		t.Fatalf("repo-bounded: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("repo-bounded = %+v; want none", none)
	}

	unlanded, err := s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded: %v", err)
	}
	if len(unlanded) != 1 || unlanded[0] != "5555555555555555555555555555555555555555" {
		t.Fatalf("unlanded = %v; want the seeded sha", unlanded)
	}
	// Once the sha is on main, it is no longer unlanded.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		_, err := AppendMainCommit(tx, "acme/app", "5555555555555555555555555555555555555555", s.Now())
		return err
	}); err != nil {
		t.Fatalf("append main commit: %v", err)
	}
	unlanded, err = s.UnlandedTaskCommits(ctx, merged, "acme/app")
	if err != nil {
		t.Fatalf("unlanded after landing: %v", err)
	}
	if len(unlanded) != 0 {
		t.Fatalf("unlanded after landing = %v; want none", unlanded)
	}
}

func TestRepoIngestionHealth(t *testing.T) {
	s := OpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateProject(ctx, "demo", "Demo", "WL"); err != nil {
		t.Fatalf("create project: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/app"); err != nil {
		t.Fatalf("map acme/app: %v", err)
	}
	if err := s.AddRepo(ctx, "demo", "acme/silent"); err != nil {
		t.Fatalf("map acme/silent: %v", err)
	}

	recordGitHubEvent(t, s, "d-1", "issues.opened.ignored", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-2", "push", `{"repository":{"full_name":"acme/app"}}`)
	recordGitHubEvent(t, s, "d-3", "push.ignored", `{"repository":{"full_name":"acme/unmapped"}}`)

	all, err := s.RepoIngestionHealth(ctx, "")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("health rows = %d, want 2 mapped repos", len(all))
	}
	app, silent := all[0], all[1] // ordered by repo
	if app.Repo != "acme/app" || app.LastEventAt == nil || app.Unapplied != 2 {
		t.Fatalf("acme/app = %+v; want a last event and 2 unapplied", app)
	}
	if len(app.EventTypes) != 2 { // issues.opened.ignored, push
		t.Fatalf("acme/app event types = %v; want 2 distinct types", app.EventTypes)
	}
	if silent.Repo != "acme/silent" || silent.LastEventAt != nil || silent.Unapplied != 0 {
		t.Fatalf("acme/silent = %+v; want no events at all", silent)
	}
	if silent.MappedAt.IsZero() {
		t.Fatalf("mapped_at not populated for a fresh mapping")
	}

	one, err := s.RepoIngestionHealth(ctx, "acme/app")
	if err != nil {
		t.Fatalf("filtered health: %v", err)
	}
	if len(one) != 1 || one[0].Repo != "acme/app" {
		t.Fatalf("filtered health = %+v; want only acme/app", one)
	}

	senders, err := s.UnmappedSenders(ctx)
	if err != nil {
		t.Fatalf("unmapped senders: %v", err)
	}
	if len(senders) != 1 || senders[0].Repo != "acme/unmapped" || senders[0].Events != 1 {
		t.Fatalf("unmapped senders = %+v; want acme/unmapped with 1 event", senders)
	}
}
