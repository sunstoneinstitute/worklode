package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/refkind"
)

// projectKeys reads every project key on the instance, the set a bare
// <KEY>-<n> counts as a task id for (WL-REQ-1851).
func projectKeys(tx *sql.Tx) (map[string]bool, error) {
	rows, err := tx.Query(`SELECT key FROM projects`)
	if err != nil {
		return nil, fmt.Errorf("read project keys: %w", err)
	}
	keys, err := collectRows(rows, "read project keys", func(r rowScanner) (string, error) {
		var k string
		return k, r.Scan(&k)
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

// resolveHits turns scanner hits into targets; unresolved hits are returned
// separately for --dry-run (WL-REQ-1851). Rows are grouped by target with
// one line entry per hit. A hit naming the node itself is dropped.
func resolveHits(tx *sql.Tx, project string, from model.NodeKind, fromID string, hits []inlineref.Hit) (refs []model.InlineRef, unresolved []inlineref.Hit, err error) {
	var key string
	if err := tx.QueryRow(`SELECT key FROM projects WHERE id = $1`, project).Scan(&key); err != nil {
		return nil, nil, fmt.Errorf("read key of project %q: %w", project, err)
	}
	at := map[[2]string]int{}
	for _, h := range hits {
		kind, id, ok, err := resolveHit(tx, project, key, h)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			unresolved = append(unresolved, h)
			continue
		}
		if kind == from && id == fromID {
			continue
		}
		k := [2]string{string(kind), id}
		i, seen := at[k]
		if !seen {
			i = len(refs)
			at[k] = i
			refs = append(refs, model.InlineRef{FromKind: from, FromID: fromID, ToKind: kind, ToID: id, ToRef: hitRef(h, key), Source: "derived"})
		}
		refs[i].Lines = append(refs[i].Lines, h.Line)
	}
	return refs, unresolved, nil
}

// hitRef spells the ref a hit names, for display.
func hitRef(h inlineref.Hit, key string) string {
	if h.Key != "" {
		key = h.Key
	}
	ref := key + "-" + strconv.FormatInt(h.Number, 10)
	if h.Infix != "" {
		ref = key + "-" + h.Infix + "-" + strconv.FormatInt(h.Number, 10)
	}
	if h.Anchor != "" {
		ref += "#" + h.Anchor
	}
	return ref
}

// resolveHit resolves one hit (WL-REQ-1357). A section ref resolves to the
// rule arranged at that anchor, else to the document; a plan arranges no
// rules of its own, so its anchors resolve to the plan (WL-REQ-1295).
func resolveHit(tx *sql.Tx, project, key string, h inlineref.Hit) (model.NodeKind, string, bool, error) {
	if h.Infix == "" {
		var id string
		err := tx.QueryRow(`SELECT id FROM tasks WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("task", id, err)
	}
	target, _ := refkind.Lookup(h.Infix)
	switch target {
	case refkind.Rule:
		id, err := RuleIDByRef(tx, h.Key, h.Number)
		if errors.Is(err, ErrNotFound) {
			return "", "", false, nil
		}
		return found("rule", strconv.FormatInt(id, 10), err)
	case refkind.Milestone:
		var id string
		err := tx.QueryRow(`SELECT id FROM milestones WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("milestone", id, err)
	case refkind.Deliverable:
		var id string
		err := tx.QueryRow(`SELECT id FROM deliverables WHERE id = $1`, hitRef(h, key)).Scan(&id)
		return found("deliverable", id, err)
	}
	k := h.Key
	if k == "" {
		k = key
	}
	docID, ok, err := resolveDocRef(tx, project, k+"-"+h.Infix+"-"+strconv.FormatInt(h.Number, 10))
	if err != nil || !ok {
		return "", "", false, err
	}
	if h.Anchor != "" {
		var ruleID int64
		err := tx.QueryRow(
			`SELECT dc.rule_id FROM doc_entries dc JOIN docs d ON d.id = dc.doc_id
			  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan' AND dc.rule_id IS NOT NULL`,
			docID, h.Anchor).Scan(&ruleID)
		if err == nil {
			return "rule", strconv.FormatInt(ruleID, 10), true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", "", false, fmt.Errorf("resolve %s: %w", hitRef(h, k), err)
		}
	}
	return "doc", strconv.FormatInt(docID, 10), true, nil
}

// found maps a single-row lookup to resolveHit's result.
func found(kind model.NodeKind, id string, err error) (model.NodeKind, string, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("resolve %s %s: %w", kind, id, err)
	}
	return kind, id, true, nil
}

// deriveInlineRefs re-extracts one field and replaces its derived rows
// (WL-REQ-1852). version is nil for an unversioned field. Manual rows stay;
// a derived row that duplicates one records its lines on the manual row.
func deriveInlineRefs(tx *sql.Tx, project string, from model.NodeKind, fromID, field string, version *int, text string) error {
	keys, err := projectKeys(tx)
	if err != nil {
		return err
	}
	refs, _, err := resolveHits(tx, project, from, fromID, inlineref.Scan(text, keys))
	if err != nil {
		return err
	}
	const match = `from_kind = $1 AND from_id = $2 AND field = $3 AND from_version IS NOT DISTINCT FROM $4::int`
	if _, err := tx.Exec(`DELETE FROM inline_refs WHERE `+match+` AND source = 'derived'`, from, fromID, field, version); err != nil {
		return fmt.Errorf("clear inline refs of %s %s %s: %w", from, fromID, field, err)
	}
	if _, err := tx.Exec(`UPDATE inline_refs SET lines = '{}' WHERE `+match, from, fromID, field, version); err != nil {
		return fmt.Errorf("reset inline ref lines of %s %s %s: %w", from, fromID, field, err)
	}
	for _, r := range refs {
		if _, err := tx.Exec(
			`INSERT INTO inline_refs (from_kind, from_id, field, from_version, to_kind, to_id, lines, source)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, 'derived')
			 ON CONFLICT (from_kind, from_id, field, COALESCE(from_version, -1), to_kind, to_id)
			 DO UPDATE SET lines = EXCLUDED.lines`,
			from, fromID, field, version, r.ToKind, r.ToID, r.Lines); err != nil {
			return fmt.Errorf("derive inline ref %s %s -> %s %s: %w", from, fromID, r.ToKind, r.ToID, err)
		}
	}
	return nil
}

// deriveRuleInlineRefs re-extracts a rule's heading and body at version.
func deriveRuleInlineRefs(tx *sql.Tx, project string, ruleID int64, version int, heading, body string) error {
	id := strconv.FormatInt(ruleID, 10)
	if err := deriveInlineRefs(tx, project, "rule", id, "heading", &version, heading); err != nil {
		return err
	}
	return deriveInlineRefs(tx, project, "rule", id, "body", &version, body)
}
