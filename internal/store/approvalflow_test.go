package store_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/api"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// storySnapshot is the shipped `story` flow wrapped in a snapshot, with the
// given reviewer template. Reading it from api.LoadApprovalFlows keeps the
// test honest about the flow the instance actually ships.
func storySnapshot(t *testing.T, reviewers map[string]string) model.ApprovalFlowSnapshot {
	t.Helper()
	flows, err := api.LoadApprovalFlows("")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range flows {
		if f.Name == "story" {
			return model.ApprovalFlowSnapshot{Flow: f, Reviewers: reviewers}
		}
	}
	t.Fatal("shipped flows do not include story")
	return model.ApprovalFlowSnapshot{}
}

// flowFixture creates a project with one deliverable named `Methodology`, the
// `worklode` service actor the created_by FK needs, and an open transaction.
func flowFixture(t *testing.T) (*sql.Tx, string, string) {
	t.Helper()
	s := store.OpenTestStore(t)
	ctx := t.Context()
	if err := s.EnsureServiceActor(ctx, store.FlowActorID, "approval flow rules"); err != nil {
		t.Fatal(err)
	}
	const projectID = "story-proj"
	if err := s.CreateProject(ctx, projectID, "Story", "STO"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.DBForTests().Begin()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	d, err := store.CreateDeliverable(tx, time.Now().UTC(), store.DeliverableInput{
		ProjectID: projectID, Name: "Methodology",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx, projectID, d.ID
}

func TestMaterializeForProjectIsIdempotent(t *testing.T) {
	t.Parallel()
	tx, projectID, delID := flowFixture(t)
	snap := storySnapshot(t, nil)
	now := time.Now().UTC()

	n, err := store.MaterializeForProject(tx, now, projectID, snap)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("first materialize inserted %d rows, want 2", n)
	}
	again, err := store.MaterializeForProject(tx, now, projectID, snap)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Errorf("second materialize inserted %d rows, want 0", again)
	}

	rows, err := tx.Query(
		`SELECT lane, state, subject_revision, COALESCE(required_role, ''),
		        COALESCE(required_actor, ''), COALESCE(created_by, '')
		   FROM approvals WHERE entity_kind = 'deliverable' AND entity_id = $1
		  ORDER BY lane`, delID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type row struct{ lane, state, rev, role, actor, createdBy string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.lane, &r.state, &r.rev, &r.role, &r.actor, &r.createdBy); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []row{
		{"methodology/domain-expert", "awaiting", "", "domain-experts", "", store.FlowActorID},
		{"methodology/science-lead", "awaiting", "", "science-leads", "", store.FlowActorID},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d approval rows %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMaterializeForEntityUsesTheReviewerTemplate(t *testing.T) {
	t.Parallel()
	tx, _, delID := flowFixture(t)
	snap := storySnapshot(t, map[string]string{"methodology/science-lead": "alice"})
	if err := seedActor(t, tx, "alice"); err != nil {
		t.Fatal(err)
	}

	n, err := store.MaterializeForEntity(tx, time.Now().UTC(), snap,
		"deliverable", delID, "Methodology")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("materialized %d rows, want 2", n)
	}
	for _, c := range []struct{ lane, role, actor string }{
		{"methodology/science-lead", "", "alice"},
		{"methodology/domain-expert", "domain-experts", ""},
	} {
		var role, actor string
		if err := tx.QueryRow(
			`SELECT COALESCE(required_role, ''), COALESCE(required_actor, '')
			   FROM approvals WHERE entity_id = $1 AND lane = $2`,
			delID, c.lane).Scan(&role, &actor); err != nil {
			t.Fatalf("lane %s: %v", c.lane, err)
		}
		if role != c.role || actor != c.actor {
			t.Errorf("lane %s: role %q actor %q, want role %q actor %q",
				c.lane, role, actor, c.role, c.actor)
		}
	}
}

func TestMaterializeForEntityIgnoresAnUnmatchedName(t *testing.T) {
	t.Parallel()
	tx, _, _ := flowFixture(t)
	n, err := store.MaterializeForEntity(tx, time.Now().UTC(), storySnapshot(t, nil),
		"deliverable", "STO-DEL-99", "Interview notes")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("materialized %d rows for an unmatched name, want 0", n)
	}
}

func TestProjectApprovalFlowRoundTrips(t *testing.T) {
	t.Parallel()
	tx, projectID, _ := flowFixture(t)

	got, err := store.ProjectApprovalFlow(tx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("unstamped project has snapshot %+v, want nil", got)
	}

	snap := storySnapshot(t, map[string]string{"methodology/science-lead": "alice"})
	if err := store.SetProjectApprovalFlow(tx, projectID, snap); err != nil {
		t.Fatal(err)
	}
	got, err = store.ProjectApprovalFlow(tx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("stamped project has no snapshot")
	}
	if got.Flow.Name != snap.Flow.Name || got.Flow.Rev != snap.Flow.Rev ||
		len(got.Flow.Requirements) != len(snap.Flow.Requirements) ||
		got.Reviewers["methodology/science-lead"] != "alice" {
		t.Errorf("round-tripped snapshot = %+v, want %+v", *got, snap)
	}

	var name, rev string
	if err := tx.QueryRow(
		`SELECT approval_flow_name, approval_flow_rev FROM projects WHERE id = $1`,
		projectID).Scan(&name, &rev); err != nil {
		t.Fatal(err)
	}
	if name != "story" || rev != snap.Flow.Rev {
		t.Errorf("denormalized columns = %q/%q, want story/%s", name, rev, snap.Flow.Rev)
	}
}

// seedActor inserts a human actor the required_actor FK can point at.
func seedActor(t *testing.T, tx *sql.Tx, id string) error {
	t.Helper()
	_, err := tx.Exec(
		`INSERT INTO actors (id, kind, display_name, admin) VALUES ($1, 'human', $1, false)`, id)
	return err
}
