package store

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ruleEdgeTypes are the wl: properties a rule edge may carry (S12, S26,
// S22). supersededBy and wasDerivedFrom reuse dct:isReplacedBy and
// prov:wasDerivedFrom (ns/ontology.ttl's reuse list).
var ruleEdgeTypes = map[string]bool{
	"refines": true, "constrains": true, "conflictsWith": true, "references": true,
	"supersededBy": true, "wasDerivedFrom": true,
}

// ruleEdgeTypesList names every recognized type, for error messages.
const ruleEdgeTypesList = "refines, constrains, conflictsWith, references, supersededBy, wasDerivedFrom"

// LinkRules writes a manual edge (12-spec-refactoring-design-tree.md S12).
// A second identical edge is ErrEdgeExists; a self edge or an unknown type is
// ErrInvalidInput; an unknown rule id is ErrNotFound. supersededBy has one
// writer, lode rule supersede (S22, R4): LinkRules refuses it.
func LinkRules(tx *sql.Tx, fromID, toID int64, typ string) error {
	if !ruleEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of %s: %w", typ, ruleEdgeTypesList, ErrInvalidInput)
	}
	if typ == "supersededBy" {
		return fmt.Errorf("supersededBy edges are written by lode rule supersede, not rule link: %w", ErrInvalidInput)
	}
	if fromID == toID {
		return fmt.Errorf("a rule cannot relate to itself: %w", ErrInvalidInput)
	}
	_, err := tx.Exec(
		`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, $3, 'manual')`,
		fromID, toID, typ)
	switch {
	case isUniqueViolationOn(err, "rule_edges_pkey"):
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
// only by a later refactor.
func UnlinkRules(tx *sql.Tx, fromID, toID int64, typ string) error {
	if !ruleEdgeTypes[typ] {
		return fmt.Errorf("edge type %q is not one of %s: %w", typ, ruleEdgeTypesList, ErrInvalidInput)
	}
	var source string
	err := tx.QueryRow(`SELECT source FROM rule_edges WHERE from_rule = $1 AND to_rule = $2 AND type = $3`,
		fromID, toID, typ).Scan(&source)
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

// ruleEdgesSQL lists every edge in or out of one rule with both refs and
// headings, outgoing first, then by type and the other rule's number.
const ruleEdgesSQL = `
SELECT e.type, e.source, e.created_at,
       pf.key, cf.number, vf.heading,
       pt.key, ct.number, vt.heading
  FROM rule_edges e
  JOIN rules cf ON cf.id = e.from_rule
  JOIN projects pf ON pf.id = cf.project_id
  JOIN rule_versions vf ON vf.rule_id = cf.id AND vf.version = cf.version
  JOIN rules ct ON ct.id = e.to_rule
  JOIN projects pt ON pt.id = ct.project_id
  JOIN rule_versions vt ON vt.rule_id = ct.id AND vt.version = ct.version
 WHERE e.from_rule = $1 OR e.to_rule = $1
 ORDER BY (e.from_rule = $1) DESC, e.type, ct.number, cf.number`

// scanRuleEdges turns the rows of ruleEdgesSQL into model edges.
func scanRuleEdges(rows *sql.Rows) ([]model.RuleEdge, error) {
	out := []model.RuleEdge{}
	for rows.Next() {
		var e model.RuleEdge
		var fk, tk string
		var fn, tn int64
		if err := rows.Scan(&e.Type, &e.Source, &e.CreatedAt, &fk, &fn, &e.FromHeading, &tk, &tn, &e.ToHeading); err != nil {
			return nil, fmt.Errorf("scan rule edge: %w", err)
		}
		e.From = fmt.Sprintf("%s-RULE-%d", fk, fn)
		e.To = fmt.Sprintf("%s-RULE-%d", tk, tn)
		out = append(out, e)
	}
	return out, rows.Err()
}

// deriveReferences replaces a rule's derived references edges with the
// rules its text names (S26): every WL-RULE-<n> ref, and every
// WL-SPEC-<n>#sec-<a> ref resolved to the rule arranged at that anchor. A
// ref to the rule itself, to a whole document, or to nothing contributes
// no edge. Manual edges are untouched; a derived edge that would duplicate a
// manual one is skipped by the ON CONFLICT.
func deriveReferences(tx *sql.Tx, project string, ruleID int64, text string) error {
	if _, err := tx.Exec(
		`DELETE FROM rule_edges WHERE from_rule = $1 AND type = 'references' AND source = 'derived'`,
		ruleID); err != nil {
		return fmt.Errorf("clear derived references of rule %d: %w", ruleID, err)
	}
	var targets []int64
	for _, r := range designdoc.FindRuleRefs(text) {
		var id int64
		err := tx.QueryRow(
			`SELECT c.id FROM rules c JOIN projects p ON p.id = c.project_id WHERE p.key = $1 AND c.number = $2`,
			r.Key, r.Number).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s-RULE-%d: %w", r.Key, r.Number, err)
		}
		targets = append(targets, id)
	}
	for _, r := range designdoc.FindSectionRefs(text) {
		shorthand := fmt.Sprintf("%s-%s-%d", r.Shorthand.Key, r.Shorthand.Type, r.Shorthand.Number)
		docID, ok, err := resolveDocRef(tx, project, shorthand)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		var id int64
		// A plan's doc_rules rows borrow the spec's rules, so a ref to a
		// plan anchor names no rule of its own.
		err = tx.QueryRow(
			`SELECT dc.rule_id FROM doc_rules dc JOIN docs d ON d.id = dc.doc_id
			  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan'`, docID, r.Anchor).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("resolve %s#%s: %w", shorthand, r.Anchor, err)
		}
		targets = append(targets, id)
	}
	for _, to := range targets {
		if to == ruleID {
			continue
		}
		if _, err := tx.Exec(
			`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'references', 'derived')
			 ON CONFLICT (from_rule, to_rule, type) DO NOTHING`, ruleID, to); err != nil {
			return fmt.Errorf("derive reference %d -> %d: %w", ruleID, to, err)
		}
	}
	return nil
}
