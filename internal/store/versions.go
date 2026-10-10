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
//
// The snapshot holds the rendered text (WL-REQ-1299), so a version keeps
// what it showed even where a draft rule version it arranged is rewritten in
// place later.
func bumpDocVersion(tx *sql.Tx, docID int64) (int, error) {
	var body string
	if err := tx.QueryRow(`SELECT body FROM docs WHERE id = $1`, docID).Scan(&body); err != nil {
		return 0, fmt.Errorf("read doc %d before version bump: %w", docID, err)
	}
	texts, err := arrangedTexts(context.Background(), tx, "doc_rules", "x.doc_id = $1", docID)
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_versions (doc_id, version, body, title, issued, created_at)
		 SELECT id, version, $2, title, issued, updated_at FROM docs WHERE id = $1`, docID, renderBody(body, texts[docID]),
	); err != nil {
		return 0, fmt.Errorf("snapshot doc %d before version bump: %w", docID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_edge_versions
		   (doc_id, version, from_anchor, type, to_doc, to_anchor, to_external, to_rule, owner_doc, owner_external)
		 SELECT e.from_doc, d.version, e.from_anchor, e.type, e.to_doc, e.to_anchor, e.to_external,
		        e.to_rule, e.owner_doc, e.owner_external
		   FROM doc_edges e JOIN docs d ON d.id = e.from_doc
		  WHERE e.from_doc = $1`, docID,
	); err != nil {
		return 0, fmt.Errorf("snapshot edges of doc %d: %w", docID, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO doc_rule_versions (doc_id, version, position, rule_id, rule_version, heading, depth, anchor)
		 SELECT r.doc_id, d.version, r.position, r.rule_id, r.rule_version, r.heading, r.depth, r.anchor
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

// moveSpecsToAcceptedRules moves every live spec arranging one of ruleIDs
// at an older version than the rule's accepted one to that version
// (WL-REQ-1298). An accepted spec is bumped first, so its prior version
// keeps the rule versions it showed, and the sections whose rule moved are
// stamped revised in the new version; a draft spec follows without a bump.
// except is the spec whose own accept lands the rules, 0 for none. It
// returns the accepted specs it bumped, with their new versions.
func moveSpecsToAcceptedRules(tx *sql.Tx, ruleIDs []int64, except int64) (map[int64]int, error) {
	rows, err := tx.Query(
		`SELECT DISTINCT dr.doc_id, d.status FROM doc_rules dr
		   JOIN rules r ON r.id = dr.rule_id
		   JOIN docs d ON d.id = dr.doc_id
		  WHERE dr.rule_id = ANY($1) AND r.status = 'accepted' AND dr.rule_version < r.version
		    AND dr.doc_id <> $2 AND d.kind <> 'plan' AND d.deleted_at IS NULL
		    AND d.status IN ('draft', 'accepted')
		  ORDER BY dr.doc_id`, ruleIDs, except)
	if err != nil {
		return nil, fmt.Errorf("specs arranging rules %v: %w", ruleIDs, err)
	}
	type spec struct {
		id       int64
		accepted bool
	}
	var specs []spec
	for rows.Next() {
		var sp spec
		var status string
		if err := rows.Scan(&sp.id, &status); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan spec arranging rules %v: %w", ruleIDs, err)
		}
		sp.accepted = status == "accepted"
		specs = append(specs, sp)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("specs arranging rules %v: %w", ruleIDs, err)
	}
	bumped := map[int64]int{}
	for _, sp := range specs {
		version := 0
		if sp.accepted {
			if version, err = bumpDocVersion(tx, sp.id); err != nil {
				return nil, err
			}
			bumped[sp.id] = version
		}
		if _, err := tx.Exec(
			`WITH moved AS (
			     UPDATE doc_rules dr SET rule_version = r.version
			       FROM rules r
			      WHERE dr.doc_id = $1 AND r.id = dr.rule_id AND dr.rule_id = ANY($2)
			        AND r.status = 'accepted' AND dr.rule_version < r.version
			  RETURNING dr.anchor)
			 UPDATE doc_sections SET last_revised_in = $3
			  WHERE $3 > 0 AND doc_id = $1 AND anchor IN (SELECT anchor FROM moved)`,
			sp.id, ruleIDs, version); err != nil {
			return nil, fmt.Errorf("move doc %d to accepted rule versions: %w", sp.id, err)
		}
	}
	return bumped, nil
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
// version.
func (s *Store) docEdgeSnapshot(ctx context.Context, id int64, version int) ([]model.DocEdge, error) {
	return s.storedEdgeSet(ctx, "doc_edge_versions", "e.doc_id = $1 AND e.version = $2", id, version)
}

// revisionEdges reads the edge set of document id's open candidate revision.
func (s *Store) revisionEdges(ctx context.Context, id int64) ([]model.DocEdge, error) {
	return s.storedEdgeSet(ctx, "doc_revision_edges", "e.doc_id = $1", id)
}

// storedEdgeSet reads an edge set kept outside doc_edges, with the far end
// and a defers owner named the way ListDocEdges names them.
func (s *Store) storedEdgeSet(ctx context.Context, table, where string, args ...any) ([]model.DocEdge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, ra.doc_id, 0),
		        coalesce(e.to_anchor, ra.anchor, ''), coalesce(e.to_external,''),
		        coalesce(d.project_id,''), coalesce(d.slug,''), coalesce(d.kind,''),
		        coalesce(d.number,0), coalesce(d.status,''), coalesce(od.slug, e.owner_external, ''),
		        coalesce(`+ruleRefSQL("rp", "r")+`, ''), coalesce(d.title,''), coalesce(dp.key,'')
		   FROM `+table+` e
		   LEFT JOIN rules r ON r.id = e.to_rule
		   LEFT JOIN projects rp ON rp.id = r.project_id
		   LEFT JOIN LATERAL (
		            SELECT dr.doc_id, dr.anchor FROM doc_rules dr
		             WHERE dr.rule_id = e.to_rule
		             ORDER BY dr.doc_id, dr.position LIMIT 1
		        ) ra ON true
		   LEFT JOIN docs d ON d.id = coalesce(e.to_doc, ra.doc_id)
		   LEFT JOIN projects dp ON dp.id = d.project_id
		   LEFT JOIN docs od ON od.id = e.owner_doc
		  WHERE `+where+`
		  ORDER BY e.type, coalesce(e.from_anchor,''), coalesce(e.to_doc, ra.doc_id, 0),
		           coalesce(e.to_anchor, ra.anchor, ''), coalesce(e.to_external,''), r.number`, args...)
	if err != nil {
		return nil, fmt.Errorf("read edges from %s: %w", table, err)
	}
	out, err := scanDocEdges(rows)
	if err != nil {
		return nil, fmt.Errorf("read edges from %s: %w", table, err)
	}
	return out, nil
}

// docVersionRules reads the entries document id arranged at version, rules
// and spec headings: from doc_rule_versions when snapshot is set, else from
// the live doc_rules.
func (s *Store) docVersionRules(ctx context.Context, id int64, version int, snapshot bool) ([]model.DocVersionRule, error) {
	table, where, args := "doc_rules", "x.doc_id = $1", []any{id}
	if snapshot {
		table, where, args = "doc_rule_versions", "x.doc_id = $1 AND x.version = $2", []any{id, version}
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT x.position, coalesce(`+ruleRefSQL("p", "r")+`, ''), coalesce(x.rule_version, 0),
		        coalesce(x.heading, ''), x.depth, x.anchor
		   FROM `+table+` x
		   LEFT JOIN rules r ON r.id = x.rule_id
		   LEFT JOIN projects p ON p.id = r.project_id
		  WHERE `+where+`
		  ORDER BY x.position`, args...)
	if err != nil {
		return nil, fmt.Errorf("read rules of doc %d v%d: %w", id, version, err)
	}
	return collectRows(rows, fmt.Sprintf("read rules of doc %d v%d", id, version), func(r rowScanner) (model.DocVersionRule, error) {
		var v model.DocVersionRule
		err := r.Scan(&v.Position, &v.Rule, &v.RuleVersion, &v.Heading, &v.Depth, &v.Anchor)
		return v, err
	})
}

// ruleEdgeSnapshotSQL reads a rule's outgoing edges as snapshotted at a
// version, in ruleEdgesSQL's column order. A snapshot has no creation time.
const ruleEdgeSnapshotSQL = `
SELECT e.type, e.source, '0001-01-01T00:00:00Z'::timestamptz,
       pf.key, cf.number, cf.kind, vf.heading,
       pt.key, ct.number, ct.kind, vt.heading
  FROM rule_edge_versions e
  JOIN rules cf ON cf.id = e.rule_id
  JOIN projects pf ON pf.id = cf.project_id
  JOIN rule_versions vf ON vf.rule_id = cf.id AND vf.version = e.version
  JOIN rules ct ON ct.id = e.to_rule
  JOIN projects pt ON pt.id = ct.project_id
  JOIN rule_versions vt ON vt.rule_id = ct.id AND vt.version = ct.version
 WHERE e.rule_id = $1 AND e.version = $2
 ORDER BY e.type, ct.number`
