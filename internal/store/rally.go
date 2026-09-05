// rally.go holds the two reads the rally kind needs: a project's open rally,
// and the membership set that steers ranking. A rally carries no work of its
// own — the 'blocks' edges pointing at it are its whole content, naming the
// tasks a human picked as the thing to finish now.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// rallyOpenCondition renders "this row is an open rally" for the tasks row
// aliased as alias. It is migration 0069's partial unique index predicate,
// and has to stay identical to it: that index is what makes "the" open rally
// singular per project, so a query asking a different question could report
// two. openWorkExcludedStates renders deliveredStateSet, which is the state
// list the index spells out.
//
// taskClosed is deliberately not the predicate here. taskClosed is per repo —
// a merged task stays open where done_state is 'released' — so it would
// disagree with the index in exactly the corner the index exists to protect.
func rallyOpenCondition(alias string) string {
	return alias + `.kind = 'rally' AND ` + alias + `.deleted_at IS NULL
	    AND ` + alias + `.state NOT IN (` + openWorkExcludedStates + `)`
}

// OpenRally returns projectID's open rally, or ErrNotFound when it has none.
func (s *Store) OpenRally(ctx context.Context, projectID string) (*model.Task, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+taskColumnsT+` FROM tasks t
		  WHERE t.project_id = $1 AND `+rallyOpenCondition("t"), projectID)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		s.metrics.rallyRead("none")
		return nil, ErrNotFound
	}
	if err != nil {
		s.metrics.rallyRead("error")
		return nil, fmt.Errorf("open rally of %s: %w", projectID, err)
	}
	s.metrics.rallyRead("ok")
	return t, nil
}

// rallyMembers returns the transitive open-blocker closure of every open
// rally, every project in one query: the set of task ids some rally is
// waiting on, directly or through another blocker. rankedFrontier calls it on
// every ranking pass, so it takes no project filter — the ready set it feeds
// may span projects.
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
      JOIN tasks r ON r.id = b.blocked AND (`+rallyOpenCondition("r")+`)
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
