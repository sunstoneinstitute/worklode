// rally.go holds what the rally kind needs: a project's active rally, the
// draft rally the Progress page assembles into, the membership set that
// steers ranking, and the writes that fill a draft. A rally carries no work
// of its own — the 'blocks' edges pointing at it are its whole content,
// naming the tasks a human picked as the thing to finish now.
//
// A project holds at most one DRAFT rally and at most one ACTIVE one, each
// held singular by its own partial unique index (0069, 0072). A draft is
// inert: it contributes no members and changes no ranking, so `lode work
// next` behaves as if it were not there. `lode task publish` (draft ->
// ready) is what activates one.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// rallyInactiveStates is the SQL literal list of the states a rally steers
// nothing in: 'draft' plus every state in deliveredStateSet
// (openWorkExcludedStates). It renders migration 0069's index predicate, and
// TestRallyIndexPredicateMatchesGo reads that index's definition back from
// Postgres to hold the two together.
var rallyInactiveStates = `'draft', ` + openWorkExcludedStates

// rallyActiveCondition renders "this row is an active rally" for the tasks
// row aliased as alias — migration 0069's partial unique index predicate.
// That index is what makes "the" active rally singular per project, so a
// query asking a different question could report two.
//
// taskClosed is deliberately not the predicate here. taskClosed is per repo —
// a merged task stays open where done_state is 'released' — so it would
// disagree with the index in exactly the corner the index exists to protect.
func rallyActiveCondition(alias string) string {
	return alias + `.kind = 'rally' AND ` + alias + `.deleted_at IS NULL
	    AND ` + alias + `.state NOT IN (` + rallyInactiveStates + `)`
}

// rallyDraftCondition renders "this row is the draft rally" for the tasks row
// aliased as alias — migration 0072's partial unique index predicate, which
// is what makes the draft rally singular per project.
func rallyDraftCondition(alias string) string {
	return alias + `.kind = 'rally' AND ` + alias + `.state = 'draft'
	    AND ` + alias + `.deleted_at IS NULL`
}

// ActiveRally returns projectID's active rally, or ErrNotFound when it has
// none. A draft rally is not one.
func (s *Store) ActiveRally(ctx context.Context, projectID string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumnsT+` FROM tasks t
		  WHERE t.project_id = $1 AND `+rallyActiveCondition("t"), projectID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		s.metrics.rallyRead("none")
		return nil, ErrNotFound
	}
	if err != nil {
		s.metrics.rallyRead("error")
		return nil, fmt.Errorf("active rally of %s: %w", projectID, err)
	}
	s.metrics.rallyRead("ok")
	return t, nil
}

// DraftRally returns projectID's draft rally, or ErrNotFound when it has
// none — the rally WL-SPEC-66 §3.5's Rally button adds to. It is not counted
// under worklode_rally_reads_total: that counter is about the active rally,
// the one that steers ranking.
func (s *Store) DraftRally(ctx context.Context, projectID string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumnsT+` FROM tasks t
		  WHERE t.project_id = $1 AND `+rallyDraftCondition("t"), projectID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("draft rally of %s: %w", projectID, err)
	}
	return t, nil
}

// RallyMemberCount returns how many live tasks a rally's direct membership
// names: the 'blocks' edges pointing at it, whatever state those tasks are
// in. The tombstone filter is the point — ListEdges has none while
// blockerRelation does, so a total taken from the raw edges would keep a
// soft-deleted member in the denominator while the open set dropped it, and
// the cockpit card would read it as done.
func (s *Store) RallyMemberCount(ctx context.Context, rallyID string) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM task_edges e
		   JOIN tasks f ON f.id = e.from_task AND f.deleted_at IS NULL
		  WHERE e.to_task = $1 AND e.type = 'blocks'`, rallyID).Scan(&n); err != nil {
		return 0, fmt.Errorf("rally member count for %s: %w", rallyID, err)
	}
	return n, nil
}

// rallyMembers returns the transitive open-blocker closure of every ACTIVE
// rally, every project in one query: the set of task ids some rally is
// waiting on, directly or through another blocker. A draft rally seeds
// nothing, which is what makes it inert. rankedFrontier calls this on every
// ranking pass, so it takes no project filter — the ready set it feeds may
// span projects.
//
// The relation walked is blockerRelation, the one `lode task blockers` reports
// and openBlockers gates claims on, so rally membership and the blocker view
// cannot disagree about what blocks what — including about what "open" means.
// Recursion is UNION over one column, so a blocks-cycle stops at its repeat;
// nothing validates 'blocks' against cycles on write.
func (s *Store) rallyMembers(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `
WITH RECURSIVE blockers(blocked, id, title, state, project_id) AS (`+blockerRelation+`),
members(id) AS (
    SELECT b.id FROM blockers b
      JOIN tasks r ON r.id = b.blocked AND (`+rallyActiveCondition("r")+`)
  UNION
    SELECT b.id FROM members m JOIN blockers b ON b.blocked = m.id
)
SELECT id FROM members`)
	if err != nil {
		return nil, fmt.Errorf("rally members: %w", err)
	}
	defer rows.Close()

	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan rally member: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rally members: %w", err)
	}
	return out, nil
}

// rallyUniqueConflict turns either rally uniqueness violation —
// tasks_one_active_rally (0069) or tasks_one_draft_rally (0072) — into a
// sentinel. Four writes can trip one, every write that can put a row into
// either index, which means creating one, or moving one of the three columns
// their predicates read:
//   - creating a rally, draft or not (CreateTask)
//   - publishing a draft rally, state (transitionKnown)
//   - retagging a task to rally, kind (UpdateTaskFields)
//   - undeleting a tombstoned rally, deleted_at (UndeleteTask)
//
// Every other write to tasks leaves all three alone, and Postgres does not
// re-check uniqueness against a row's own earlier version, so none of them
// can trip either. The index, not a prior read, is what serialises concurrent
// attempts, so this is the refusal itself rather than a backstop behind one.
// A non-matching error passes through.
func rallyUniqueConflict(err error, taskID string) error {
	switch {
	case isUniqueViolationOn(err, "tasks_one_active_rally"):
		return fmt.Errorf("task %s: its project already has an active rally: %w",
			taskID, ErrInvalidInput)
	case isUniqueViolationOn(err, "tasks_one_draft_rally"):
		return fmt.Errorf("task %s: its project already has a draft rally: %w",
			taskID, ErrInvalidInput)
	}
	return err
}

// EnsureDraftRally returns the project's draft rally, creating "Rally
// <YYYY-MM-DD>" when it has none (WL-SPEC-66 §3.5). The page never makes a
// second one, and neither does this: the read comes first, and where two
// transactions both read none, migration 0072's partial unique index is what
// decides between them — the loser gets rallyUniqueConflict's refusal out of
// CreateTask rather than a duplicate.
func EnsureDraftRally(tx *sql.Tx, now time.Time, projectID, actorID string, eventID int64) (*model.Task, error) {
	row := tx.QueryRow(
		`SELECT `+taskColumnsT+` FROM tasks t
		  WHERE t.project_id = $1 AND `+rallyDraftCondition("t"), projectID)
	t, err := scanTask(row)
	if err == nil {
		return t, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("draft rally of %s: %w", projectID, err)
	}
	return CreateTask(tx, now, TaskInput{
		ProjectID: projectID,
		Title:     "Rally " + now.UTC().Format(time.DateOnly),
		Kind:      "rally",
		Priority:  "medium",
		Draft:     true,
		CreatedBy: actorID,
	}, eventID)
}

// AddRallyMembers adds each task to the rally as a 'blocks' edge, the only
// edge a rally carries (004 §6.3), and reports how many edges were new.
// Adding is set-like (066 §3.5): a task already in the rally, or named twice
// in one call, is added once.
//
// The membership it already holds is read first rather than letting the
// task_edges unique key refuse the duplicate: AddEdge is the door that holds
// the rally invariants (no outbound blocks edge, no cycle) and logs both
// endpoints, and a unique violation raised inside this transaction would
// abort every edge added before it, not just the repeat.
func AddRallyMembers(tx *sql.Tx, now time.Time, rallyID string, taskIDs []string, eventID int64) (added int, err error) {
	if len(taskIDs) == 0 {
		return 0, nil
	}
	rows, err := tx.Query(
		`SELECT from_task FROM task_edges
		  WHERE to_task = $1 AND type = 'blocks' AND from_task = ANY($2)`,
		rallyID, taskIDs)
	if err != nil {
		return 0, fmt.Errorf("rally %s membership: %w", rallyID, err)
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan rally member: %w", err)
		}
		seen[id] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("rally %s membership: %w", rallyID, err)
	}
	rows.Close()

	for _, id := range taskIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := AddEdge(tx, now, id, rallyID, "blocks", eventID); err != nil {
			return added, err
		}
		added++
	}
	return added, nil
}

// AcceptDecisionTitle is the title of the decision task 066 §3.5 mints for a
// draft plan a rally wants accepted. The mint and the page name it the same
// way, so the guard that suppresses a second one recognises the first.
func AcceptDecisionTitle(planRef string) string { return "Accept " + planRef + "?" }

// MintAcceptDecision mints the decision task that asks whether a draft plan
// should be accepted (066 §3.5, case 3), with the one yes/no question that
// makes it answerable. The rally holds the prompt; accepting the plan stays
// the owner's act through §3.2, so answering this changes no document.
//
// §3.5 puts the prompt in the plan owner's queue, so the task is assigned to
// them — but only when they are on the project's crew, which is the whole of
// what may hold a task (029 §6.1). An owner who is not gets an unassigned
// prompt rather than a failed mint: the rally still carries the question, and
// the answer is to add them to the crew, not to refuse the rally.
//
// The caller guards with OpenTaskForDoc(planDoc, "decision") before opening
// the transaction — the same shape POST .../progress/plan uses for the
// planning task — so a second click mints nothing.
func MintAcceptDecision(tx *sql.Tx, now time.Time, planDoc int64, actorID string, eventID int64) (*model.Task, error) {
	var d model.Doc
	var projectID, owner string
	err := tx.QueryRow(
		`SELECT d.project_id, d.kind, coalesce(d.number, 0), coalesce(p.key, ''),
		        coalesce(d.owner, '')
		   FROM docs d JOIN projects p ON p.id = d.project_id
		  WHERE d.id = $1 AND d.deleted_at IS NULL`, planDoc,
	).Scan(&projectID, &d.Kind, &d.Number, &d.ProjectKey, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("doc %d: %w", planDoc, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read plan %d: %w", planDoc, err)
	}
	if d.Kind != "plan" {
		return nil, fmt.Errorf("doc %d is a %s, not a plan: %w", planDoc, d.Kind, ErrInvalidInput)
	}

	title := AcceptDecisionTitle(d.FormatRef())
	t, err := CreateTask(tx, now, TaskInput{
		ProjectID: projectID,
		Title:     title,
		Body:      "Answering this records the decision. Accepting the plan is a separate act, in its owner's hands (WL-SPEC-66 §3.5).",
		Kind:      "decision",
		Priority:  "medium",
		AboutDoc:  planDoc,
		CreatedBy: actorID,
	}, eventID)
	if err != nil {
		return nil, err
	}
	if _, err := InsertDecision(tx, t.ID, model.Decision{
		Key: "accept", Question: title, ResponseType: "yes_no",
	}); err != nil {
		return nil, err
	}
	if owner != "" {
		crew, err := isCrewMember(tx, projectID, owner)
		if err != nil {
			return nil, err
		}
		if crew {
			if err := AssignTask(tx, now, t.ID, owner, eventID); err != nil {
				return nil, err
			}
			t.Assignee = owner
		}
	}
	return t, nil
}
