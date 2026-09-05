// rally.go holds the two reads the rally kind needs: a project's active
// rally, and the membership set that steers ranking. A rally carries no work
// of its own — the 'blocks' edges pointing at it are its whole content,
// naming the tasks a human picked as the thing to finish now.
//
// A project may hold any number of DRAFT rallies and at most one ACTIVE one.
// A draft is inert: it contributes no members and changes no ranking, so
// `lode work next` behaves as if it were not there. `lode task publish`
// (draft -> ready) is what activates one.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

// rallyAlreadyActive turns the tasks_one_active_rally unique violation into a
// sentinel. Two writes can trip it: publishing a draft rally (transitionKnown)
// and retagging a task to rally (UpdateTaskFields). The index, not a prior
// read, is what serialises concurrent attempts, so this is the refusal itself
// rather than a backstop behind one. A non-matching error passes through.
func rallyAlreadyActive(err error, taskID string) error {
	if isUniqueViolationOn(err, "tasks_one_active_rally") {
		return fmt.Errorf("task %s: its project already has an active rally: %w",
			taskID, ErrInvalidInput)
	}
	return err
}
