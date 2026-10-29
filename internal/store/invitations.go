package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CrewInvitation is one invited participant before (or after) identity
// linkage (WL-REQ-1605). LinkedActor == "" means the invitation is open.
// It stays store-local until a surface sends it over HTTP; it then moves to
// internal/model (WL-RULE-1349).
type CrewInvitation struct {
	ID          int64
	ProjectID   string
	DisplayName string
	Email       string
	Role        string
	InvitedAt   time.Time
	InvitedBy   string
	LinkedActor string
	LinkedAt    time.Time
}

// InviteParticipant records one invitation (event "crew.invited"). The
// project must exist (ErrNotFound); displayName must be non-blank and role
// valid (ErrInvalidInput). An empty email stores NULL. A second open
// invitation for the same email on the project is ErrInvalidInput.
func InviteParticipant(tx *sql.Tx, now time.Time, projectID, displayName, email, role, invitedBy string, eventID int64) (int64, error) {
	displayName = strings.TrimSpace(displayName)
	email = strings.TrimSpace(email)
	role = strings.TrimSpace(role)
	if displayName == "" {
		return 0, fmt.Errorf("display name is required: %w", ErrInvalidInput)
	}
	if !validParticipantRoles[role] {
		return 0, fmt.Errorf("unknown role %q; valid roles: %s: %w",
			role, strings.Join(ParticipantRoles(), ", "), ErrInvalidInput)
	}
	var one int
	err := tx.QueryRow(`SELECT 1 FROM projects WHERE id = $1`, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("check project %s: %w", projectID, err)
	}
	var by sql.NullString
	if invitedBy != "" {
		if err := requireActor(tx, invitedBy); err != nil {
			return 0, err
		}
		by = sql.NullString{String: invitedBy, Valid: true}
	}
	var id int64
	err = tx.QueryRow(
		`INSERT INTO crew_invitations (project_id, display_name, email, role, invited_at, invited_by)
		 VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6) RETURNING id`,
		projectID, displayName, email, role, now, by).Scan(&id)
	if err != nil {
		if isUniqueViolationOn(err, "crew_invitations_open_email") {
			return 0, fmt.Errorf("project %s already has an open invitation for %s: %w", projectID, email, ErrInvalidInput)
		}
		return 0, fmt.Errorf("invite %s to project %s: %w", displayName, projectID, err)
	}
	return id, LogChange(tx, "project", projectID, eventID, map[string]any{
		"field": "crew", "op": "invite", "invitation": id,
		"display_name": displayName, "email": email, "role": role, "by": invitedBy,
	})
}

// getOpenInvitation reads an invitation for update. ErrNotFound if absent,
// ErrInvalidInput if already linked.
func getOpenInvitation(tx *sql.Tx, id int64) (projectID, role string, invitedAt time.Time, err error) {
	var linked sql.NullString
	err = tx.QueryRow(
		`SELECT project_id, role, invited_at, linked_actor FROM crew_invitations WHERE id = $1 FOR UPDATE`, id,
	).Scan(&projectID, &role, &invitedAt, &linked)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, fmt.Errorf("invitation %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", "", time.Time{}, fmt.Errorf("read invitation %d: %w", id, err)
	}
	if linked.Valid {
		return "", "", time.Time{}, fmt.Errorf("invitation %d is already linked to %s: %w", id, linked.String, ErrInvalidInput)
	}
	return projectID, role, invitedAt, nil
}

// LinkInvitation attaches an invitation to an authenticated identity (event
// "crew.invite_linked"). The actor joins the Crew with the invitation's role
// through AddParticipant, so validation, the agent refusal and the
// duplicate-role refusal are shared. The invitation row survives with
// linked_actor and linked_at set (WL-REQ-1605).
func LinkInvitation(tx *sql.Tx, now time.Time, invitationID int64, actorID, by string, eventID int64) error {
	projectID, role, invitedAt, err := getOpenInvitation(tx, invitationID)
	if err != nil {
		return err
	}
	if err := AddParticipant(tx, now, projectID, actorID, role, false, false, by, eventID); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE crew_invitations SET linked_actor = $2, linked_at = $3 WHERE id = $1`,
		invitationID, actorID, now); err != nil {
		return fmt.Errorf("link invitation %d: %w", invitationID, err)
	}
	return LogChange(tx, "project", projectID, eventID, map[string]any{
		"field": "crew", "op": "invite_link", "invitation": invitationID,
		"actor": actorID, "role": role, "invited_at": invitedAt, "by": by,
	})
}

// WithdrawInvitation deletes an open invitation (event
// "crew.invite_withdrawn"); the event is its history. A linked invitation is
// history itself and is refused (ErrInvalidInput).
func WithdrawInvitation(tx *sql.Tx, now time.Time, invitationID int64, by string, eventID int64) error {
	projectID, role, _, err := getOpenInvitation(tx, invitationID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM crew_invitations WHERE id = $1`, invitationID); err != nil {
		return fmt.Errorf("withdraw invitation %d: %w", invitationID, err)
	}
	return LogChange(tx, "project", projectID, eventID, map[string]any{
		"field": "crew", "op": "invite_withdraw", "invitation": invitationID, "role": role, "by": by,
	})
}

// ListInvitations returns a project's invitations, open first, then by
// invited_at. ErrNotFound if the project does not exist.
func (s *Store) ListInvitations(ctx context.Context, projectID string) ([]CrewInvitation, error) {
	if _, err := s.GetProject(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, display_name, COALESCE(email, ''), role, invited_at,
		        COALESCE(invited_by, ''), COALESCE(linked_actor, ''), linked_at
		   FROM crew_invitations
		  WHERE project_id = $1
		  ORDER BY (linked_actor IS NOT NULL), invited_at, id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list invitations for project %s: %w", projectID, err)
	}
	defer rows.Close()
	out := []CrewInvitation{}
	for rows.Next() {
		var i CrewInvitation
		var linkedAt sql.NullTime
		if err := rows.Scan(&i.ID, &i.ProjectID, &i.DisplayName, &i.Email, &i.Role, &i.InvitedAt,
			&i.InvitedBy, &i.LinkedActor, &linkedAt); err != nil {
			return nil, fmt.Errorf("list invitations for project %s: %w", projectID, err)
		}
		i.LinkedAt = linkedAt.Time
		out = append(out, i)
	}
	return out, rows.Err()
}
