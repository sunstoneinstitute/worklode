package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// versions.go is the one place a document or rule version moves (WL-SPEC-77
// §3). Each bump first copies the node's current row and its outgoing edges
// (and, for a document, its rule arrangement) into the snapshot tables at the
// current version, then increments the version. TestOnlyBumpFunctionsWriteVersions
// holds every other write path to that.

// bumpDocVersion snapshots document docID at its current version into
// doc_versions, doc_edge_versions and doc_rule_versions, then increments
// docs.version and returns the new version. Call it before any other write of
// the edit, so the snapshot holds the version being replaced.
func bumpDocVersion(tx *sql.Tx, docID int64) (int, error) {
	if _, err := tx.Exec(
		`INSERT INTO doc_versions (doc_id, version, body, title, issued, created_at)
		 SELECT id, version, body, title, issued, updated_at FROM docs WHERE id = $1`, docID,
	); err != nil {
		return 0, fmt.Errorf("snapshot doc %d before version bump: %w", docID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_edge_versions
		   (doc_id, version, from_anchor, type, to_doc, to_anchor, to_external, coverage, to_rule, completed_with)
		 SELECT e.from_doc, d.version, e.from_anchor, e.type, e.to_doc, e.to_anchor, e.to_external,
		        e.coverage, e.to_rule,
		        (SELECT jsonb_agg(CASE WHEN w.to_doc IS NOT NULL
		                               THEN jsonb_build_object('to_doc', w.to_doc)
		                               ELSE jsonb_build_object('to_external', w.to_external) END
		                          ORDER BY w.position)
		           FROM doc_coverage_completed_with w WHERE w.edge_id = e.id)
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc
		  WHERE e.from_doc = $1`, docID,
	); err != nil {
		return 0, fmt.Errorf("snapshot edges of doc %d: %w", docID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_rule_versions (doc_id, version, position, rule_id, rule_version, depth, anchor)
		 SELECT r.doc_id, d.version, r.position, r.rule_id, r.rule_version, r.depth, r.anchor
		   FROM doc_rules r JOIN docs d ON d.id = r.doc_id
		  WHERE r.doc_id = $1`, docID,
	); err != nil {
		return 0, fmt.Errorf("snapshot rules of doc %d: %w", docID, err)
	}
	var version int
	if err := tx.QueryRow(
		`UPDATE docs SET version = version + 1 WHERE id = $1 RETURNING version`, docID,
	).Scan(&version); err != nil {
		return 0, fmt.Errorf("bump doc %d version: %w", docID, err)
	}
	return version, nil
}

// bumpRuleVersion snapshots rule ruleID's outgoing edges at its current
// version into rule_edge_versions, moves the rule to the next version as a
// draft, and inserts that version's text. It returns the new version.
func bumpRuleVersion(tx *sql.Tx, ruleID int64, heading, body string) (int, error) {
	if _, err := tx.Exec(
		`INSERT INTO rule_edge_versions (rule_id, version, to_rule, type, source)
		 SELECT e.from_rule, r.version, e.to_rule, e.type, e.source
		   FROM rule_edges e JOIN rules r ON r.id = e.from_rule
		  WHERE e.from_rule = $1`, ruleID,
	); err != nil {
		return 0, fmt.Errorf("snapshot edges of rule %d: %w", ruleID, err)
	}
	var version int
	if err := tx.QueryRow(
		`UPDATE rules SET version = version + 1, status = 'draft', updated_at = now() WHERE id = $1 RETURNING version`,
		ruleID).Scan(&version); err != nil {
		return 0, fmt.Errorf("bump rule %d: %w", ruleID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO rule_versions (rule_id, version, heading, body) VALUES ($1, $2, $3, $4)`,
		ruleID, version, heading, body); err != nil {
		return 0, fmt.Errorf("insert rule %d v%d: %w", ruleID, version, err)
	}
	return version, nil
}

// docEdgeSnapshot reads document id's outgoing edges as snapshotted at
// version, with the far end named the way ListDocEdges names it.
func (s *Store) docEdgeSnapshot(ctx context.Context, id int64, version int) ([]model.DocEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, 0),
		        coalesce(e.to_anchor,''), coalesce(e.to_external,''),
		        coalesce(d.project_id,''), coalesce(d.slug,''), coalesce(d.kind,''),
		        coalesce(d.number,0), coalesce(d.status,''),
		        coalesce((SELECT json_agg(coalesce(wd.slug, w->>'to_external') ORDER BY n)
		                    FROM jsonb_array_elements(e.completed_with) WITH ORDINALITY AS c(w, n)
		                    LEFT JOIN docs wd ON wd.id = (w->>'to_doc')::bigint), '[]')::text,
		        coalesce(rp.key || '-RULE-' || r.number, '')
		   FROM doc_edge_versions e
		   LEFT JOIN docs d ON d.id = e.to_doc
		   LEFT JOIN rules r ON r.id = e.to_rule
		   LEFT JOIN projects rp ON rp.id = r.project_id
		  WHERE e.doc_id = $1 AND e.version = $2
		  ORDER BY e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, 0),
		           coalesce(e.to_anchor,''), coalesce(e.to_external,''), r.number`, id, version)
	if err != nil {
		return nil, fmt.Errorf("read edges of doc %d v%d: %w", id, version, err)
	}
	out, err := scanDocEdges(rows)
	if err != nil {
		return nil, fmt.Errorf("read edges of doc %d v%d: %w", id, version, err)
	}
	return out, nil
}

// docVersionRules reads the rules document id arranged at version: from
// doc_rule_versions when snapshot is set, else from the live doc_rules.
func (s *Store) docVersionRules(ctx context.Context, id int64, version int, snapshot bool) ([]model.DocVersionRule, error) {
	table, where, args := "doc_rules", "x.doc_id = $1", []any{id}
	if snapshot {
		table, where, args = "doc_rule_versions", "x.doc_id = $1 AND x.version = $2", []any{id, version}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT x.position, p.key || '-RULE-' || r.number, x.rule_version, x.depth, x.anchor
		   FROM `+table+` x
		   JOIN rules r ON r.id = x.rule_id
		   JOIN projects p ON p.id = r.project_id
		  WHERE `+where+`
		  ORDER BY x.position`, args...)
	if err != nil {
		return nil, fmt.Errorf("read rules of doc %d v%d: %w", id, version, err)
	}
	return collectRows(rows, fmt.Sprintf("read rules of doc %d v%d", id, version), func(r rowScanner) (model.DocVersionRule, error) {
		var v model.DocVersionRule
		err := r.Scan(&v.Position, &v.Rule, &v.RuleVersion, &v.Depth, &v.Anchor)
		return v, err
	})
}

// ruleEdgeSnapshotSQL reads a rule's outgoing edges as snapshotted at a
// version, in ruleEdgesSQL's column order. A snapshot has no creation time.
const ruleEdgeSnapshotSQL = `
SELECT e.type, e.source, '0001-01-01T00:00:00Z'::timestamptz,
       pf.key, cf.number, vf.heading,
       pt.key, ct.number, vt.heading
  FROM rule_edge_versions e
  JOIN rules cf ON cf.id = e.rule_id
  JOIN projects pf ON pf.id = cf.project_id
  JOIN rule_versions vf ON vf.rule_id = cf.id AND vf.version = e.version
  JOIN rules ct ON ct.id = e.to_rule
  JOIN projects pt ON pt.id = ct.project_id
  JOIN rule_versions vt ON vt.rule_id = ct.id AND vt.version = ct.version
 WHERE e.rule_id = $1 AND e.version = $2
 ORDER BY e.type, ct.number`
