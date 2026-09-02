package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// acceptPlanDoc is AcceptDoc's plan branch (025 §9.2): parse the plan body's
// ## Tasks declarations, mint one draft task per declaration that has no row
// yet with plan_doc set to id, wire the newly minted tasks' blockedBy numbers
// as blocks edges, then flip the document to accepted — all inside the
// caller's transaction, so accept and mint are one commit and a failed mint
// leaves the document as it was.
//
// Re-accepting an accepted plan runs the same code: a declaration whose title
// already names a row is left alone — no re-mint, no field overwrite, no
// state change — so a re-accept of an unedited plan mints nothing and is a
// safe no-op. The match is on plan_task_key, the declaration title recorded at
// mint, which is why a title edit inside the plan reads as withdrawing one
// declaration and adding another: a minted task is execution fact and outlives
// its declaration, so nothing here deletes a task whose declaration is gone.
//
// Plans carry no sections and no anchors (025 §9), so none of the spec/ADR
// branch's section or diff machinery runs here: there is nothing to publish
// and no depth gate to evaluate. d.status is already known draft or accepted —
// AcceptDoc checks it before branching.
func acceptPlanDoc(tx *sql.Tx, now time.Time, id int64, d lockedDoc, actorID string, eventID int64) (*model.Doc, []model.Task, error) {
	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, err
	}
	defs, err := designdoc.PlanTasks(parsed.doc)
	if err != nil {
		return nil, nil, fmt.Errorf("doc %d cannot be accepted: %w: %w", id, err, ErrInvalidInput)
	}
	minted, err := plantaskRows(tx, id)
	if err != nil {
		return nil, nil, err
	}

	// First pass resolves every definition number to a task id — minting the
	// ones that have none — and the second wires blockedBy once every number
	// resolves, so a forward reference (task 1 blockedBy task 2) needs no
	// reordering.
	taskID := make(map[int]string, len(defs))
	fresh := make(map[int]bool, len(defs))
	tasks := make([]model.Task, 0, len(defs))
	for _, def := range defs {
		if existing, ok := minted[def.Title]; ok {
			taskID[def.Number] = existing
			continue
		}
		task, err := CreateTask(tx, now, TaskInput{
			ProjectID:   d.project,
			Title:       def.Title,
			Body:        def.Body,
			Priority:    def.Priority,
			Kind:        def.Kind,
			Skills:      def.Skills,
			CreatedBy:   actorID,
			PlanDoc:     id,
			PlanTaskKey: def.Title,
		}, eventID)
		if err != nil {
			return nil, nil, fmt.Errorf("mint task %d of plan %d: %w", def.Number, id, err)
		}
		taskID[def.Number] = task.ID
		fresh[def.Number] = true
		tasks = append(tasks, *task)
	}
	// Only a newly minted task gets its declared blockers wired. An edge into
	// a task that already exists would change how that task ranks and when it
	// is claimable — a change to an existing row, which re-acceptance does not
	// make.
	for _, def := range defs {
		if !fresh[def.Number] {
			continue
		}
		for _, blocker := range def.BlockedBy {
			if err := AddEdge(tx, now, taskID[blocker], taskID[def.Number], "blocks", eventID); err != nil {
				return nil, nil, fmt.Errorf(
					"wire blocks edge task %d -> %d of plan %d: %w", blocker, def.Number, id, err)
			}
		}
	}

	ts := now.UTC().Truncate(time.Second)
	if _, err := tx.Exec(
		`UPDATE docs SET status = 'accepted', updated_at = $2 WHERE id = $1`, id, ts); err != nil {
		return nil, nil, fmt.Errorf("accept doc %d: %w", id, err)
	}
	// A first accept logs the status move; a re-accept logs what it minted,
	// because the status did not move and an "accepted -> accepted" line would
	// say nothing about what changed.
	change := map[string]string{"field": "status", "old": d.status, "new": "accepted"}
	if d.status == "accepted" {
		change = map[string]string{"field": "plan_tasks", "new": strconv.Itoa(len(tasks))}
	}
	if err := logDocChange(tx, id, eventID, change); err != nil {
		return nil, nil, err
	}
	doc, err := getDocTx(tx, id)
	if err != nil {
		return nil, nil, err
	}
	return doc, tasks, nil
}

// plantaskRows reads a plan's minted task set as declaration title -> task id
// (025 §9.2): what acceptPlanDoc matches declarations against, and what
// checkPlanTasksMinted uses to decide whether a body edit has a task set to
// stay consistent with.
//
// Soft-deleted tasks are included deliberately. A deleted task is withdrawn
// work, not absent work; skipping it here would have the next re-accept mint
// its declaration again and undo the withdrawal, and the partial unique index
// on (plan_doc, plan_task_key) would refuse the insert anyway.
func plantaskRows(tx *sql.Tx, docID int64) (map[string]string, error) {
	rows, err := tx.Query(
		`SELECT plan_task_key, id FROM tasks WHERE plan_doc = $1`, docID)
	if err != nil {
		return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
	}
	defer rows.Close()
	minted := map[string]string{}
	for rows.Next() {
		var key, taskID string
		if err := rows.Scan(&key, &taskID); err != nil {
			return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
		}
		minted[key] = taskID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read minted tasks of plan %d: %w", docID, err)
	}
	return minted, nil
}

// checkPlanTasksMinted refuses a plan body edit that would leave a plan whose
// tasks are already minted without the valid ## Tasks section a re-accept has
// to read (025 §9.2). Without it an accepted plan's declarations could be
// rewritten into something unparseable, and the drift between the document and
// its task set would surface only at the next accept — or never.
//
// It binds only once something has been minted. A draft plan is written a
// paragraph at a time and its ## Tasks section is legitimately incomplete
// until the accept gate reads it, and an accepted plan that minted nothing is
// §9.2's historical import, which never had a task set to stay consistent
// with.
//
// What it does not refuse is a declaration that disappeared or was retitled.
// §9.2 is explicit that a minted task outlives its declaration — withdrawing
// work is a task transition, not a document edit — so an edit that drops one
// leaves the row alone, and one that retitles it declares a task the next
// re-accept mints. Only the ambiguity a re-accept cannot resolve is an error,
// and designdoc.PlanTasks names it.
func checkPlanTasksMinted(tx *sql.Tx, id int64, doc *designdoc.Document) error {
	minted, err := plantaskRows(tx, id)
	if err != nil {
		return err
	}
	if len(minted) == 0 {
		return nil
	}
	if _, err := designdoc.PlanTasks(doc); err != nil {
		return fmt.Errorf(
			"doc %d has %d minted task(s), so its \"## Tasks\" section must stay readable: %w: %w",
			id, len(minted), err, ErrInvalidInput)
	}
	return nil
}

// checkPlanOrdering enforces that a blocks edge runs between two *distinct*
// plan documents (025 §5): it orders plan against plan, and
// planBlockedCondition reads it as the blocking plan's whole task set. An
// unresolved reference is refused too — nothing here can say it names a plan,
// and a to_external ordering edge would gate nothing while looking like it
// did.
//
// A plan naming its own slug resolves to itself (resolveDocRef matches within
// the project), which would wedge that plan's task set forever: with
// from_doc = to_doc its own open tasks block themselves, and while it is draft
// the unminted-set arm blocks too. A cycle through two or more plans wedges
// them the same way — each plan's tasks are held by the next plan's open set,
// so no set can ever close — and plans stay mutable at any status, so it is
// only the write closing the cycle that can catch it. Both are refused here,
// the way AddEdge refuses a child_of cycle between tasks (WL-144).
//
// docKind is the *declaring* document's own kind, which every caller already
// holds (from lockDoc or from the create input) — re-reading it here would
// cost a query per blocks edge in the frontmatter. inverse says which end of
// the row the declaring document is: false for `blocks:`, where it is the from
// end, true for `blockedBy:`, where it is the to end (025 §5). Every check
// below is about the row, so it reads the ends rather than the author — which
// is why the two spellings cannot disagree about what is legal.
func checkPlanOrdering(tx *sql.Tx, docID int64, docKind, ref string, otherDoc int64, resolved, inverse bool) error {
	if !resolved {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, which no plan in this project resolves to (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	if otherDoc == docID {
		return fmt.Errorf(
			"blocks edge from doc %d names %q, itself: a plan cannot block itself (025 §5): %w",
			docID, ref, ErrInvalidInput)
	}
	var otherKind string
	if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1`, otherDoc).Scan(&otherKind); err != nil {
		return fmt.Errorf("read kind of doc %d: %w", otherDoc, err)
	}
	fromDoc, fromKind, toDoc, toKind := docID, docKind, otherDoc, otherKind
	if inverse {
		fromDoc, fromKind, toDoc, toKind = otherDoc, otherKind, docID, docKind
	}
	// The from end first, matching the order the two-query loop reported in.
	if fromKind != "plan" {
		return fmt.Errorf("blocks orders plan documents, but the from end (doc %d) is a %s (025 §5): %w",
			fromDoc, fromKind, ErrInvalidInput)
	}
	if toKind != "plan" {
		return fmt.Errorf("blocks orders plan documents, but the to end (doc %d) is a %s (025 §5): %w",
			toDoc, toKind, ErrInvalidInput)
	}
	back, err := blocksPath(tx, toDoc, fromDoc)
	if err != nil {
		return err
	}
	if back != nil {
		chain, err := blocksChainText(tx, append([]int64{fromDoc}, back...))
		if err != nil {
			return err
		}
		return fmt.Errorf("blocks edge from doc %d names %q, closing the cycle %s (025 §5): %w",
			docID, ref, chain, ErrInvalidInput)
	}
	return nil
}

// blocksPath returns the documents on a path from start to target over stored
// `blocks` edges — start first, target last — or nil when target is
// unreachable. checkPlanOrdering walks it from the proposed edge's *to* end
// back towards its *from* end: a path that arrives means the proposed edge
// closes a cycle.
//
// The walk reads what is stored, and rebuildEdges clears the writing
// document's own edges before re-inserting them one at a time, so a rewrite
// never trips over the row it is about to replace. Only resolved edges
// (to_doc) are walked — an unresolved reference names no document, and
// checkPlanOrdering refuses one anyway. Breadth-first over a visited set, so
// the chain reported is a shortest one and the walk terminates even on a graph
// that is already cyclic. A start == target self-edge is not reported here;
// checkPlanOrdering refuses that case before it gets this far.
func blocksPath(tx *sql.Tx, start, target int64) ([]int64, error) {
	prev := map[int64]int64{}
	visited := map[int64]bool{start: true}
	frontier := []int64{start}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]

		rows, err := tx.Query(
			`SELECT to_doc FROM doc_edges
			  WHERE from_doc = $1 AND type = 'blocks' AND to_doc IS NOT NULL`, cur)
		if err != nil {
			return nil, fmt.Errorf("walk blocks edges of doc %d: %w", cur, err)
		}
		var next []int64
		for rows.Next() {
			var to int64
			if err := rows.Scan(&to); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan blocks edge of doc %d: %w", cur, err)
			}
			next = append(next, to)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("walk blocks edges of doc %d: %w", cur, err)
		}
		rows.Close()

		for _, to := range next {
			if visited[to] {
				continue
			}
			prev[to] = cur
			if to == target {
				path := []int64{to}
				for at := to; at != start; {
					at = prev[at]
					path = append([]int64{at}, path...)
				}
				return path, nil
			}
			visited[to] = true
			frontier = append(frontier, to)
		}
	}
	return nil, nil
}

// blocksChainText renders a chain of document ids as "a blocks b blocks a", by
// slug, so a refused write names the cycle rather than just reporting one. A
// document whose slug cannot be read falls back to its id — the caller is
// already on an error path and a lookup failure must not mask what refused the
// write.
func blocksChainText(tx *sql.Tx, chain []int64) (string, error) {
	parts := make([]string, 0, len(chain))
	for _, id := range chain {
		var slug string
		if err := tx.QueryRow(`SELECT slug FROM docs WHERE id = $1`, id).Scan(&slug); err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("read slug of doc %d: %w", id, err)
			}
			slug = fmt.Sprintf("doc %d", id)
		}
		parts = append(parts, slug)
	}
	return strings.Join(parts, " blocks "), nil
}

// NeedsPlanning returns the accepted specs that have at least one section no
// accepted or superseded plan discharges, each with the anchors that made it
// a gap and why (026 §2.1). project narrows the answer; "" answers over every
// project.
//
// The discharging set is not `accepted` alone: 026 §2.1's "A superseded plan
// discharges what it covered" reads the status as "not draft", since a
// superseded plan is one that was accepted and then carried out (025 §9's "a
// plan is spent once executed") — reading the set as `accepted` alone would
// report a shipped third of the corpus as unplanned work. A section is
// discharged when some such plan's `covers` edge claims it `full`, or claims
// it `partial` with a `fullCoverageWith` set that closes: every named plan is
// itself accepted or superseded and itself contributes `full` or `partial` to
// that same section. `fullCoverageWith` is checked, never taken on trust — an
// empty list, an unresolved reference, a draft target, a `none` target, or a
// target that does not itself cover the section all leave it open.
//
// An undischarged section is classified by the strongest reading that holds,
// in order: "partial" when some accepted-or-superseded plan claims it
// `partial` (whether or not that claim closed); "deferred" when none claims
// `partial` but some such plan hands it off to a named owner with `defers`
// (026 §5.3) — the report names the owner, recovered from the same
// doc_coverage_completed_with row a partial entry's fullCoverageWith uses,
// because a deferral is that same assertion read at level zero; "bound-only"
// when every accepted-or-superseded plan naming it claims `none`; "unplanned"
// when no such plan names it, deferral included, at all. A deferral is
// delivered by any covering plan discharging the section under the rules
// above, so it is checked against the same `cov`/`closed` machinery as
// covers, not against who was named.
//
// Four further consequences are deliberate:
//
//   - A whole-document edge (to_anchor IS NULL) discharges nothing. It cannot
//     say which present section it undertakes and would silently claim future
//     ones (026 §2.1), so it never appears in the discharged set.
//   - `covers: NO-SPEC` resolves to no row and lands in to_external (026
//     §4.3), so it falls out of the join without a case of its own.
//   - Only an accepted spec and an accepted-or-superseded plan participate: a
//     draft spec is not yet owed planning, and a draft plan has not yet
//     undertaken work — its `defers` entries do not classify a section either.
//     A tombstoned document participates on neither end (044 §4) — it is
//     neither owed planning nor able to discharge or defer a section.
//   - A deferral's owner is reported however it resolved at write time: a
//     slug when the reference named a live document, the reference text
//     verbatim (`w.to_external`) when it did not — the same fallback
//     fullCoverageWith uses.
//
// A plan naming itself in its own `fullCoverageWith` closes its own section.
// §2.1's closure test is only that each named plan is accepted or superseded
// and contributes `full` or `partial` — it says nothing about the naming plan
// — so this is not a bug; narrowing it to siblings would be a spec change
// (tracked in docs/follow-ups.md).
func (s *Store) NeedsPlanning(ctx context.Context, project string) ([]model.Doc, []model.DocPlanningGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH cov AS (
		     SELECT e.id, e.from_doc AS plan_id, e.to_doc AS doc_id,
		            e.to_anchor AS anchor, e.coverage
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		      WHERE e.type = 'covers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status IN ('accepted','superseded')
		        AND p.deleted_at IS NULL
		 ),
		 def_raw AS (
		     SELECT e.to_doc AS doc_id, e.to_anchor AS anchor,
		            coalesce(owner_doc.slug, w.to_external) AS owner
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		       JOIN doc_coverage_completed_with w ON w.edge_id = e.id
		       LEFT JOIN docs owner_doc ON owner_doc.id = w.to_doc
		      WHERE e.type = 'defers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status IN ('accepted','superseded')
		        AND p.deleted_at IS NULL
		 ),
		 def AS (
		     SELECT doc_id, anchor,
		            -- Comma without a space: the CLI joins anchors with spaces,
		            -- so a spaced separator would split one gap across tokens.
		            string_agg(DISTINCT owner, ',' ORDER BY owner) AS owner
		       FROM def_raw
		      GROUP BY doc_id, anchor
		 ),
		 closed AS (
		     SELECT c.id
		       FROM cov c
		      WHERE c.coverage = 'partial'
		        AND EXISTS (SELECT 1 FROM doc_coverage_completed_with w
		                     WHERE w.edge_id = c.id)
		        AND NOT EXISTS (
		              SELECT 1 FROM doc_coverage_completed_with w
		               WHERE w.edge_id = c.id
		                 AND NOT EXISTS (
		                       SELECT 1 FROM cov o
		                        WHERE o.plan_id = w.to_doc
		                          AND o.doc_id = c.doc_id AND o.anchor = c.anchor
		                          AND o.coverage IN ('full','partial')))
		 ),
		 resolved AS (
		     SELECT c.doc_id, c.anchor,
		            bool_or(c.coverage = 'full' OR cl.id IS NOT NULL) AS discharged,
		            bool_or(c.coverage = 'partial')                   AS any_partial
		       FROM cov c
		       LEFT JOIN closed cl ON cl.id = c.id
		      GROUP BY c.doc_id, c.anchor
		 )
		 SELECT `+docColumnsD+`, count(*)::int,
		        coalesce(json_agg(json_strip_nulls(json_build_object(
		                     'anchor', sec.anchor,
		                     'coverage', CASE WHEN coalesce(r.any_partial, false) THEN 'partial'
		                                      WHEN def.doc_id IS NOT NULL         THEN 'deferred'
		                                      WHEN r.doc_id IS NOT NULL           THEN 'bound-only'
		                                      ELSE 'unplanned' END,
		                     'owner', CASE WHEN NOT coalesce(r.any_partial, false)
		                                        AND def.doc_id IS NOT NULL
		                                   THEN def.owner END))
		                 ORDER BY sec.position)
		                 FILTER (WHERE r.discharged IS NOT TRUE), '[]')::text
		   FROM docs d
		   JOIN doc_sections sec ON sec.doc_id = d.id
		   LEFT JOIN resolved r ON r.doc_id = sec.doc_id AND r.anchor = sec.anchor
		   LEFT JOIN def ON def.doc_id = sec.doc_id AND def.anchor = sec.anchor
		  WHERE d.kind = 'spec' AND d.status = 'accepted'
		    AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		  GROUP BY d.id
		 HAVING count(*) FILTER (WHERE r.discharged IS NOT TRUE) > 0
		  ORDER BY d.project_id, d.number NULLS LAST, d.slug`, project)
	if err != nil {
		return nil, nil, fmt.Errorf("list specs needing planning: %w", err)
	}
	defer rows.Close()

	var docs []model.Doc
	var gaps []model.DocPlanningGap
	for rows.Next() {
		var gap model.DocPlanningGap
		var gapsJSON string
		d, err := scanDoc(appendScan{rows, []any{&gap.Sections, &gapsJSON}})
		if err != nil {
			return nil, nil, fmt.Errorf("scan spec needing planning: %w", err)
		}
		if err := json.Unmarshal([]byte(gapsJSON), &gap.Gaps); err != nil {
			return nil, nil, fmt.Errorf("decode planning gaps of doc %d: %w", d.ID, err)
		}
		gap.Doc = d.ID
		docs = append(docs, *d)
		gaps = append(gaps, gap)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("list specs needing planning: %w", err)
	}
	return docs, gaps, nil
}

// NeedsExecution returns the accepted plans whose task set holds at least one
// live task that is not closed. project narrows the answer; "" answers over
// every project. "Closed" is taskClosed's notion, shared with the ready set and
// the blocks predicate, so the three cannot drift on what done means; a
// tombstoned task is out on top of that (044 §4), matching planUnfinished.
//
// This departs from 025 §18's "unminted or unfinished" deliberately, as the
// 2026-08-03 plan-acceptance plan records: the accepted plans with no task set
// at all are the importer's *spent* plans, which must not be reported as
// pending work. The ordering need §18's "unminted" arm served is covered by
// the plan-to-plan blocks predicate (planBlockedCondition).
//
// A declaration added to an accepted plan and not yet re-accepted (025 §9.2)
// is invisible here, because whether one exists is a fact about the body and
// not about any row. Re-accepting the plan is what makes it visible; nothing
// SQL can see says it is owed.
func (s *Store) NeedsExecution(ctx context.Context, project string) ([]model.Doc, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+docColumnsD+`
		   FROM docs d
		  WHERE d.kind = 'plan' AND d.status = 'accepted'
		    AND d.deleted_at IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		    AND EXISTS (SELECT 1 FROM tasks t
		                 WHERE t.plan_doc = d.id AND t.deleted_at IS NULL
		                   AND NOT `+taskClosed("t")+`)
		  ORDER BY d.project_id, d.slug`, project)
	if err != nil {
		return nil, fmt.Errorf("list plans needing execution: %w", err)
	}
	return collectRows(rows, "list plans needing execution", byValue(scanDoc))
}

// RecordPlanTasksMinted records n tasks minted by one plan accept
// (worklode_doc_plan_tasks_minted_total). AcceptDoc is a package-level
// function with no *Store to record through, so the caller — the API's
// acceptDoc handler — calls this once the accepting transaction has
// committed, with the length of AcceptDoc's minted-task return. Nil-safe:
// a store opened without WithMetrics records nothing.
func (s *Store) RecordPlanTasksMinted(n int) {
	s.metrics.planTasksMinted(n)
}
