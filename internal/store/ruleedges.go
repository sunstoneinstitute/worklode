package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ruleEdgeTypes are the wl: properties a rule edge may carry (S12, S26,
// S22, WL-REQ-165). supersedes and wasDerivedFrom reuse dct:replaces and
// prov:wasDerivedFrom (ns/ontology.ttl's reuse list). supersedes runs new ->
// old and amends runs amending -> amended; their inverses (supersededBy,
// amendedBy) are read from the far end and never stored.
var ruleEdgeTypes = map[string]bool{
	"refines": true, "needs": true, "constrains": true, "conflictsWith": true, "references": true,
	"amends": true, "supersedes": true, "wasDerivedFrom": true,
}

// ruleEdgeTypesList names every recognized type, for error messages.
const ruleEdgeTypesList = "refines, needs, constrains, conflictsWith, references, amends, supersedes, wasDerivedFrom"

// ruleEdgeInverses maps each inferred inverse to its declared term. A write
// naming an inverse is refused, naming the declared term (WL-REQ-1288).
var ruleEdgeInverses = map[string]string{
	"refinedBy": "refines", "neededBy": "needs", "referencedBy": "references",
	"supersededBy": "supersedes", "amendedBy": "amends",
}

// checkRuleEdgeType refuses a type that is not a stored rule edge type.
func checkRuleEdgeType(typ string) error {
	if declared, ok := ruleEdgeInverses[typ]; ok {
		return fmt.Errorf("%s is inferred and never stored; write %s from the other rule: %w", typ, declared, ErrInvalidInput)
	}
	if !ruleEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of %s: %w", typ, ruleEdgeTypesList, ErrInvalidInput)
	}
	return nil
}

// LinkRules writes a manual edge.
// A second identical edge is ErrEdgeExists, and so is a conflictsWith edge
// whose reverse exists: the relation is symmetric and stored once per pair
// (rule_edges_symmetric_once). A self edge or an unknown type is
// ErrInvalidInput; an unknown rule id is ErrNotFound. supersedes has one
// writer, lode rule supersede (S22, R4): LinkRules refuses it.
func LinkRules(tx *sql.Tx, fromID, toID int64, typ string) error {
	if err := checkRuleEdgeType(typ); err != nil {
		return err
	}
	if typ == "supersedes" {
		return fmt.Errorf("supersedes edges are written by lode rule supersede, not rule link: %w", ErrInvalidInput)
	}
	if fromID == toID {
		return fmt.Errorf("a rule cannot relate to itself: %w", ErrInvalidInput)
	}
	if typ == "references" {
		return linkReference(tx, fromID, toID)
	}
	_, err := tx.Exec(
		`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, $3, 'manual')`,
		fromID, toID, typ)
	switch {
	case isUniqueViolationOn(err, "rule_edges_pkey"), isUniqueViolationOn(err, "rule_edges_symmetric_once"):
		return fmt.Errorf("rule %d already %s rule %d: %w", fromID, typ, toID, ErrEdgeExists)
	case pgViolation(err, "23503", "rule_edges_from_rule_fkey"), pgViolation(err, "23503", "rule_edges_to_rule_fkey"):
		return fmt.Errorf("rule: %w", ErrNotFound)
	case err != nil:
		return fmt.Errorf("link rule %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// UnlinkRules removes a manual edge, or reports ErrNotFound. A derived
// edge cannot be removed by hand: it comes back on the next version anyway.
// A refactor edge cannot be removed by hand either (S22, R4): it is undone
// only by a later refactor. A conflictsWith edge is removed whichever
// direction it was stored in.
func UnlinkRules(tx *sql.Tx, fromID, toID int64, typ string) error {
	if err := checkRuleEdgeType(typ); err != nil {
		return err
	}
	if typ == "references" {
		return unlinkReference(tx, fromID, toID)
	}
	var source string
	err := tx.QueryRow(`SELECT from_rule, to_rule, source FROM rule_edges
		 WHERE type = $3 AND ((from_rule = $1 AND to_rule = $2)
		    OR (type = 'conflictsWith' AND from_rule = $2 AND to_rule = $1))`,
		fromID, toID, typ).Scan(&fromID, &toID, &source)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule %d does not %s rule %d: %w", fromID, typ, toID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read edge %d %s %d: %w", fromID, typ, toID, err)
	}
	if source == "derived" {
		return fmt.Errorf("the %s edge from rule %d to %d is derived from its text; edit the rule instead: %w", typ, fromID, toID, ErrInvalidInput)
	}
	if source == "refactor" {
		return fmt.Errorf("the %s edge from rule %d to %d was written by a refactor; only a later refactor can undo it: %w", typ, fromID, toID, ErrInvalidInput)
	}
	if _, err := tx.Exec(`DELETE FROM rule_edges WHERE from_rule = $1 AND to_rule = $2 AND type = $3`,
		fromID, toID, typ); err != nil {
		return fmt.Errorf("unlink rule %d %s %d: %w", fromID, typ, toID, err)
	}
	return nil
}

// ListDocAmendments lists the amends edges onto the rules a document
// arranges, in section order then by amending rule (WL-REQ-165). A
// plan contains no rules, so a plan has none.
func (s *Store) ListDocAmendments(ctx context.Context, docID int64) ([]model.DocAmendment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT dr.anchor, `+ruleRefSQL("p", "r")+`, `+ruleRefSQL("ap", "ar")+`
		   FROM doc_entries dr
		   JOIN docs d ON d.id = dr.doc_id
		   JOIN rules r ON r.id = dr.rule_id
		   JOIN projects p ON p.id = r.project_id
		   JOIN rule_edges e ON e.to_rule = r.id AND e.type = 'amends'
		   JOIN rules ar ON ar.id = e.from_rule
		   JOIN projects ap ON ap.id = ar.project_id
		  WHERE dr.doc_id = $1 AND d.kind <> 'plan'
		  ORDER BY dr.position, ap.key, ar.number`, docID)
	if err != nil {
		return nil, fmt.Errorf("list amendments of doc %d: %w", docID, err)
	}
	return collectRows(rows, fmt.Sprintf("list amendments of doc %d", docID), func(r rowScanner) (model.DocAmendment, error) {
		var a model.DocAmendment
		err := r.Scan(&a.Anchor, &a.Rule, &a.By)
		return a, err
	})
}

// ruleEdgesSQL lists every edge in or out of one rule with both refs and
// headings, outgoing first, then by type and the other rule's number. A
// references edge is a rule-to-rule inline_refs row at the referring rule's
// current version (WL-REQ-1852), one per pair whichever fields name it.
const ruleEdgesSQL = `
WITH e AS (
    SELECT from_rule, to_rule, type, source, created_at FROM rule_edges
     WHERE from_rule = $1 OR to_rule = $1
    UNION ALL
    SELECT r.id, i.to_id::bigint, 'references', max(i.source), min(i.created_at)
      FROM inline_refs i JOIN rules r ON r.id::text = i.from_id AND r.version = i.from_version
     WHERE i.from_kind = 'rule' AND i.to_kind = 'rule'
       AND (i.from_id = $1::bigint::text OR i.to_id = $1::bigint::text)
     GROUP BY r.id, i.to_id
)
SELECT e.type, e.source, e.created_at,
       pf.key, cf.number, cf.kind, vf.heading,
       pt.key, ct.number, ct.kind, vt.heading
  FROM e
  JOIN rules cf ON cf.id = e.from_rule
  JOIN projects pf ON pf.id = cf.project_id
  JOIN rule_versions vf ON vf.rule_id = cf.id AND vf.version = cf.version
  JOIN rules ct ON ct.id = e.to_rule
  JOIN projects pt ON pt.id = ct.project_id
  JOIN rule_versions vt ON vt.rule_id = ct.id AND vt.version = ct.version
 ORDER BY (e.from_rule = $1) DESC, e.type, ct.number, cf.number`

// scanRuleEdges turns the rows of ruleEdgesSQL into model edges.
func scanRuleEdges(rows *sql.Rows) ([]model.RuleEdge, error) {
	out := []model.RuleEdge{}
	for rows.Next() {
		var e model.RuleEdge
		var fk, tk, fkind, tkind string
		var fn, tn int64
		if err := rows.Scan(&e.Type, &e.Source, &e.CreatedAt, &fk, &fn, &fkind, &e.FromHeading, &tk, &tn, &tkind, &e.ToHeading); err != nil {
			return nil, fmt.Errorf("scan rule edge: %w", err)
		}
		e.From = designdoc.FormatRuleRef(fk, fn, fkind)
		e.To = designdoc.FormatRuleRef(tk, tn, tkind)
		out = append(out, e)
	}
	return out, rows.Err()
}

// linkReference writes a manual references row in inline_refs on the rule's
// body at its current version (WL-REQ-1640). A derived row for the same
// target becomes manual; a manual one is ErrEdgeExists.
func linkReference(tx *sql.Tx, fromID, toID int64) error {
	var version int
	err := tx.QueryRow(`SELECT version FROM rules WHERE id = $1`, fromID).Scan(&version)
	if err == nil {
		err = tx.QueryRow(`SELECT 1 FROM rules WHERE id = $1`, toID).Scan(new(int))
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule: %w", ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read rules %d, %d: %w", fromID, toID, err)
	}
	res, err := tx.Exec(
		`INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
		 VALUES ('rule', $1, 'body', $2, 'rule', $3, '{}', 'manual')
		 ON CONFLICT (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id,
		              COALESCE(via_doc, ''), COALESCE(via_anchor, ''))
		 DO UPDATE SET source = 'manual' WHERE inline_refs.source = 'derived'`,
		strconv.FormatInt(fromID, 10), version, strconv.FormatInt(toID, 10))
	if err != nil {
		return fmt.Errorf("link rule %d references %d: %w", fromID, toID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("rule %d already references rule %d: %w", fromID, toID, ErrEdgeExists)
	}
	return nil
}

// unlinkReference removes a manual references row at the rule's current
// version. A row the text still names stays, as derived. A derived-only
// reference is ErrInvalidInput: it comes from the text.
func unlinkReference(tx *sql.Tx, fromID, toID int64) error {
	var source string
	var named bool
	err := tx.QueryRow(
		`SELECT max(i.source), coalesce(bool_or(cardinality(i.lines) > 0) FILTER (WHERE i.source = 'manual'), false) FROM inline_refs i JOIN rules r ON r.id::text = i.from_id AND r.version = i.from_version
		  WHERE i.from_kind = 'rule' AND i.from_id = $1 AND i.to_kind = 'rule' AND i.to_id = $2
		 HAVING count(*) > 0`,
		strconv.FormatInt(fromID, 10), strconv.FormatInt(toID, 10)).Scan(&source, &named)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rule %d does not reference rule %d: %w", fromID, toID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("read reference %d -> %d: %w", fromID, toID, err)
	}
	if source == "derived" {
		return fmt.Errorf("the references edge from rule %d to %d is derived from its text; edit the rule instead: %w", fromID, toID, ErrInvalidInput)
	}
	q := `DELETE FROM inline_refs`
	if named {
		q = `UPDATE inline_refs SET source = 'derived'`
	}
	if _, err := tx.Exec(q+` WHERE from_kind = 'rule' AND from_id = $1::bigint::text AND to_kind = 'rule' AND to_id = $2::bigint::text
		   AND source = 'manual' AND from_version = (SELECT version FROM rules WHERE id = $1)`,
		fromID, toID); err != nil {
		return fmt.Errorf("unlink rule %d references %d: %w", fromID, toID, err)
	}
	return nil
}
