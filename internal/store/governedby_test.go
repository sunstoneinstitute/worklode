package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

// governingNumbers reads the CL numbers governing a task, in order.
func governingNumbers(t *testing.T, s *Store, taskID string) []int64 {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT c.number FROM task_governed_by g JOIN clauses c ON c.id = g.clause_id
		  WHERE g.task_id = $1 ORDER BY c.number`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func equalInt64s(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A plan covering P1-SPEC-1 sec-1 only. The spec is clauseDocV1 (clauses.go
// tests): sec-1 is clause 1, its child sec-1.1 is clause 2, sec-2 is clause 3.
const governedPlanBody = "---\nstatus: draft\ncovers: [P1-SPEC-1#sec-1]\n---\n# Plan\n\n## Tasks\n\n### Task 1 — First\n\n```yaml\nkind: feature\n```\n\nDo it.\n\n### Task 2 — Second\n\n```yaml\nkind: chore\n```\n\nDo more.\n"

// TestAcceptPlanGovernsMintedTasks: accepting a plan gives every task it mints
// a governedBy link to each clause the plan's covers edges reach (S2). A
// section-scoped edge reaches the clause at that anchor and the clauses
// arranged under it, so covering sec-1 governs by clauses 1 and 2 and leaves
// clause 3 out.
func TestAcceptPlanGovernsMintedTasks(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})

	var resolved int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM doc_edges WHERE from_doc = $1 AND type = 'covers' AND to_doc IS NOT NULL`, plan.ID).Scan(&resolved); err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Fatalf("plan's covers edge did not resolve to the spec (%d resolved rows); fix the covers ref in governedPlanBody", resolved)
	}

	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(minted) != 2 {
		t.Fatalf("minted %d tasks, want 2", len(minted))
	}
	for _, task := range minted {
		if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{1, 2}) {
			t.Errorf("%s governed by %v, want [1 2]", task.ID, got)
		}
	}

	var source string
	var linkVersion int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT source, clause_version FROM task_governed_by WHERE task_id = $1 ORDER BY clause_id LIMIT 1`,
		minted[0].ID).Scan(&source, &linkVersion); err != nil {
		t.Fatal(err)
	}
	if source != "plan" || linkVersion != 1 {
		t.Errorf("link source = %s v%d, want plan v1", source, linkVersion)
	}
}

// TestAcceptPlanSplitsCoveredDocOnFirstUse: a spec written before the clause
// tables existed has no arrangement yet; accepting a plan that covers it
// splits it first so the minted tasks still get their links.
func TestAcceptPlanSplitsCoveredDocOnFirstUse(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_clauses WHERE doc_id = $1`, spec.ID); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})
	_, minted, err := acceptDoc(t, s, plan.ID, "stig")
	if err != nil {
		t.Fatal(err)
	}
	if len(arrangementOf(t, s, spec.ID)) != 3 {
		t.Errorf("covered spec was not split on first use")
	}
	if got := governingNumbers(t, s, minted[0].ID); len(got) != 2 {
		t.Errorf("%s governed by %v, want two clauses", minted[0].ID, got)
	}
}

// TestEnsureClausesBackfillsAcceptedClauses: a spec that was accepted before
// the clause tables existed has no arrangement yet. Accepting a plan that
// covers it backfills the arrangement (ensureClauses), and because the spec
// is already accepted, its backfilled clauses must come out accepted too
// (S11), not stuck at the column default of draft.
func TestEnsureClausesBackfillsAcceptedClauses(t *testing.T) {
	s := openDocStore(t)
	spec := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, spec.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(context.Background(), `DELETE FROM doc_clauses WHERE doc_id = $1`, spec.ID); err != nil {
		t.Fatal(err)
	}
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: governedPlanBody, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, plan.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	arrangement := arrangementOf(t, s, spec.ID)
	if len(arrangement) != 3 {
		t.Fatalf("covered spec was not re-split on backfill: %d rows", len(arrangement))
	}
	for _, row := range arrangement {
		if row.Status != "accepted" {
			t.Errorf("clause %d status = %q, want accepted (doc %d was already accepted)", row.Number, row.Status, spec.ID)
		}
	}
}

// TestGovernAndUngovern: the architect adds and removes links by hand (S3);
// adding twice is a no-op, removing an absent link and naming an unknown
// task or clause are ErrNotFound.
func TestGovernAndUngovern(t *testing.T) {
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	task := createTask(t, s, time.Now(), TaskInput{ProjectID: "p1", Title: "a task", Body: "b", Priority: "medium", Kind: "bug", CreatedBy: "stig"})

	govern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.governed", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := ClauseIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Govern(tx, task.ID, id, "manual")
			})
		return err
	}
	if err := govern(3); err != nil {
		t.Fatal(err)
	}
	if err := govern(3); err != nil {
		t.Fatalf("second govern should be a no-op: %v", err)
	}
	if got := governingNumbers(t, s, task.ID); !equalInt64s(got, []int64{3}) {
		t.Errorf("governed by %v, want [3]", got)
	}
	if err := govern(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown clause: err = %v, want ErrNotFound", err)
	}

	list, err := s.GovernedBy(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Clause != "P1-CL-3" || list[0].Heading != "Two" || list[0].Source != "manual" ||
		list[0].ClauseVersion != 1 || list[0].Current != 1 {
		t.Errorf("GovernedBy = %+v", list)
	}

	ungovern := func(number int64) error {
		_, _, err := s.RecordEvent(t.Context(), "cli", nextExt(t), "task.ungoverned", nil,
			func(tx *sql.Tx, _ int64) error {
				id, err := ClauseIDByRef(tx, "P1", number)
				if err != nil {
					return err
				}
				return Ungovern(tx, task.ID, id)
			})
		return err
	}
	if err := ungovern(3); err != nil {
		t.Fatal(err)
	}
	if err := ungovern(3); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing an absent link: err = %v, want ErrNotFound", err)
	}
	if got := governingNumbers(t, s, task.ID); len(got) != 0 {
		t.Errorf("governed by %v after ungovern, want none", got)
	}
}
