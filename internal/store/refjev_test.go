package store

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/sunstoneinstitute/worklode/internal/typesafe"
)

// TestJevClassify: JevPending takes the references with no cue hit and no
// classification; ClassifyNode writes a classified edge for a typed answer
// and none for references; reclassify replaces only classified edges and
// skips a pair a person repaired (WL-REQ-1857, WL-REQ-1858). The collector
// counts the rows (WL-REQ-1861).
func TestJevClassify(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	ctx := context.Background()
	for _, id := range []string{"P1-1", "P1-2", "P1-3", "P1-4"} {
		seedDocNoteTask(t, s, id)
	}
	const edges = `SELECT type, to_task, classification_id IS NOT NULL FROM task_edges WHERE from_task = $1 ORDER BY type, to_task`
	pending := func(reclassify bool) (b JevBatch) {
		t.Helper()
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			var err error
			b, err = JevPending(tx, "p1", "task", "P1-1", reclassify)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return b
	}
	targets := func(b JevBatch) (out []string) {
		lines := strings.Split(b.Node.Text, "\n")
		for _, r := range b.Refs {
			out = append(out, lines[r.Line-1])
		}
		return out
	}
	classify := func(b JevBatch, answers ...string) {
		t.Helper()
		as := map[string]typesafe.Answer{}
		for i, a := range answers {
			as["r"+string(rune('0'+i))] = typesafe.Answer{Choice: a, Confidence: 0.8, Probabilities: map[string]float64{a: 0.8}}
		}
		if err := s.Tx(ctx, func(tx *sql.Tx) error {
			got, err := ClassifyNode(tx, s.Now(), "task", "P1-1", b, as, "jev-test", seedEvent(t, tx))
			if err == nil && len(got) != len(answers) {
				t.Errorf("classified %d, want %d", len(got), len(answers))
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE tasks SET body = 'Intro.' || chr(10) || 'We need P1-2 done.' || chr(10) ||
			'This depends on P1-3.' || chr(10) || 'See P1-4.' WHERE id = 'P1-1'`); err != nil {
			return err
		}
		_, err := DeriveNode(tx, s.Now(), "p1", "task", "P1-1", seedEvent(t, tx))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	b := pending(false)
	if got, want := targets(b), []string{"We need P1-2 done.", "See P1-4."}; !slices.Equal(got, want) {
		t.Fatalf("pending lines = %q, want %q", got, want)
	}
	classify(b, "dependsOn", "references")
	if got, want := classifiedRows(t, s, edges, "P1-1"), []string{"dependsOn P1-2 true", "dependsOn P1-3 true"}; !slices.Equal(got, want) {
		t.Errorf("after classify: got %q, want %q", got, want)
	}
	if b := pending(false); len(b.Refs) != 0 {
		t.Errorf("pending after classify = %+v", b.Refs)
	}

	// An older jev version is re-asked; a person's edge to P1-4 stays.
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE ref_classifications SET classifier_version = 0 WHERE classifier = 'jev'`); err != nil {
			return err
		}
		return AddEdge(tx, s.Now(), "P1-1", "P1-4", "childOf", seedEvent(t, tx))
	}); err != nil {
		t.Fatal(err)
	}
	if b := pending(false); len(b.Refs) != 0 {
		t.Errorf("pending without reclassify = %+v", b.Refs)
	}
	classify(pending(true), "followUpTo", "dependsOn")
	if got, want := classifiedRows(t, s, edges, "P1-1"), []string{"childOf P1-4 false", "dependsOn P1-3 true", "dependsOn P1-4 true", "followUpTo P1-2 true"}; !slices.Equal(got, want) {
		t.Errorf("after reclassify: got %q, want %q", got, want)
	}

	// A pair a person repaired is not re-asked.
	if _, err := s.db.ExecContext(ctx, `UPDATE ref_classifications SET classifier_version = 0 WHERE classifier = 'jev'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		return RepairClassifications(tx, s.Now(), "task", "P1-1", "task", "P1-2", "followUpTo", true, "stig", 0)
	}); err != nil {
		t.Fatal(err)
	}
	if got, want := targets(pending(true)), []string{"See P1-4."}; !slices.Equal(got, want) {
		t.Errorf("pending after repair = %q, want %q", got, want)
	}

	const want = `
# HELP worklode_ref_classifications_total Inline reference classifications recorded (WL-REQ-1861), by classifier (cue, jev) and predicate, counted from ref_classifications at scrape time.
# TYPE worklode_ref_classifications_total counter
worklode_ref_classifications_total{classifier="cue",predicate="dependsOn"} 1
worklode_ref_classifications_total{classifier="jev",predicate="dependsOn"} 2
worklode_ref_classifications_total{classifier="jev",predicate="followUpTo"} 1
worklode_ref_classifications_total{classifier="jev",predicate="references"} 1
`
	if err := testutil.CollectAndCompare(&refClassificationCollector{db: s.db.DB}, strings.NewReader(want), "worklode_ref_classifications_total"); err != nil {
		t.Error(err)
	}
	const outcomes = `
# HELP worklode_ref_outcomes_total Inline reference classification outcomes (WL-REQ-1861), by classifier and outcome, counted from ref_classifications at scrape time. Every classification starts kept, so kept counts all of them; adjusted, rejected and missed are final and count the rows that reached them.
# TYPE worklode_ref_outcomes_total counter
worklode_ref_outcomes_total{classifier="cue",outcome="kept"} 1
worklode_ref_outcomes_total{classifier="jev",outcome="kept"} 4
worklode_ref_outcomes_total{classifier="jev",outcome="rejected"} 1
`
	if err := testutil.CollectAndCompare(&refClassificationCollector{db: s.db.DB}, strings.NewReader(outcomes), "worklode_ref_outcomes_total"); err != nil {
		t.Error(err)
	}
}
