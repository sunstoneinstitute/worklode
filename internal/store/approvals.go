// Spec 029 §7.1: one approvals table for every entity that needs a
// human decision. Functions here are tx-scoped so the GitHub review ingest
// and the web decide handler both call them inside a RecordEvent
// transaction (021 §4).

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Approval is one row of the approvals table.
type Approval struct {
	ID              int64
	EntityKind      string
	EntityID        string
	SubjectRevision string
	RequiredRole    *string
	RequiredActor   *string
	ResolvingActor  *string
	State           string
	CreatedAt       time.Time
	ResolvedAt      *time.Time
}

// PREntityID renders the approvals entity_id for a pull request: the ingest
// (Task 5) and the queue reader must share this one spelling.
func PREntityID(repo string, number int64) string {
	return fmt.Sprintf("%s#%d", repo, number)
}

// InsertAwaitingApproval materializes the requirement as an 'awaiting' row.
// ON CONFLICT (entity_kind, entity_id, subject_revision) DO NOTHING: a
// redelivered or reopened PR never duplicates the requirement.
func InsertAwaitingApproval(tx *sql.Tx, now time.Time,
	entityKind, entityID, subjectRevision string,
	requiredRole, requiredActor *string) error {
	_, err := tx.Exec(
		`INSERT INTO approvals
		   (entity_kind, entity_id, subject_revision, required_role,
		    required_actor, state, created_at)
		 VALUES ($1, $2, $3, $4, $5, 'awaiting', $6)
		 ON CONFLICT (entity_kind, entity_id, subject_revision) DO NOTHING`,
		entityKind, entityID, subjectRevision, requiredRole, requiredActor,
		now.UTC())
	if err != nil {
		return fmt.Errorf("insert awaiting approval %s %s@%s: %w",
			entityKind, entityID, subjectRevision, err)
	}
	return nil
}

// approvalColumns is the SELECT list scanApproval expects, in order.
const approvalColumns = `id, entity_kind, entity_id, subject_revision,
	required_role, required_actor, resolving_actor, state, created_at, resolved_at`

func scanApproval(row rowScanner) (*Approval, error) {
	var a Approval
	err := row.Scan(&a.ID, &a.EntityKind, &a.EntityID, &a.SubjectRevision,
		&a.RequiredRole, &a.RequiredActor, &a.ResolvingActor, &a.State,
		&a.CreatedAt, &a.ResolvedAt)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// OpenApprovalForEntity returns the single row for (entityKind, entityID)
// whose state is 'awaiting' or 'changes_requested' — both count as an open
// requirement; ErrNotFound otherwise.
func OpenApprovalForEntity(tx *sql.Tx, entityKind, entityID string) (*Approval, error) {
	a, err := scanApproval(tx.QueryRow(
		`SELECT `+approvalColumns+` FROM approvals
		 WHERE entity_kind = $1 AND entity_id = $2
		   AND state IN ('awaiting', 'changes_requested')`,
		entityKind, entityID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("open approval for %s %s: %w", entityKind, entityID, err)
	}
	return a, nil
}

// resolveApproval stamps state, resolving_actor, resolved_at. Shared by the
// review ingest and the web act, so the two resolution paths cannot drift.
func resolveApproval(tx *sql.Tx, id int64, state string,
	resolvingActor *string, at time.Time) error {
	_, err := tx.Exec(
		`UPDATE approvals SET state = $1, resolving_actor = $2, resolved_at = $3
		 WHERE id = $4`,
		state, resolvingActor, at.UTC(), id)
	if err != nil {
		return fmt.Errorf("resolve approval %d: %w", id, err)
	}
	return nil
}

// ReopenApproval flips changes_requested back to awaiting (029 §7.1's
// re-request edge), clearing resolving_actor and resolved_at. No-op on any
// other state, including approved.
func ReopenApproval(tx *sql.Tx, id int64) error {
	_, err := tx.Exec(
		`UPDATE approvals SET state = 'awaiting', resolving_actor = NULL,
		   resolved_at = NULL
		 WHERE id = $1 AND state = 'changes_requested'`,
		id)
	if err != nil {
		return fmt.Errorf("reopen approval %d: %w", id, err)
	}
	return nil
}

// SetRequiredActor fills required_actor when it is currently NULL (a later
// review_requested resolves a reviewer the open ingest could not).
func SetRequiredActor(tx *sql.Tx, id int64, actorID string) error {
	_, err := tx.Exec(
		`UPDATE approvals SET required_actor = $1
		 WHERE id = $2 AND required_actor IS NULL`,
		actorID, id)
	if err != nil {
		return fmt.Errorf("set required actor on approval %d: %w", id, err)
	}
	return nil
}

// ActorIDForGitHubLogin maps a GitHub login to an actor id via
// lower(expected_github_login); "" when no actor matches.
func ActorIDForGitHubLogin(tx *sql.Tx, login string) (string, error) {
	var id string
	err := tx.QueryRow(
		`SELECT id FROM actors WHERE lower(expected_github_login) = lower($1)`,
		login).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("actor for github login %s: %w", login, err)
	}
	return id, nil
}

// GetApproval loads one row by id; ErrNotFound when absent.
func (s *Store) GetApproval(ctx context.Context, id int64) (*Approval, error) {
	a, err := scanApproval(s.db.QueryRowContext(ctx,
		`SELECT `+approvalColumns+` FROM approvals WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get approval %d: %w", id, err)
	}
	return a, nil
}
