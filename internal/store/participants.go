package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// Participant is one Crew member of a project, aggregated over their
// role-labelled rows (spec 029 §6.1). One actor may hold several role
// labels, each its own project_participants row; ListParticipants folds
// those into one Participant per actor.
type Participant struct {
	ActorID     string
	DisplayName string
	Roles       []string // sorted
	IsLead      bool
	AddedAt     time.Time // earliest role row for this actor
}

// ActorProject is one project an actor participates in, with the roles they
// hold on it. Plan D's Home project list consumes this.
type ActorProject struct {
	Project Project
	Roles   []string // sorted
	IsLead  bool
}

// ListParticipants returns the Crew of a project: one Participant per actor
// holding at least one role-labelled row, ordered lead first, then by
// AddedAt (the actor's earliest role row), then actor id. Returns
// ErrNotFound if the project does not exist.
func (s *Store) ListParticipants(ctx context.Context, projectID string) ([]Participant, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT pp.actor_id, a.display_name, pp.role, pp.is_lead, pp.added_at
		   FROM project_participants pp
		   JOIN actors a ON a.id = pp.actor_id
		  WHERE pp.project_id = $1
		  ORDER BY pp.is_lead DESC, pp.added_at, pp.actor_id`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("list participants for project %s: %w", projectID, err)
	}
	defer rows.Close()

	// Aggregate per actor id, preserving the order each actor first appears
	// in (already lead-first/AddedAt/actor-id from the ORDER BY above, since
	// project_participants_one_lead guarantees at most one lead per project).
	var order []string
	byActor := map[string]*Participant{}
	for rows.Next() {
		var actorID, role string
		var displayName sql.NullString
		var isLead bool
		var addedAt time.Time
		if err := rows.Scan(&actorID, &displayName, &role, &isLead, &addedAt); err != nil {
			return nil, fmt.Errorf("list participants for project %s: %w", projectID, err)
		}
		p, ok := byActor[actorID]
		if !ok {
			p = &Participant{ActorID: actorID, DisplayName: displayName.String, AddedAt: addedAt}
			byActor[actorID] = p
			order = append(order, actorID)
		}
		p.Roles = append(p.Roles, role)
		if isLead {
			p.IsLead = true
		}
		if addedAt.Before(p.AddedAt) {
			p.AddedAt = addedAt
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list participants for project %s: %w", projectID, err)
	}

	out := make([]Participant, 0, len(order))
	for _, id := range order {
		p := byActor[id]
		slices.Sort(p.Roles)
		out = append(out, *p)
	}
	return out, nil
}

// ProjectsForActor returns one ActorProject per project the actor
// participates in, ordered by project id (deterministic; callers that want
// a different tiering re-sort). An actor on no projects returns an empty
// slice, not an error.
func (s *Store) ProjectsForActor(ctx context.Context, actorID string) ([]ActorProject, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+projectColumnsP+`, pp.role, pp.is_lead
		   FROM project_participants pp
		   JOIN projects p ON p.id = pp.project_id
		  WHERE pp.actor_id = $1
		  ORDER BY p.id`,
		actorID)
	if err != nil {
		return nil, fmt.Errorf("list projects for actor %s: %w", actorID, err)
	}
	defer rows.Close()

	var order []string
	byProject := map[string]*ActorProject{}
	for rows.Next() {
		var role string
		var isLead bool
		p, err := scanProject(appendScan{rows, []any{&role, &isLead}})
		if err != nil {
			return nil, fmt.Errorf("list projects for actor %s: %w", actorID, err)
		}
		ap, ok := byProject[p.ID]
		if !ok {
			ap = &ActorProject{Project: *p}
			byProject[p.ID] = ap
			order = append(order, p.ID)
		}
		ap.Roles = append(ap.Roles, role)
		if isLead {
			ap.IsLead = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects for actor %s: %w", actorID, err)
	}

	out := make([]ActorProject, 0, len(order))
	for _, id := range order {
		ap := byProject[id]
		slices.Sort(ap.Roles)
		out = append(out, *ap)
	}
	return out, nil
}
