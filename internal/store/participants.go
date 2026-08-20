package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
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

// OwnedWork is one open item a Crew member owns, blocking their removal
// (spec 029 §6.1). Kind is "task" today; approvals (spec 029 §7, plan C)
// and decisions join this query when their tables exist.
type OwnedWork struct {
	Kind  string // "task"
	ID    string
	Title string
	State string
}

// rowQueryer is the multi-row read surface OpenWorkOwnedBy needs: both
// *sql.DB and *sql.Tx satisfy it, so the removal guard (task 7) can run the
// same query inside the transaction that reassigns or unassigns the work
// it finds.
type rowQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// openWorkExcludedStates is the SQL "NOT IN (...)" literal list of every
// state in deliveredStateSet (internal/store/tasks.go), sorted. Building it
// from that set — rather than hand-listing states here — means a state
// added to or removed from deliveredStateSet changes what "open" means for
// the removal guard automatically; the two can never drift apart.
var openWorkExcludedStates = func() string {
	states := slices.Sorted(maps.Keys(deliveredStateSet))
	quoted := make([]string, len(states))
	for i, st := range states {
		quoted[i] = "'" + st + "'"
	}
	return strings.Join(quoted, ", ")
}()

// openWorkOwnedBy runs the OpenWorkOwnedBy query against any rowQueryer, so
// Task 7's removal guard can call it inside the same transaction that
// reassigns or unassigns the work it finds. "Open" means the task's state
// is not in deliveredStateSet (see openWorkExcludedStates).
func openWorkOwnedBy(ctx context.Context, q rowQueryer, projectID, actorID string) ([]OwnedWork, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT id, title, state
		   FROM tasks
		  WHERE project_id = $1 AND assignee = $2
		    AND state NOT IN (`+openWorkExcludedStates+`)
		  ORDER BY id`,
		projectID, actorID)
	if err != nil {
		return nil, fmt.Errorf("open work owned by %s in project %s: %w", actorID, projectID, err)
	}
	defer rows.Close()

	var out []OwnedWork
	for rows.Next() {
		w := OwnedWork{Kind: "task"}
		if err := rows.Scan(&w.ID, &w.Title, &w.State); err != nil {
			return nil, fmt.Errorf("open work owned by %s in project %s: %w", actorID, projectID, err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("open work owned by %s in project %s: %w", actorID, projectID, err)
	}
	return out, nil
}

// OpenWorkOwnedBy returns every open item actorID owns in projectID — the
// removal guard's fact query (spec 029 §6.1) and the basis for a member's
// responsibility listing (032 §6). An actor who owns nothing open returns
// an empty slice, not an error.
func (s *Store) OpenWorkOwnedBy(ctx context.Context, projectID, actorID string) ([]OwnedWork, error) {
	return openWorkOwnedBy(ctx, s.db, projectID, actorID)
}

// maxParticipantRole caps a role label. The label is free-form on purpose
// (spec 029 §6.1): what a person does on a project is org vocabulary, not a
// closed enum this package gets to decide, and it is never used as a metric
// label. The cap exists only to keep a runaway paste out of the table.
const maxParticipantRole = 100

// AddParticipant adds one role-labelled Crew row inside the given ingest
// transaction (spec 029 §6.1). Callers reach it through RecordEvent with
// event type "crew.member_added" (spec 029 §8.4) — never directly, so the
// membership fact and the event that records it commit together.
//
// The project and the actor must exist (ErrNotFound); role is trimmed and
// must be non-empty and at most maxParticipantRole characters
// (ErrInvalidInput). One actor may hold several role labels, one row each,
// so only the same role twice is a conflict; a project may hold at most one
// lead. Both conflicts are ErrInvalidInput, named by which one fired. An
// empty addedBy stores NULL — an open instance has no acting actor to
// attribute the row to, and inventing one would be a fabricated fact.
func AddParticipant(tx *sql.Tx, now time.Time, projectID, actorID, role string, isLead bool, addedBy string, eventID int64) error {
	role = strings.TrimSpace(role)
	switch {
	case role == "":
		return fmt.Errorf("role is required: %w", ErrInvalidInput)
	case utf8.RuneCountInString(role) > maxParticipantRole:
		return fmt.Errorf("role %q is too long (%d characters at most): %w",
			role, maxParticipantRole, ErrInvalidInput)
	}

	// Both referenced rows are checked before the insert: without this an
	// unknown project or actor surfaces as a raw foreign-key violation,
	// which poisons the caller's transaction instead of returning
	// ErrNotFound (the same reason requireActor exists for tasks.assignee).
	var one int
	err := tx.QueryRow(`SELECT 1 FROM projects WHERE id = $1`, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("project %s: %w", projectID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("check project %s: %w", projectID, err)
	}
	if err := requireActor(tx, actorID); err != nil {
		return err
	}
	var by sql.NullString
	if addedBy != "" {
		if err := requireActor(tx, addedBy); err != nil {
			return err
		}
		by = sql.NullString{String: addedBy, Valid: true}
	}

	if _, err := tx.Exec(
		`INSERT INTO project_participants (project_id, actor_id, role, is_lead, added_at, added_by)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		projectID, actorID, role, isLead, now.UTC(), by,
	); err != nil {
		if isUniqueViolationOn(err, "project_participants_pkey") {
			return fmt.Errorf("actor %s already holds role %q on project %s: %w",
				actorID, role, projectID, ErrInvalidInput)
		}
		if isUniqueViolationOn(err, "project_participants_one_lead") {
			return fmt.Errorf("project %s already has a lead: %w", projectID, ErrInvalidInput)
		}
		return fmt.Errorf("add participant %s to project %s: %w", actorID, projectID, err)
	}

	return LogChange(tx, "project", projectID, eventID, map[string]any{
		"field": "crew", "op": "add",
		"actor": actorID, "role": role, "lead": isLead, "by": addedBy,
	})
}
