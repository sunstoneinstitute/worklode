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

// errSupersedeDryRun rolls back a dry run's transaction. SupersedeClauses
// turns it into a nil error.
var errSupersedeDryRun = errors.New("supersede dry run")

// supersedeLine is one map line resolved against the store: what applying it
// changes, computed under the clause row locks before anything is written.
type supersedeLine struct {
	old      int64
	news     []int64
	withdraw bool     // old is not withdrawn yet
	edges    []int64  // successors with no supersededBy edge from old yet
	tasks    []string // tasks governed by old, told when the line changes anything
}

// SupersedeClauses applies a refactor map (S24, R2 to R7): every old clause
// becomes withdrawn, a supersededBy edge with source refactor runs from it to
// each successor, and every task it governs gets a task.governance_superseded
// event (R5). Its task_governed_by rows stay where they are (S22).
// Withdrawing marks the accepted plans arranging the clause stale (S23,
// through SetClauseStatus). The whole map is one clause.superseded event and
// one transaction. A dry run resolves and counts, then rolls back.
//
// Every ref is resolved and validated before the first write. A successor
// that is withdrawn or also on the left side, an old clause listed twice,
// and an unparseable ref are ErrInvalidInput naming the ref; an unknown
// clause, document or anchor is ErrNotFound. Re-running an applied map
// changes nothing and reports zero counts.
func (s *Store) SupersedeClauses(ctx context.Context, project, actor string, in model.SupersedeInput) (res model.SupersedeResult, err error) {
	defer func() { s.metrics.clauseSupersede(supersedeOutcome(in.DryRun, err)) }()
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
	_, _, err = s.RecordEvent(ctx, "cli", extID, "clause.superseded", payload, func(tx *sql.Tx, eventID int64) error {
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

// resolveSupersede resolves every ref, locks the clauses involved in id order
// (two refactors over overlapping maps cannot deadlock), validates the map,
// and works out what applying it changes. It writes only what ensureClauses
// mints for a document that predates the clause tables. The lock is NO KEY
// UPDATE: a Govern insert holds KEY SHARE on the clause through the
// task_governed_by FK while its caller holds a plan row that
// SetClauseStatus's MarkPlansStale waits on, so FOR UPDATE would close a
// deadlock cycle.
func resolveSupersede(tx *sql.Tx, project string, entries []model.SupersedeEntry) ([]supersedeLine, model.SupersedeResult, error) {
	var res model.SupersedeResult
	if len(entries) == 0 {
		return nil, res, fmt.Errorf("empty supersede map: %w", ErrInvalidInput)
	}
	oldRef := map[int64]string{} // old clause -> the ref naming it in the map
	var lines []supersedeLine
	var newRefs [][]string
	var ids []int64
	for _, e := range entries {
		old, err := resolveClauseRef(tx, project, e.Old)
		if err != nil {
			return nil, res, err
		}
		if prev, dup := oldRef[old]; dup {
			return nil, res, fmt.Errorf("%s names the same clause as %s, already on the left side: %w", e.Old, prev, ErrInvalidInput)
		}
		oldRef[old] = e.Old
		l := supersedeLine{old: old}
		for _, n := range e.New {
			id, err := resolveClauseRef(tx, project, n)
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
		`SELECT c.id, c.status, p.key, c.number FROM clauses c JOIN projects p ON p.id = c.project_id
		  WHERE c.id = ANY($1) ORDER BY c.id FOR NO KEY UPDATE OF c`, ids)
	if err != nil {
		return nil, res, fmt.Errorf("lock clauses: %w", err)
	}
	for rows.Next() {
		var id, number int64
		var st, key string
		if err := rows.Scan(&id, &st, &key, &number); err != nil {
			rows.Close()
			return nil, res, err
		}
		status[id], ref[id] = st, key+"-CL-"+strconv.FormatInt(number, 10)
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
		// The plans SetClauseStatus will mark stale, counted before it runs.
		if err := tx.QueryRow(
			`SELECT count(DISTINCT d.id) FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
			  WHERE dc.clause_id = ANY($1) AND d.kind = 'plan' AND d.status = 'accepted' AND d.deleted_at IS NULL`,
			withdrawn).Scan(&res.StalePlans); err != nil {
			return nil, res, fmt.Errorf("count plans arranging withdrawn clauses: %w", err)
		}
	}
	return lines, res, nil
}

// applySupersede writes what resolveSupersede worked out, line by line.
func applySupersede(tx *sql.Tx, now time.Time, actor string, eventID int64, lines []supersedeLine, resolved []model.SupersedeResolved) error {
	for i, l := range lines {
		if l.withdraw {
			if err := SetClauseStatus(tx, now, l.old, "withdrawn", eventID); err != nil {
				return err
			}
		}
		for _, n := range l.edges {
			if _, err := tx.Exec(
				`INSERT INTO clause_edges (from_clause, to_clause, type, source) VALUES ($1, $2, 'supersededBy', 'refactor')
				 ON CONFLICT (from_clause, to_clause, type) DO NOTHING`, l.old, n); err != nil {
				return fmt.Errorf("supersede clause %d by %d: %w", l.old, n, err)
			}
		}
		for _, task := range l.tasks {
			extID, err := randomExternalID()
			if err != nil {
				return err
			}
			payload, err := EventPayload(map[string]any{
				"task":               task,
				"clause":             resolved[i].Old,
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

// resolveClauseRef resolves one map ref (R7): "WL-CL-12" by project key and
// number, which may name another project's clause (S32), or
// "WL-SPEC-4#sec-2" to the clause that document arranges at that anchor.
func resolveClauseRef(tx *sql.Tx, project, ref string) (int64, error) {
	if c, ok := designdoc.ParseClauseRef(ref); ok {
		return ClauseIDByRef(tx, c.Key, c.Number)
	}
	base, anchor := designdoc.SplitFragment(ref)
	if _, ok := designdoc.ParseShorthand(base); !ok || anchor == "" {
		return 0, fmt.Errorf("%q is neither a clause ref nor a section ref: %w", ref, ErrInvalidInput)
	}
	docID, ok, err := resolveDocRef(tx, project, base)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("%s: document %s: %w", ref, base, ErrNotFound)
	}
	id, ok, err := clauseAtAnchor(tx, docID, anchor)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", ref, err)
	}
	if !ok {
		return 0, fmt.Errorf("%s: no clause at that anchor: %w", ref, ErrNotFound)
	}
	return id, nil
}

// clauseAtAnchor is the clause a document arranges at anchor, after
// ensureClauses splits a document that predates the clause tables. A plan's
// doc_clauses rows borrow its covered clauses, so a plan anchor names none.
// The sibling increment 4a ships store.ClauseAtSection for the same lookup;
// whichever branch lands second deletes one of the two (R7).
func clauseAtAnchor(tx *sql.Tx, docID int64, anchor string) (int64, bool, error) {
	if err := ensureClauses(tx, docID); err != nil {
		return 0, false, err
	}
	var id int64
	err := tx.QueryRow(
		`SELECT dc.clause_id FROM doc_clauses dc JOIN docs d ON d.id = dc.doc_id
		  WHERE dc.doc_id = $1 AND dc.anchor = $2 AND d.kind <> 'plan'`, docID, anchor).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("clause at anchor %s of doc %d: %w", anchor, docID, err)
	}
	return id, true, nil
}

// supersededTargets is the set of clauses old already has a supersededBy
// edge to.
func supersededTargets(tx *sql.Tx, old int64) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT to_clause FROM clause_edges WHERE from_clause = $1 AND type = 'supersededBy'`, old)
	if err != nil {
		return nil, fmt.Errorf("supersededBy edges of clause %d: %w", old, err)
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

// governedTaskIDs lists the tasks a clause governs, in id order.
func governedTaskIDs(tx *sql.Tx, clauseID int64) ([]string, error) {
	rows, err := tx.Query(`SELECT task_id FROM task_governed_by WHERE clause_id = $1 ORDER BY task_id`, clauseID)
	if err != nil {
		return nil, fmt.Errorf("tasks governed by clause %d: %w", clauseID, err)
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

// supersedeOutcome is the worklode_clause_supersede_total label for one call.
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
