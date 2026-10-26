package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/kg/iri"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// TestRecordClassification: the row is inserted and the event carries it as
// payload (WL-REQ-1853).
func TestRecordClassification(t *testing.T) {
	t.Parallel()
	s := OpenTestStore(t)
	ctx := context.Background()
	c := model.RefClassification{
		FromKind: model.NodeKind("task"), FromID: "P1-1", Field: "body", Line: 3,
		LineText: "blocked by WL-5", ToKind: model.NodeKind("task"), ToID: "P1-5",
		Classifier: "cue", ClassifierVersion: 1, Predicate: "blockedBy", Confidence: 0.9,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	var id, eventID int64
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		eventID = seedEvent(t, tx)
		var err error
		id, _, err = recordClassification(tx, c, eventID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var outcome, predicate string
	if err := s.db.QueryRowContext(ctx,
		`SELECT outcome, predicate FROM ref_classifications WHERE id = $1`, id).Scan(&outcome, &predicate); err != nil {
		t.Fatal(err)
	}
	if outcome != "kept" || predicate != "blockedBy" {
		t.Errorf("row = %q %q", outcome, predicate)
	}
	var caller []byte
	if err := s.db.QueryRowContext(ctx, `SELECT payload FROM events WHERE id = $1`, eventID).Scan(&caller); err != nil {
		t.Fatal(err)
	}
	if caller != nil {
		t.Errorf("caller's event payload changed: %s", caller)
	}
	var raw []byte
	if err := s.db.QueryRowContext(ctx,
		`SELECT payload FROM events WHERE type = 'wl:ReferenceClassified' AND external_id = $1`,
		fmt.Sprintf("ref_classification:%d", id)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if p["@type"] != "wl:ReferenceClassified" || p["id"] != float64(id) || p["predicate"] != "blockedBy" ||
		p["prov:wasInformedBy"] != iri.CURIE(iri.Event(eventID)) {
		t.Errorf("payload = %s", raw)
	}
}

// classifiedRows lists a typed table's rows from one node as
// "type to classified", for the cue tests.
func classifiedRows(t *testing.T, s *Store, query, from string) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), query, from)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, to string
		var classified bool
		if err := rows.Scan(&typ, &to, &classified); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %t", typ, to, classified))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// cueRow reads the newest cue classification from a node to a target.
func cueRow(t *testing.T, s *Store, fromKind, fromID, toID string) (predicate string, confidence float64, informedBy any) {
	t.Helper()
	var id int64
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT id, predicate, confidence FROM ref_classifications
		  WHERE classifier = 'cue' AND from_kind = $1 AND from_id = $2 AND to_id = $3 ORDER BY id DESC LIMIT 1`,
		fromKind, fromID, toID).Scan(&id, &predicate, &confidence); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT payload FROM events WHERE external_id = $1`, fmt.Sprintf("ref_classification:%d", id)).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return predicate, confidence, p["prov:wasInformedBy"]
}

// TestCueTaskDependsOn: derive turns "depends on" into a classified
// dependsOn edge informed by the derive's event; dropping the reference
// removes it and leaves a person's edge to the same task (WL-REQ-1853,
// WL-REQ-1857, WL-REQ-1858).
func TestCueTaskDependsOn(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	for _, id := range []string{"P1-1", "P1-2", "P1-3"} {
		seedDocNoteTask(t, s, id)
	}
	const edges = `SELECT type, to_task, classification_id IS NOT NULL FROM task_edges WHERE from_task = $1 ORDER BY type, to_task`
	var eventID int64
	derive := func(body string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE tasks SET body = $1 WHERE id = 'P1-1'`, body); err != nil {
				return err
			}
			eventID = seedEvent(t, tx)
			_, err := DeriveNode(tx, s.Now(), "p1", "task", "P1-1", eventID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return AddEdge(tx, s.Now(), "P1-1", "P1-2", "followUpTo", seedEvent(t, tx))
	}); err != nil {
		t.Fatal(err)
	}

	derive("Intro.\nThis depends on P1-2.\nSee P1-3.\n")
	if got, want := classifiedRows(t, s, edges, "P1-1"), []string{"dependsOn P1-2 true", "followUpTo P1-2 false"}; !slices.Equal(got, want) {
		t.Errorf("after hit: got %q, want %q", got, want)
	}
	pred, conf, informed := cueRow(t, s, "task", "P1-1", "P1-2")
	if pred != "dependsOn" || conf != 1 || informed != iri.CURIE(iri.Event(eventID)) {
		t.Errorf("classification = %s %v informed by %v", pred, conf, informed)
	}

	// Re-deriving the same text records no second classification.
	derive("Intro.\nThis depends on P1-2.\nSee P1-3.\n")
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM ref_classifications WHERE from_id = 'P1-1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("classifications after re-derive = %d, want 1", n)
	}

	derive("See P1-3.\n")
	if got, want := classifiedRows(t, s, edges, "P1-1"), []string{"followUpTo P1-2 false"}; !slices.Equal(got, want) {
		t.Errorf("after reference removed: got %q, want %q", got, want)
	}
}

// TestCueTaskDependsOnRefused: a hit the edge writer refuses (a cycle) leaves
// no edge and no classification, and the write goes through.
func TestCueTaskDependsOnRefused(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	seedDocNoteTask(t, s, "P1-1")
	seedDocNoteTask(t, s, "P1-2")
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := AddEdge(tx, s.Now(), "P1-2", "P1-1", "dependsOn", seedEvent(t, tx)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE tasks SET body = 'Depends on P1-2.' WHERE id = 'P1-1'`); err != nil {
			return err
		}
		return deriveNodeRefs(tx, "task", "P1-1", nil)
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM task_edges WHERE from_task = 'P1-1') + (SELECT count(*) FROM ref_classifications)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("edges plus classifications = %d, want 0", n)
	}
}

// TestCueTaskGovernedBy: "governed by" writes a classified task_governed_by
// row; with no event of its own the classification event names none; a
// person's link survives the reference leaving.
func TestCueTaskGovernedBy(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	seedDocNoteTask(t, s, "P1-1")
	r1 := strconv.FormatInt(ruleID(t, s, "P1", 1), 10)
	r2 := strconv.FormatInt(ruleID(t, s, "P1", 2), 10)
	const links = `SELECT 'governedBy', rule_id::text, classification_id IS NOT NULL FROM task_governed_by WHERE task_id = $1 ORDER BY rule_id`
	derive := func(body string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE tasks SET body = $1 WHERE id = 'P1-1'`, body); err != nil {
				return err
			}
			return deriveNodeRefs(tx, "task", "P1-1", nil)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return Govern(tx, "P1-1", ruleID(t, s, "P1", 2), "manual", false)
	}); err != nil {
		t.Fatal(err)
	}
	derive("Governed by P1-REQ-1 and P1-REQ-2.\n")
	if got, want := classifiedRows(t, s, links, "P1-1"), []string{"governedBy " + r1 + " true", "governedBy " + r2 + " false"}; !slices.Equal(got, want) {
		t.Errorf("after hit: got %q, want %q", got, want)
	}
	if pred, _, informed := cueRow(t, s, "task", "P1-1", r1); pred != "governedBy" || informed != nil {
		t.Errorf("classification = %s informed by %v", pred, informed)
	}
	derive("Nothing.\n")
	if got, want := classifiedRows(t, s, links, "P1-1"), []string{"governedBy " + r2 + " false"}; !slices.Equal(got, want) {
		t.Errorf("after reference removed: got %q, want %q", got, want)
	}
}

// TestCueRuleRefines: a rule body's "refines" writes a classified rule_edges
// row; a manual edge to the same rule survives the reference leaving.
func TestCueRuleRefines(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	r1, r2 := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 2)
	from := strconv.FormatInt(r2, 10)
	to := strconv.FormatInt(r1, 10)
	const edges = `SELECT type, to_rule::text, classification_id IS NOT NULL FROM rule_edges WHERE from_rule = $1::bigint ORDER BY type`
	derive := func(body string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			var v int
			if err := tx.QueryRow(`SELECT version FROM rules WHERE id = $1`, r2).Scan(&v); err != nil {
				return err
			}
			return deriveRuleInlineRefs(tx, "p1", r2, v, "", "", "H", body)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, r2, r1, "needs") }); err != nil {
		t.Fatal(err)
	}
	derive("Intro.\nThis refines P1-REQ-1.\n")
	if got, want := classifiedRows(t, s, edges, from), []string{"needs " + to + " false", "refines " + to + " true"}; !slices.Equal(got, want) {
		t.Errorf("after hit: got %q, want %q", got, want)
	}
	if pred, conf, _ := cueRow(t, s, "rule", from, to); pred != "refines" || conf != 1 {
		t.Errorf("classification = %s %v", pred, conf)
	}
	derive("Nothing.\n")
	if got, want := classifiedRows(t, s, edges, from), []string{"needs " + to + " false"}; !slices.Equal(got, want) {
		t.Errorf("after reference removed: got %q, want %q", got, want)
	}
}

// TestCueDocBlockedBy: a plan body's "runs after" another plan writes a
// classified blockedBy doc edge; a person's edge to the same plan survives
// the reference leaving.
func TestCueDocBlockedBy(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "a", Body: planTaskBody("", "A"), CreatedBy: "stig"})
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "b", Body: planTaskBody("", "B"), CreatedBy: "stig"})
	from, to := strconv.FormatInt(a.ID, 10), strconv.FormatInt(b.ID, 10)
	const edges = `SELECT type, to_doc::text, classification_id IS NOT NULL FROM doc_edges WHERE from_doc = $1::bigint ORDER BY type`
	derive := func(body string) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(`UPDATE docs SET body = $1 WHERE id = $2`, body, a.ID); err != nil {
				return err
			}
			return deriveDocRefs(tx, a.ID, nil)
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return AddDocEdge(tx, a.ID, b.ID, "requires", 0) }); err != nil {
		t.Fatal(err)
	}
	derive(fmt.Sprintf("# A\n\nThis runs after P1-PLAN-%d.\n", b.Number))
	if got, want := classifiedRows(t, s, edges, from), []string{"blockedBy " + to + " true", "requires " + to + " false"}; !slices.Equal(got, want) {
		t.Errorf("after hit: got %q, want %q", got, want)
	}
	if pred, _, _ := cueRow(t, s, "doc", from, to); pred != "blockedBy" {
		t.Errorf("classification = %s", pred)
	}
	derive("# A\n\nNothing.\n")
	if got, want := classifiedRows(t, s, edges, from), []string{"requires " + to + " false"}; !slices.Equal(got, want) {
		t.Errorf("after reference removed: got %q, want %q", got, want)
	}
}

// TestCueDocFrontmatterRebuild: a write that rebuilds a doc's frontmatter
// edges clears the classified edge, and the derive after it puts the edge back
// under the kept classification, so there is still exactly one row.
func TestCueDocFrontmatterRebuild(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "b", Body: planTaskBody("", "B"), CreatedBy: "stig"})
	body := fmt.Sprintf("---\nstatus: draft\n---\n\n# A\n\nThis runs after P1-PLAN-%d.\n", b.Number)
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "a", Body: body, CreatedBy: "stig"})
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := ReplaceDocEdges(tx, s.Now(), a.ID, nil, seedEvent(t, tx)); err != nil {
			return err
		}
		return deriveDocRefs(tx, a.ID, nil)
	}); err != nil {
		t.Fatal(err)
	}
	var classes, edges int
	var marked bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT (SELECT count(*) FROM ref_classifications WHERE from_kind = 'doc' AND from_id = $1),
		        (SELECT count(*) FROM doc_edges WHERE from_doc = $1::bigint AND type = 'blockedBy'),
		        (SELECT bool_and(e.classification_id = c.id) FROM doc_edges e, ref_classifications c
		          WHERE e.from_doc = $1::bigint AND e.type = 'blockedBy' AND c.from_id = $1)`,
		strconv.FormatInt(a.ID, 10)).Scan(&classes, &edges, &marked); err != nil {
		t.Fatal(err)
	}
	if classes != 1 || edges != 1 || !marked {
		t.Errorf("classifications = %d, blockedBy edges = %d, marked = %t; want 1, 1, true", classes, edges, marked)
	}
}

// TestAddDocEdge: the single-row doc edge writer sets classification_id and
// maps each refusal to its sentinel.
func TestAddDocEdge(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	a := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "a", Body: planTaskBody("", "A"), CreatedBy: "stig"})
	b := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "b", Body: planTaskBody("", "B"), CreatedBy: "stig"})
	var classID int64
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		classID, _, err = recordClassification(tx, model.RefClassification{
			FromKind: "doc", FromID: "1", Field: "body", Line: 1, LineText: "x", ToKind: "doc", ToID: "2",
			Classifier: "cue", ClassifierVersion: 1, Predicate: "requires", Confidence: 1, CreatedAt: time.Now().UTC(),
		}, 0)
		if err != nil {
			return err
		}
		return AddDocEdge(tx, a.ID, b.ID, "requires", classID)
	}); err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := s.db.QueryRowContext(ctx, `SELECT classification_id FROM doc_edges WHERE from_doc = $1 AND to_doc = $2`, a.ID, b.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != classID {
		t.Errorf("classification_id = %d, want %d", got, classID)
	}
	for _, c := range []struct {
		from, to int64
		typ      string
		want     error
	}{
		{a.ID, b.ID, "requires", ErrEdgeExists},
		{a.ID, a.ID, "requires", ErrInvalidInput},
		{a.ID, b.ID, "covers", ErrInvalidInput},
		{a.ID, 999999, "requires", ErrNotFound},
	} {
		err := s.Tx(ctx, func(tx *sql.Tx) error { return AddDocEdge(tx, c.from, c.to, c.typ, 0) })
		if !errors.Is(err, c.want) {
			t.Errorf("AddDocEdge(%d, %d, %s) = %v, want %v", c.from, c.to, c.typ, err, c.want)
		}
	}
}

// TestRefStats: rows group by classifier version, predicate and confidence
// tenth, 1.0 lands in the 0.9 bucket, rates divide by the row's total, and
// only the selected nodes count (WL-REQ-1859).
func TestRefStats(t *testing.T) {
	s := openDocStore(t)
	seedDocNoteTask(t, s, "P1-7")
	seedDocNoteTask(t, s, "P1-8")
	ins := func(from, classifier string, version int, predicate string, conf float64, outcome string) {
		t.Helper()
		if _, err := s.db.ExecContext(t.Context(), `INSERT INTO ref_classifications
			(from_kind, from_id, field, line, line_text, to_kind, to_id, classifier, classifier_version,
			 predicate, confidence, outcome, created_at)
			VALUES ('task', $1, 'body', 1, 'x', 'task', 'P1-9', $2, $3, $4, $5, $6, now())`,
			from, classifier, version, predicate, conf, outcome); err != nil {
			t.Fatal(err)
		}
	}
	ins("P1-7", "jev", 1, "blockedBy", 0.75, "kept")
	ins("P1-7", "jev", 1, "blockedBy", 0.71, "rejected")
	ins("P1-7", "jev", 1, "blockedBy", 0.79, "kept")
	ins("P1-7", "jev", 2, "blockedBy", 0.75, "adjusted")
	ins("P1-7", "cue", 1, "references", 1, "missed")
	ins("P1-8", "cue", 1, "references", 1, "kept")
	ins("P1-8", "jev", 1, "blockedBy", 0.75, "kept")

	got, err := s.RefStats(t.Context(), "p1", model.RefStatsParams{Kind: "task", Refs: []string{"P1-7"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []model.RefStatsRow{
		{Classifier: "cue", ClassifierVersion: 1, Predicate: "references", ConfidenceBucket: 0.9, Missed: 1, MissedRate: 1},
		{Classifier: "jev", ClassifierVersion: 1, Predicate: "blockedBy", ConfidenceBucket: 0.7, Kept: 2, Rejected: 1, KeptRate: 2.0 / 3, RejectedRate: 1.0 / 3},
		{Classifier: "jev", ClassifierVersion: 2, Predicate: "blockedBy", ConfidenceBucket: 0.7, Adjusted: 1, AdjustedRate: 1},
	}
	if !slices.Equal(got.Rows, want) {
		t.Errorf("rows = %+v\nwant   %+v", got.Rows, want)
	}
	all, err := s.RefStats(t.Context(), "p1", model.RefStatsParams{Kind: "task", All: true})
	if err != nil || len(all.Rows) != 3 || all.Rows[0].Kept != 1 || all.Rows[0].Missed != 1 {
		t.Errorf("all = %+v, %v", all.Rows, err)
	}
	if _, err := s.RefStats(t.Context(), "p1", model.RefStatsParams{Kind: "task"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("nothing selected: %v", err)
	}
}
