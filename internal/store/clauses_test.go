package store

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// arranged is one row of a document's arrangement as the tests read it back.
type arranged struct {
	Position, Depth int
	Number          int64
	ClauseVersion   int
	Anchor          string
	Status          string
}

func arrangementOf(t *testing.T, s *Store, docID int64) []arranged {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT dc.position, dc.depth, c.number, dc.clause_version, dc.anchor, c.status
		   FROM doc_clauses dc JOIN clauses c ON c.id = dc.clause_id
		  WHERE dc.doc_id = $1 ORDER BY dc.position`, docID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []arranged
	for rows.Next() {
		var a arranged
		if err := rows.Scan(&a.Position, &a.Depth, &a.Number, &a.ClauseVersion, &a.Anchor, &a.Status); err != nil {
			t.Fatal(err)
		}
		out = append(out, a)
	}
	return out
}

func assertArrangement(t *testing.T, got, want []arranged) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("arrangement has %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

const clauseDocV1 = "---\nstatus: draft\n---\n# T\n\nIntro.\n\n## 1. One {#sec-1}\n\nA.\n\n### 1.1 Sub {#sec-1.1}\n\nB.\n\n## 2. Two {#sec-2}\n\nC.\n"

// TestSyncClausesMintsVersionsAndKeepsIdentity: a create mints one clause per
// anchored section in order; an edit of a draft rewrites the changed clause's
// draft version in place, keeps the others untouched, and mints a new number for a new
// section; an anchor change with the same heading keeps the clause (S8 to
// S10, S20).
func TestSyncClausesMintsVersionsAndKeepsIdentity(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
	})

	v2 := strings.Replace(clauseDocV1, "C.\n", "C changed.\n", 1) + "\n## 3. Three {#sec-3}\n\nD.\n"
	if _, err := updateDocBody(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "draft"},
		{1, 3, 2, 1, "sec-1.1", "draft"},
		{2, 2, 3, 1, "sec-2", "draft"},
		{3, 2, 4, 1, "sec-3", "draft"},
	})

	v3 := strings.Replace(v2, "## 3. Three {#sec-3}", "## 2a. Three {#sec-2a}", 1)
	if _, err := updateDocBody(t, s, d.ID, v3); err != nil {
		t.Fatal(err)
	}
	got := arrangementOf(t, s, d.ID)
	if len(got) != 4 || got[3].Number != 4 || got[3].Anchor != "sec-2a" || got[3].ClauseVersion != 1 {
		t.Errorf("anchor change with the same heading should keep clause 4 at v1: %+v", got)
	}

	var versions int
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM clause_versions`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 4 {
		t.Errorf("clause_versions has %d rows, want 4 (four clauses, draft edits rewrite in place)", versions)
	}
	c, err := s.GetClause(context.Background(), "P1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || !strings.Contains(c.Body, "C changed.") {
		t.Errorf("draft edit should rewrite v1 in place: %+v", c)
	}
}

// TestReviseClauseLocksAcceptedVersions: once a document is accepted its
// clauses' versions are locked; landing a revision with changed text becomes
// the clause's version 2, accepted with the revision, and the v1 text is
// still readable (S10, S35).
func TestReviseClauseLocksAcceptedVersions(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	if err := reviseDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	v2 := strings.Replace(clauseDocV1, "C.\n", "C changed.\n", 1)
	if err := updateRevision(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}
	if _, err := acceptRevision(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	assertArrangement(t, arrangementOf(t, s, d.ID), []arranged{
		{0, 2, 1, 1, "sec-1", "accepted"},
		{1, 3, 2, 1, "sec-1.1", "accepted"},
		{2, 2, 3, 2, "sec-2", "accepted"},
	})
	var v1 string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT body FROM clause_versions WHERE version = 1 AND clause_id = (SELECT id FROM clauses WHERE number = 3)`).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(v1, "changed") {
		t.Errorf("accepted v1 text must not change: %q", v1)
	}
}

// TestSyncClausesSkipsPlans: plans carry no anchors and arrange nothing in
// this increment.
func TestSyncClausesSkipsPlans(t *testing.T) {
	s := openDocStore(t)
	body := "---\nstatus: draft\ncovers: NO-SPEC\n---\n# P\n\n## Tasks\n\n### Task 1 — Do it\n\n```yaml\nkind: chore\n```\n\nText.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "p", Body: body, CreatedBy: "stig"})
	if got := arrangementOf(t, s, d.ID); len(got) != 0 {
		t.Errorf("plan arranged %d clauses, want 0", len(got))
	}
}

// TestAcceptDocAcceptsItsClauses: accepting a document accepts every clause it
// arranges and writes nothing else (S11).
func TestAcceptDocAcceptsItsClauses(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig", Owner: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatal(err)
	}
	got := arrangementOf(t, s, d.ID)
	if len(got) == 0 {
		t.Fatal("no clauses arranged")
	}
	for _, a := range got {
		if a.Status != "accepted" {
			t.Errorf("clause %d status = %s, want accepted", a.Number, a.Status)
		}
	}
}

// TestSyncClausesKeepsIdentityAcrossAnchorRenumber: inserting a section
// shifts every anchor after it (`--update-section-anchors` and hand
// renumbering both rewrite anchors from tree position). Matching sections to
// prior clauses one at a time in document order lets a later section's
// heading-fallback match steal an earlier section's clause once that
// earlier section's own anchor match already claimed a different clause —
// syncClauses resolves matches over the whole document first so that can't
// happen.
func TestSyncClausesKeepsIdentityAcrossAnchorRenumber(t *testing.T) {
	s := openDocStore(t)
	v1 := "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\nA.\n\n## 2. B {#sec-2}\n\nB.\n"
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: v1, CreatedBy: "stig"})

	v2 := "---\nstatus: draft\n---\n# T\n\n## 1. A {#sec-1}\n\nA.\n\n## 2. New {#sec-2}\n\nNew.\n\n## 3. B {#sec-3}\n\nB.\n"
	if _, err := updateDocBody(t, s, d.ID, v2); err != nil {
		t.Fatal(err)
	}

	got := arrangementOf(t, s, d.ID)
	byAnchor := map[string]arranged{}
	for _, a := range got {
		byAnchor[a.Anchor] = a
	}
	if b := byAnchor["sec-3"]; b.Number != 2 || b.ClauseVersion != 1 {
		t.Errorf("B should keep clause 2 at v1: %+v", b)
	}
	if n := byAnchor["sec-2"]; n.Number != 3 || n.ClauseVersion != 1 {
		t.Errorf("New should mint clause 3 at v1: %+v", n)
	}
}

// TestGetClause reads a clause by project key and number with its current
// text and the documents arranging it.
func TestGetClause(t *testing.T) {
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: clauseDocV1, CreatedBy: "stig"})
	c, err := s.GetClause(context.Background(), "P1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.Ref != "P1-CL-2" || c.Heading != "Sub" || c.Body != "\nB.\n\n" || c.Status != "draft" || c.Version != 1 {
		t.Errorf("clause = %+v", c)
	}
	if len(c.ArrangedIn) != 1 || c.ArrangedIn[0].Doc != d.ID || c.ArrangedIn[0].DocRef != "P1-SPEC-1" ||
		c.ArrangedIn[0].Anchor != "sec-1.1" || c.ArrangedIn[0].Depth != 3 || c.ArrangedIn[0].Position != 1 {
		t.Errorf("arranged_in = %+v", c.ArrangedIn)
	}
	if _, err := s.GetClause(context.Background(), "P1", 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing clause: err = %v, want ErrNotFound", err)
	}
}
