package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// errSupersedeDryRun rolls back a dry run's transaction. SupersedeRules
// turns it into a nil error.
var errSupersedeDryRun = errors.New("supersede dry run")

// supersedeLine is one map line resolved against the store: what applying it
// changes, computed under the rule row locks before anything is written.
type supersedeLine struct {
	old      int64
	news     []int64
	withdraw bool     // old is not withdrawn yet
	edges    []int64  // successors with no supersedes edge to old yet
	tasks    []string // tasks governed by old, told when the line changes anything
}

// SupersedeRules applies a refactor map (S24, R2 to R7): every old rule
// becomes withdrawn, a supersedes edge with source refactor runs from each
// successor to it, and every task it governs gets a task.governance_superseded
// event (R5). Its task_governed_by rows stay where they are (S22).
// Withdrawing marks the accepted plans covering the rule stale (S23,
// through SetRuleStatus). The whole map is one rule.superseded event and
// one transaction. A dry run resolves and counts, then rolls back.
//
// Every ref is resolved and validated before the first write. A successor
// that is withdrawn or also on the left side, an old rule listed twice,
// and an unparseable ref are ErrInvalidInput naming the ref; an unknown
// rule, document or anchor is ErrNotFound. Re-running an applied map
// changes nothing and reports zero counts.
func (s *Store) SupersedeRules(ctx context.Context, project, actor string, in model.SupersedeInput) (res model.SupersedeResult, err error) {
	defer func() { s.metrics.ruleSupersede(supersedeOutcome(in.DryRun, err)) }()
	if in.DryRun {
		err = s.Tx(ctx, func(tx *sql.Tx) error {
			var rerr error
			if _, res, rerr = resolveSupersede(tx, project, in.Entries); rerr != nil {
				return rerr
			}
			return errSupersedeDryRun
		})
		if !errors.Is(err, errSupersedeDryRun) {
			return model.SupersedeResult{}, err
		}
		res.DryRun = true
		return res, nil
	}

	extID, err := randomExternalID()
	if err != nil {
		return model.SupersedeResult{}, err
	}
	payload, err := EventPayload(map[string]any{"actor": actor, "entries": in.Entries})
	if err != nil {
		return model.SupersedeResult{}, err
	}
	_, _, err = s.RecordEvent(ctx, "cli", extID, "rule.superseded", payload, func(tx *sql.Tx, eventID int64) error {
		lines, r, err := resolveSupersede(tx, project, in.Entries)
		if err != nil {
			return err
		}
		if err := MergeEventPayload(tx, eventID, map[string]any{"resolved": r.Entries}); err != nil {
			return err
		}
		res = r
		return applySupersede(tx, s.nowFn(), actor, eventID, lines, r.Entries)
	})
	if err != nil {
		return model.SupersedeResult{}, err
	}
	return res, nil
}

// resolveSupersede resolves every ref, locks the rules involved in id order
// (two refactors over overlapping maps cannot deadlock), validates the map,
// and works out what applying it changes. It writes only what ensureRules
// mints for a document that predates the rule tables. The lock is NO KEY
// UPDATE: a Govern insert holds KEY SHARE on the rule through the
// task_governed_by FK while its caller holds a plan row that
// SetRuleStatus's MarkPlansStale waits on, so FOR UPDATE would close a
// deadlock cycle.
func resolveSupersede(tx *sql.Tx, project string, entries []model.SupersedeEntry) ([]supersedeLine, model.SupersedeResult, error) {
	var res model.SupersedeResult
	if len(entries) == 0 {
		return nil, res, fmt.Errorf("empty supersede map: %w", ErrInvalidInput)
	}
	oldRef := map[int64]string{} // old rule -> the ref naming it in the map
	var lines []supersedeLine
	var newRefs [][]string
	var ids []int64
	for _, e := range entries {
		old, err := resolveRuleRef(tx, project, e.Old)
		if err != nil {
			return nil, res, err
		}
		if prev, dup := oldRef[old]; dup {
			return nil, res, fmt.Errorf("%s names the same rule as %s, already on the left side: %w", e.Old, prev, ErrInvalidInput)
		}
		oldRef[old] = e.Old
		l := supersedeLine{old: old}
		for _, n := range e.New {
			id, err := resolveRuleRef(tx, project, n)
			if err != nil {
				return nil, res, err
			}
			l.news = append(l.news, id)
		}
		lines = append(lines, l)
		newRefs = append(newRefs, e.New)
		ids = append(ids, old)
		ids = append(ids, l.news...)
	}
	for i, l := range lines {
		for j, n := range l.news {
			if _, ok := oldRef[n]; ok {
				return nil, res, fmt.Errorf("successor %s of %s is also on the left side: %w", newRefs[i][j], entries[i].Old, ErrInvalidInput)
			}
		}
	}

	status := map[int64]string{}
	ref := map[int64]string{}
	rows, err := tx.Query(
		`SELECT c.id, c.status, p.key, c.number FROM rules c JOIN projects p ON p.id = c.project_id
		  WHERE c.id = ANY($1) ORDER BY c.id FOR NO KEY UPDATE OF c`, ids)
	if err != nil {
		return nil, res, fmt.Errorf("lock rules: %w", err)
	}
	for rows.Next() {
		var id, number int64
		var st, key string
		if err := rows.Scan(&id, &st, &key, &number); err != nil {
			rows.Close()
			return nil, res, err
		}
		status[id], ref[id] = st, key+"-RULE-"+strconv.FormatInt(number, 10)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, res, err
	}

	var withdrawn []int64
	for i := range lines {
		l := &lines[i]
		resolved := model.SupersedeResolved{Old: ref[l.old], New: make([]string, 0, len(l.news))}
		existing, err := supersededTargets(tx, l.old)
		if err != nil {
			return nil, res, err
		}
		for j, n := range l.news {
			if status[n] == "withdrawn" {
				return nil, res, fmt.Errorf("successor %s of %s is withdrawn: %w", newRefs[i][j], entries[i].Old, ErrInvalidInput)
			}
			resolved.New = append(resolved.New, ref[n])
			if !existing[n] {
				existing[n] = true
				l.edges = append(l.edges, n)
			}
		}
		l.withdraw = status[l.old] != "withdrawn"
		if l.withdraw {
			withdrawn = append(withdrawn, l.old)
			res.Withdrawn++
		}
		res.Edges += len(l.edges)
		if l.withdraw || len(l.edges) > 0 {
			if l.tasks, err = governedTaskIDs(tx, l.old); err != nil {
				return nil, res, err
			}
			res.Tasks += len(l.tasks)
		}
		res.Entries = append(res.Entries, resolved)
	}
	if len(withdrawn) > 0 {
		// The plans SetRuleStatus will mark stale, counted before it runs.
		plans, err := acceptedPlansCovering(tx, withdrawn)
		if err != nil {
			return nil, res, err
		}
		res.StalePlans = len(plans)
	}
	return lines, res, nil
}

// applySupersede writes what resolveSupersede worked out, line by line.
func applySupersede(tx *sql.Tx, now time.Time, actor string, eventID int64, lines []supersedeLine, resolved []model.SupersedeResolved) error {
	for i, l := range lines {
		if l.withdraw {
			if err := SetRuleStatus(tx, now, l.old, "withdrawn", eventID); err != nil {
				return err
			}
		}
		for _, n := range l.edges {
			if _, err := tx.Exec(
				`INSERT INTO rule_edges (from_rule, to_rule, type, source) VALUES ($1, $2, 'supersedes', 'refactor')
				 ON CONFLICT (from_rule, to_rule, type) DO NOTHING`, n, l.old); err != nil {
				return fmt.Errorf("supersede rule %d by %d: %w", l.old, n, err)
			}
		}
		for _, task := range l.tasks {
			extID, err := randomExternalID()
			if err != nil {
				return err
			}
			payload, err := EventPayload(map[string]any{
				"task":               task,
				"rule":               resolved[i].Old,
				"successors":         resolved[i].New,
				"actor":              actor,
				"prov:wasInformedBy": "wlid:event/" + strconv.FormatInt(eventID, 10),
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO events (source, external_id, type, payload, received_at)
				 VALUES ('cli', $1, 'task.governance_superseded', $2, $3)`,
				extID, payload, now.UTC()); err != nil {
				return fmt.Errorf("record governance superseded for task %s: %w", task, err)
			}
		}
	}
	return nil
}

// resolveRuleRef resolves one map ref (R7): "WL-RULE-12" by project key and
// number, which may name another project's rule (S32), or
// "WL-SPEC-4#sec-2" to the rule that document arranges at that anchor.
func resolveRuleRef(tx *sql.Tx, project, ref string) (int64, error) {
	if c, ok := designdoc.ParseRuleRef(ref); ok {
		return RuleIDByRef(tx, c.Key, c.Number)
	}
	base, anchor := designdoc.SplitFragment(ref)
	if _, ok := designdoc.ParseShorthand(base); !ok || anchor == "" {
		return 0, fmt.Errorf("%q is neither a rule ref nor a section ref: %w", ref, ErrInvalidInput)
	}
	docID, ok, err := resolveDocRef(tx, project, base)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%s: document %s: %w", ref, base, ErrNotFound)
	}
	id, ok, err := ruleAtAnchor(tx, docID, anchor)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", ref, err)
	}
	if !ok {
		return 0, fmt.Errorf("%s: no rule at that anchor: %w", ref, ErrNotFound)
	}
	return id, nil
}

// ruleAtAnchor is the rule a document arranges at anchor, after
// ensureRules splits a document that predates the rule tables. A plan
// contains no rules, so a plan anchor names none.
// The sibling increment 4a ships store.RuleAtSection for the same lookup;
// whichever branch lands second deletes one of the two (R7).
func ruleAtAnchor(tx *sql.Tx, docID int64, anchor string) (int64, bool, error) {
	if err := ensureRules(tx, docID); err != nil {
		return 0, false, err
	}
	var id int64
	err := tx.QueryRow(
		`SELECT dc.rule_id FROM doc_rules dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan'`, docID, anchor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("rule at anchor %s of doc %d: %w", anchor, docID, err)
	}
	return id, true, nil
}

// supersededTargets is the set of rules that already have a supersedes
// edge to old: its recorded successors.
func supersededTargets(tx *sql.Tx, old int64) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT from_rule FROM rule_edges WHERE to_rule = $1 AND type = 'supersedes'`, old)
	if err != nil {
		return nil, fmt.Errorf("successors of rule %d: %w", old, err)
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// governedTaskIDs lists the tasks a rule governs, in id order.
func governedTaskIDs(tx *sql.Tx, ruleID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT task_id FROM task_governed_by WHERE rule_id = $1 ORDER BY task_id`, ruleID)
	if err != nil {
		return nil, fmt.Errorf("tasks governed by rule %d: %w", ruleID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// supersedeOutcome is the worklode_rule_supersede_total label for one call.
func supersedeOutcome(dryRun bool, err error) string {
	switch {
	case err == nil && dryRun:
		return "dry_run"
	case err == nil:
		return "applied"
	case errors.Is(err, ErrInvalidInput):
		return "invalid"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	default:
		return "error"
	}
}
