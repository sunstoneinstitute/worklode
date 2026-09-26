package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// snapshotRows returns query's rows as JSON text, one per row, so a live
// table and its snapshot can be compared column for column.
func snapshotRows(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return collectRowsT(t, rows)
}

func collectRowsT(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer rows.Close()
	var out []string
	for rows.Next() {
		var j string
		if err := rows.Scan(&j); err != nil {
			t.Fatal(err)
		}
		out = append(out, j)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

const (
	liveEdgesSQL = `SELECT json_build_object('from_anchor', e.from_anchor, 'type', e.type, 'to_doc', e.to_doc,
	        'to_anchor', e.to_anchor, 'to_external', e.to_external, 'coverage', e.coverage, 'to_rule', e.to_rule,
	        'completed_with', (SELECT jsonb_agg(CASE WHEN w.to_doc IS NOT NULL THEN jsonb_build_object('to_doc', w.to_doc)
	                                               ELSE jsonb_build_object('to_external', w.to_external) END ORDER BY w.position)
	                             FROM doc_coverage_completed_with w WHERE w.edge_id = e.id))::text
	   FROM doc_edges e WHERE e.from_doc = $1
	  ORDER BY e.type, coalesce(e.to_rule, 0), coalesce(e.to_doc, 0), coalesce(e.to_external, '')`
	snapEdgesSQL = `SELECT json_build_object('from_anchor', e.from_anchor, 'type', e.type, 'to_doc', e.to_doc,
	        'to_anchor', e.to_anchor, 'to_external', e.to_external, 'coverage', e.coverage, 'to_rule', e.to_rule,
	        'completed_with', e.completed_with)::text
	   FROM doc_edge_versions e WHERE e.doc_id = $1 AND e.version = $2
	  ORDER BY e.type, coalesce(e.to_rule, 0), coalesce(e.to_doc, 0), coalesce(e.to_external, '')`
	liveRulesSQL = `SELECT json_build_object('position', position, 'rule_id', rule_id, 'rule_version', rule_version,
	        'depth', depth, 'anchor', anchor)::text FROM doc_rules WHERE doc_id = $1 ORDER BY position`
	snapRulesSQL = `SELECT json_build_object('position', position, 'rule_id', rule_id, 'rule_version', rule_version,
	        'depth', depth, 'anchor', anchor)::text FROM doc_rule_versions WHERE doc_id = $1 AND version = $2 ORDER BY position`
)

func equalRows(t *testing.T, what string, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("%s snapshot =\n%s\nwant the live rows before the bump\n%s",
			what, strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestBumpDocVersionSnapshotsEdgesAndRules: landing a revision keeps version
// 1's edges and arrangement exactly as the live tables held them.
func TestBumpDocVersionSnapshotsEdgesAndRules(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	doc := mustAcceptedSpec(t, s, "025-x")
	edges := snapshotRows(t, s, liveEdgesSQL, doc.ID)
	rules := snapshotRows(t, s, liveRulesSQL, doc.ID)
	if len(edges) == 0 || len(rules) < 2 {
		t.Fatalf("fixture: edges %v rules %v, want a requires edge and two or more rules", edges, rules)
	}

	if err := reviseDoc(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("ReviseDoc: %v", err)
	}
	if err := updateRevision(t, s, doc.ID, revisedSpecBody); err != nil {
		t.Fatalf("UpdateRevision: %v", err)
	}
	if _, err := acceptRevision(t, s, doc.ID, "stig"); err != nil {
		t.Fatalf("AcceptRevision: %v", err)
	}

	equalRows(t, "doc_edge_versions", snapshotRows(t, s, snapEdgesSQL, doc.ID, 1), edges)
	equalRows(t, "doc_rule_versions", snapshotRows(t, s, snapRulesSQL, doc.ID, 1), rules)

	v1, err := s.GetDocVersion(t.Context(), doc.ID, 1)
	if err != nil {
		t.Fatalf("GetDocVersion(1): %v", err)
	}
	if len(v1.Edges) != len(edges) || len(v1.Rules) != len(rules) {
		t.Errorf("GetDocVersion(1) edges %+v rules %+v, want %d and %d", v1.Edges, v1.Rules, len(edges), len(rules))
	}
	if len(v1.Rules) > 0 && !strings.HasPrefix(v1.Rules[0].Rule, "P1-RULE-") {
		t.Errorf("GetDocVersion(1).Rules[0].Rule = %q, want a P1-RULE-<n> ref", v1.Rules[0].Rule)
	}
}

// TestPlanEditSnapshotsEdges: a plan body edit bumps the version and keeps
// the replaced version's edges.
func TestPlanEditSnapshotsEdges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	mustCreateDoc(t, s, DocInput{
		Project: "p1", Kind: "spec", Number: 25, Slug: "025-documents-in-the-backbone", Body: specBody, CreatedBy: "stig",
	})
	plan := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "plan", Slug: "plan", Body: planBody, CreatedBy: "stig"})
	edges := snapshotRows(t, s, liveEdgesSQL, plan.ID)
	if len(edges) == 0 {
		t.Fatal("fixture: plan has no edges")
	}
	edited := strings.Replace(planBody, "  - 999-nowhere.md#sec-1\n", "", 1)
	updated, err := updateDocBody(t, s, plan.ID, edited)
	if err != nil {
		t.Fatalf("UpdateDocBody: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}
	equalRows(t, "doc_edge_versions", snapshotRows(t, s, snapEdgesSQL, plan.ID, 1), edges)
	if live := snapshotRows(t, s, liveEdgesSQL, plan.ID); len(live) != len(edges)-1 {
		t.Errorf("live edges after the edit = %v, want one fewer than %v", live, edges)
	}
}

// TestBumpRuleVersionSnapshotsEdges: revising an accepted rule keeps the
// replaced version's outgoing edges, and GetRuleVersion reads them back.
func TestBumpRuleVersionSnapshotsEdges(t *testing.T) {
	t.Parallel()
	s := openDocStore(t)
	d := mustCreateDoc(t, s, DocInput{Project: "p1", Kind: "spec", Slug: "t", Body: ruleDocV1, CreatedBy: "stig"})
	if _, _, err := acceptDoc(t, s, d.ID, "stig"); err != nil {
		t.Fatalf("AcceptDoc: %v", err)
	}
	a, b := ruleID(t, s, "P1", 1), ruleID(t, s, "P1", 3)
	ctx := context.Background()
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return LinkRules(tx, a, b, "refines") }); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := s.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		_, version, err = reviseRule(tx, a, "One", "A, revised.")
		return err
	}); err != nil {
		t.Fatalf("reviseRule: %v", err)
	}
	if version != 2 {
		t.Fatalf("reviseRule version = %d, want 2", version)
	}
	got := snapshotRows(t, s,
		`SELECT json_build_object('to_rule', to_rule, 'type', type, 'source', source)::text
		   FROM rule_edge_versions WHERE rule_id = $1 AND version = 1`, a)
	want := snapshotRows(t, s,
		`SELECT json_build_object('to_rule', to_rule, 'type', type, 'source', source)::text
		   FROM rule_edges WHERE from_rule = $1`, a)
	if len(want) == 0 {
		t.Fatal("fixture: rule has no outgoing edges")
	}
	equalRows(t, "rule_edge_versions", got, want)

	// Drop the live edge: version 1 still reports it.
	if err := s.Tx(ctx, func(tx *sql.Tx) error { return UnlinkRules(tx, a, b, "refines") }); err != nil {
		t.Fatal(err)
	}
	r, err := s.GetRuleVersion(ctx, "P1", 1, 1)
	if err != nil {
		t.Fatalf("GetRuleVersion: %v", err)
	}
	found := false
	for _, e := range r.Edges {
		if e.Type == "refines" && e.From == "P1-RULE-1" && e.To == "P1-RULE-3" {
			found = true
		}
	}
	if !found {
		t.Errorf("GetRuleVersion(v1).Edges = %+v, want the snapshotted refines edge", r.Edges)
	}
}
