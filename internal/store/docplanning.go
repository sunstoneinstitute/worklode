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

// acceptPlanDoc is AcceptDoc's plan branch (WL-REQ-172): parse the plan body's
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
// Accepting a stale plan runs the same code and is how WL-REQ-171's mark clears:
// the re-planning edit bumped the plan's version and may have added
// declarations, so the mint pass above picks those up and the status flip
// below records stale -> accepted like any other move.
//
// Plans carry no sections and no anchors (WL-REQ-172), so none of the spec/ADR
// branch's section or diff machinery runs here: there is nothing to publish
// and no depth gate to evaluate. d.status is already known draft, accepted or
// stale — AcceptDoc checks it before branching.
func acceptPlanDoc(tx *sql.Tx, now time.Time, id int64, d lockedDoc, actorID string, eventID int64) (*model.Doc, []model.Task, error) {
	parsed, err := parseDocBody(d.kind, d.body)
	if err != nil {
		return nil, nil, err
	}
	// A coverage-only plan declares no tasks and mints none: its whole content
	// is the coverage it records for work already built, and accepting it is
	// what puts those claims in force (WL-REQ-186). Everything below runs over an
	// empty definition set and is a no-op.
	defs, err := designdoc.PlanTasks(parsed.doc)
	if err != nil {
		coverageOnly, cerr := isCoverageOnlyPlan(tx, id, parsed.doc)
		if cerr != nil {
			return nil, nil, cerr
		}
		if coverageOnly {
			err = nil
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("doc %d cannot be accepted: %w: %w", id, err, ErrInvalidInput)
	}
	minted, err := plantaskRows(tx, id)
	if err != nil {
		return nil, nil, err
	}
	governing, err := planRules(tx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve governing rules of plan %d: %w", id, err)
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
		for _, ruleID := range governing {
			if err := Govern(tx, task.ID, ruleID, "plan", false); err != nil {
				return nil, nil, fmt.Errorf("govern task %d of plan %d: %w", def.Number, id, err)
			}
		}
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
// (WL-REQ-172): what acceptPlanDoc matches declarations against, and what
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

// PlanTaskIDs is a plan's live minted tasks as declaration title -> task id,
// the same plan_task_key match acceptPlanDoc mints against. It is how a reader
// of the plan — the document page — names the task each `### Task N` heading
// produced.
//
// Soft-deleted tasks are left out, unlike plantaskRows: a withdrawn task is
// not something to point a reader at, whereas the mint path must still see it
// to avoid minting the declaration again.
func (s *Store) PlanTaskIDs(ctx context.Context, docID int64) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT plan_task_key, id FROM tasks
		  WHERE plan_doc = $1 AND plan_task_key IS NOT NULL AND deleted_at IS NULL`, docID)
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
// to read (WL-REQ-172). Without it an accepted plan's declarations could be
// rewritten into something unparseable, and the drift between the document and
// its task set would surface only at the next accept — or never.
//
// It binds only once something has been minted. A draft plan is written a
// paragraph at a time and its ## Tasks section is legitimately incomplete
// until the accept gate reads it, and an accepted plan that minted nothing is
// WL-REQ-172's historical import, which never had a task set to stay consistent
// with.
//
// What it does not refuse is a declaration that disappeared or was retitled.
// WL-REQ-172 is explicit that a minted task outlives its declaration — withdrawing
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

// checkPlanOrdering enforces that a blockedBy edge runs between two *distinct*
// plan documents (WL-REQ-164): it orders plan against plan, and
// planBlockedCondition reads its to end as the blocking plan's whole task set. An
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
// docKind is the declaring document's own kind, which every caller already
// holds (from lockDoc or from the create input) — re-reading it here would
// cost a query per blockedBy edge in the frontmatter. The declaring document
// is the row's from end (the blocked plan); toDoc is the named, blocking plan.
func checkPlanOrdering(tx *sql.Tx, docID int64, docKind, ref string, toDoc int64, resolved bool) error {
	if !resolved {
		return fmt.Errorf(
			"blockedBy edge from doc %d names %q, which no plan in this project resolves to (WL-SPEC-77 §8): %w",
			docID, ref, ErrInvalidInput)
	}
	if toDoc == docID {
		return fmt.Errorf(
			"blockedBy edge from doc %d names %q, itself: a plan cannot block itself (WL-SPEC-77 §8): %w",
			docID, ref, ErrInvalidInput)
	}
	var toKind string
	if err := tx.QueryRow(`SELECT kind FROM docs WHERE id = $1`, toDoc).Scan(&toKind); err != nil {
		return fmt.Errorf("read kind of doc %d: %w", toDoc, err)
	}
	if docKind != "plan" {
		return fmt.Errorf("blockedBy orders plan documents, but the from end (doc %d) is a %s (WL-SPEC-77 §8): %w",
			docID, docKind, ErrInvalidInput)
	}
	if toKind != "plan" {
		return fmt.Errorf("blockedBy orders plan documents, but the to end (doc %d) is a %s (WL-SPEC-77 §8): %w",
			toDoc, toKind, ErrInvalidInput)
	}
	back, err := blocksPath(tx, toDoc, docID)
	if err != nil {
		return err
	}
	if back != nil {
		chain, err := blocksChainText(tx, append([]int64{docID}, back...))
		if err != nil {
			return err
		}
		return fmt.Errorf("blockedBy edge from doc %d names %q, closing the cycle %s (WL-SPEC-77 §8): %w",
			docID, ref, chain, ErrInvalidInput)
	}
	return nil
}

// blocksPath returns the documents on a path from start to target over stored
// `blockedBy` edges — start first, target last — or nil when target is
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
			  WHERE from_doc = $1 AND type = 'blockedBy' AND to_doc IS NOT NULL`, cur)
		if err != nil {
			return nil, fmt.Errorf("walk blockedBy edges of doc %d: %w", cur, err)
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

// blocksChainText renders a chain of document ids as "a blockedBy b blockedBy a", by
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
	return strings.Join(parts, " blockedBy "), nil
}

// NeedsPlanning returns the accepted specs that have at least one section not
// planned, each with the anchors that made it a gap and why (WL-SPEC-78
// §1.3). project narrows the answer; "" answers over every project.
//
// A covers edge means the plan builds the whole rule, so a section is planned
// when some accepted or superseded plan covers it and no draft plan also
// does. A superseded plan is spent (accepted, then executed) and discharges
// what it covered. A covers edge runs from the plan to a rule and claims
// every section arranging the rule or a rule superseding it (the
// covered_sections view, WL-REQ-165). Only a section whose rule is a
// requirement is counted or reported (WL-REQ-187).
//
// An unplanned section reports the strongest outcome that applies:
// "plan-draft" when a draft plan covers it, "deferred" when an accepted or
// superseded plan defers it to a named owner (reported beside the anchor: a
// slug, or the reference verbatim when it did not resolve), "unplanned"
// otherwise. A draft plan's deferral classifies nothing. `covers: NO-SPEC`
// resolves to no rule and falls out of the join, and a tombstoned document
// participates on neither end (WL-REQ-117).
func (s *Store) NeedsPlanning(ctx context.Context, project string) ([]model.Doc, []model.DocPlanningGap, error) {
	rows, err := s.db.QueryContext(ctx,
		`WITH cov AS (
		     SELECT cs.doc_id, cs.anchor,
		            bool_or(p.status IN ('accepted','superseded','spent')) AS discharging,
		            bool_or(p.status = 'draft')                           AS draft
		       FROM covered_sections cs
		       JOIN docs p ON p.id = cs.plan_id
		      WHERE p.kind = 'plan' AND p.deleted_at IS NULL
		      GROUP BY cs.doc_id, cs.anchor
		 ),
		 def AS (
		     SELECT e.to_doc AS doc_id, e.to_anchor AS anchor,
		            -- Comma without a space: the CLI joins anchors with spaces,
		            -- so a spaced separator would split one gap across tokens.
		            string_agg(DISTINCT coalesce(o.slug, e.owner_external), ','
		                       ORDER BY coalesce(o.slug, e.owner_external)) AS owner
		       FROM doc_edges e
		       JOIN docs p ON p.id = e.from_doc
		       LEFT JOIN docs o ON o.id = e.owner_doc
		      WHERE e.type = 'defers'
		        AND e.to_doc IS NOT NULL AND e.to_anchor IS NOT NULL
		        AND p.kind = 'plan' AND p.status IN ('accepted','superseded','spent')
		        AND p.deleted_at IS NULL
		      GROUP BY e.to_doc, e.to_anchor
		 )
		 SELECT `+docColumnsD+`, count(*)::int,
		        coalesce(json_agg(json_strip_nulls(json_build_object(
		                     'anchor', sec.anchor,
		                     'coverage', CASE WHEN coalesce(c.draft, false) THEN 'plan-draft'
		                                      WHEN def.doc_id IS NOT NULL   THEN 'deferred'
		                                      ELSE 'unplanned' END,
		                     'owner', CASE WHEN NOT coalesce(c.draft, false) THEN def.owner END))
		                 ORDER BY sec.position)
		                 FILTER (WHERE NOT coalesce(c.discharging AND NOT c.draft, false)), '[]')::text
		   FROM docs d
		   JOIN doc_sections sec ON sec.doc_id = d.id
		   LEFT JOIN doc_rules dr ON dr.doc_id = sec.doc_id AND dr.position = sec.position
		   LEFT JOIN rules r ON r.id = dr.rule_id
		   LEFT JOIN cov c ON c.doc_id = sec.doc_id AND c.anchor = sec.anchor
		   LEFT JOIN def ON def.doc_id = sec.doc_id AND def.anchor = sec.anchor
		  WHERE d.kind = 'spec' AND d.status = 'accepted'
		    AND d.deleted_at IS NULL
		    AND coalesce(r.kind, 'requirement') IN ('requirement', 'catalogue')
		    AND dr.heading IS NULL
		    AND ($1 = '' OR d.project_id = $1)
		  GROUP BY d.id
		 HAVING count(*) FILTER (WHERE NOT coalesce(c.discharging AND NOT c.draft, false)) > 0
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
// tombstoned task is out on top of that (WL-REQ-117), matching planUnfinished.
//
// This departs from WL-REQ-182's "unminted or unfinished" deliberately, as the
// 2026-08-03 plan-acceptance plan records: the accepted plans with no task set
// at all are the importer's *spent* plans, which must not be reported as
// pending work. The ordering need WL-REQ-182's "unminted" arm served is covered by
// the plan-to-plan blocks predicate (planBlockedCondition).
//
// A declaration added to an accepted plan and not yet re-accepted (WL-REQ-172)
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

// isCoverageOnlyPlan is designdoc.IsCoverageOnlyPlan over a stored plan: the
// body declares no tasks and the plan holds a covers or defers edge, which is
// where its coverage lives once the body carries no header (WL-REQ-168).
func isCoverageOnlyPlan(tx *sql.Tx, id int64, doc *designdoc.Document) (bool, error) {
	if !designdoc.DeclaresNoTasks(doc) {
		return false, nil
	}
	var ok bool
	if err := tx.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM doc_edges WHERE from_doc = $1 AND type IN ('covers', 'defers'))`,
		id).Scan(&ok); err != nil {
		return false, fmt.Errorf("read coverage of plan %d: %w", id, err)
	}
	return ok, nil
}
