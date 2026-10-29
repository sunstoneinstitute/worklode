package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// LeadHandoff is one lead-handoff ceremony (WL-REQ-1605), a
// crew_lead_handoffs row. The ceremony spans sessions and people, so each
// step is an act against this row. Zero times and empty strings mean the
// step has not happened.
type LeadHandoff struct {
	ID                  int64
	ProjectID           string
	FromActor           string
	ToActor             string
	State               string // pending | completed | cancelled
	OutgoingAcceptedAt  time.Time
	IncomingAcceptedAt  time.Time
	EditorAuthorizedBy  string
	ScienceAuthorizedBy string
	RequestedBy         string
	RequestedAt         time.Time
	ResolvedAt          time.Time
}

const leadHandoffColumns = `id, project_id, from_actor, to_actor, state,
	outgoing_accepted_at, incoming_accepted_at,
	editor_authorized_by, science_lead_authorized_by,
	requested_by, requested_at, resolved_at`

func scanLeadHandoff(row interface{ Scan(...any) error }) (*LeadHandoff, error) {
	var h LeadHandoff
	var outAt, inAt, resolved sql.NullTime
	var editor, science, by sql.NullString
	if err := row.Scan(&h.ID, &h.ProjectID, &h.FromActor, &h.ToActor, &h.State,
		&outAt, &inAt, &editor, &science, &by, &h.RequestedAt, &resolved); err != nil {
		return nil, err
	}
	h.OutgoingAcceptedAt, h.IncomingAcceptedAt, h.ResolvedAt = outAt.Time, inAt.Time, resolved.Time
	h.EditorAuthorizedBy, h.ScienceAuthorizedBy, h.RequestedBy = editor.String, science.String, by.String
	return &h, nil
}

// PendingLeadHandoff returns projectID's open ceremony, or nil when there
// is none.
func (s *Store) PendingLeadHandoff(ctx context.Context, projectID string) (*LeadHandoff, error) {
	h, err := scanLeadHandoff(s.db.QueryRowContext(ctx,
		`SELECT `+leadHandoffColumns+` FROM crew_lead_handoffs
		  WHERE project_id = $1 AND state = 'pending'`, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pending lead handoff for project %s: %w", projectID, err)
	}
	return h, nil
}

// lockPendingHandoff reads a ceremony under a row lock and refuses one that
// is no longer pending.
func lockPendingHandoff(tx *sql.Tx, handoffID int64) (*LeadHandoff, error) {
	h, err := scanLeadHandoff(tx.QueryRow(
		`SELECT `+leadHandoffColumns+` FROM crew_lead_handoffs WHERE id = $1 FOR UPDATE`, handoffID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lead handoff %d: %w", handoffID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read lead handoff %d: %w", handoffID, err)
	}
	if h.State != "pending" {
		return nil, fmt.Errorf("lead handoff %d is %s: %w", handoffID, h.State, ErrInvalidInput)
	}
	return h, nil
}

func logHandoff(tx *sql.Tx, h *LeadHandoff, eventID int64, op, by string, extra map[string]any) error {
	change := map[string]any{
		"field": "lead_handoff", "op": op, "handoff": h.ID,
		"from": h.FromActor, "to": h.ToActor, "by": by,
	}
	for k, v := range extra {
		change[k] = v
	}
	return LogChange(tx, "project", h.ProjectID, eventID, change)
}

// RequestLeadHandoff opens a ceremony moving projectID's lead to toActor
// (WL-REQ-1605). The project must have a lead (a leadless project sets its
// first lead through AddParticipant), toActor must be a human Crew member
// other than the lead, and only one ceremony per project may be pending.
// An empty by stores NULL, the AddParticipant convention.
func RequestLeadHandoff(tx *sql.Tx, now time.Time, projectID, toActor, by string, eventID int64) (int64, error) {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM projects WHERE id = $1`, projectID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("project %s: %w", projectID, ErrNotFound)
	}
	if err != nil {
		return 0, fmt.Errorf("check project %s: %w", projectID, err)
	}
	var from string
	err = tx.QueryRow(`SELECT actor_id FROM project_participants
	                    WHERE project_id = $1 AND is_lead FOR UPDATE`, projectID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("project %s has no lead to hand off; add one as lead instead: %w", projectID, ErrInvalidInput)
	}
	if err != nil {
		return 0, fmt.Errorf("read lead of project %s: %w", projectID, err)
	}
	if toActor == from {
		return 0, fmt.Errorf("%s is already the lead of project %s: %w", toActor, projectID, ErrInvalidInput)
	}
	var member bool
	if err := tx.QueryRow(`SELECT EXISTS (
	        SELECT 1 FROM project_participants pp JOIN actors a ON a.id = pp.actor_id
	         WHERE pp.project_id = $1 AND pp.actor_id = $2 AND a.kind = 'human')`,
		projectID, toActor).Scan(&member); err != nil {
		return 0, fmt.Errorf("check crew member %s: %w", toActor, err)
	}
	if !member {
		return 0, fmt.Errorf("%s is not a human Crew member of project %s: %w", toActor, projectID, ErrInvalidInput)
	}
	var requestedBy sql.NullString
	if by != "" {
		if err := requireActor(tx, by); err != nil {
			return 0, err
		}
		requestedBy = sql.NullString{String: by, Valid: true}
	}

	h := &LeadHandoff{ProjectID: projectID, FromActor: from, ToActor: toActor}
	if err := tx.QueryRow(
		`INSERT INTO crew_lead_handoffs (project_id, from_actor, to_actor, state, requested_by, requested_at)
		 VALUES ($1, $2, $3, 'pending', $4, $5) RETURNING id`,
		projectID, from, toActor, requestedBy, now.UTC()).Scan(&h.ID); err != nil {
		if isUniqueViolationOn(err, "crew_lead_handoffs_one_pending") {
			return 0, fmt.Errorf("project %s already has a pending lead handoff: %w", projectID, ErrInvalidInput)
		}
		return 0, fmt.Errorf("request lead handoff on project %s: %w", projectID, err)
	}
	return h.ID, logHandoff(tx, h, eventID, "request", by, nil)
}

// AcceptLeadHandoff records the outgoing or incoming lead's acceptance and
// completes the ceremony when that satisfies it.
func AcceptLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, actingActor string, eventID int64) (bool, error) {
	h, err := lockPendingHandoff(tx, handoffID)
	if err != nil {
		return false, err
	}
	var column string
	var already bool
	switch actingActor {
	case h.FromActor:
		column, already = "outgoing_accepted_at", !h.OutgoingAcceptedAt.IsZero()
		h.OutgoingAcceptedAt = now
	case h.ToActor:
		column, already = "incoming_accepted_at", !h.IncomingAcceptedAt.IsZero()
		h.IncomingAcceptedAt = now
	default:
		return false, fmt.Errorf("only %s (outgoing lead) or %s (incoming lead) may accept lead handoff %d: %w",
			h.FromActor, h.ToActor, handoffID, ErrInvalidInput)
	}
	if already {
		return false, fmt.Errorf("%s already accepted lead handoff %d: %w", actingActor, handoffID, ErrInvalidInput)
	}
	if _, err := tx.Exec(`UPDATE crew_lead_handoffs SET `+column+` = $2 WHERE id = $1`,
		handoffID, now.UTC()); err != nil {
		return false, fmt.Errorf("accept lead handoff %d: %w", handoffID, err)
	}
	return completeIfReady(tx, now, h, eventID, "accept", actingActor)
}

// AuthorizeLeadHandoff is the outgoing-lead-unavailable path: an editor
// and a science lead of the project jointly stand in for the outgoing
// lead's acceptance. The two slots must be filled by different actors, and
// the incoming lead cannot authorize their own handoff. An actor holding
// both roles fills the editor slot first.
func AuthorizeLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, actingActor string, eventID int64) (bool, error) {
	h, err := lockPendingHandoff(tx, handoffID)
	if err != nil {
		return false, err
	}
	if actingActor == h.ToActor {
		return false, fmt.Errorf("the incoming lead cannot authorize their own handoff: %w", ErrInvalidInput)
	}
	if actingActor == h.EditorAuthorizedBy || actingActor == h.ScienceAuthorizedBy {
		return false, fmt.Errorf("%s already authorized lead handoff %d; the second authorization needs a different person: %w",
			actingActor, handoffID, ErrInvalidInput)
	}
	rows, err := tx.Query(`SELECT role FROM project_participants WHERE project_id = $1 AND actor_id = $2`,
		h.ProjectID, actingActor)
	if err != nil {
		return false, fmt.Errorf("read roles of %s: %w", actingActor, err)
	}
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return false, fmt.Errorf("read roles of %s: %w", actingActor, err)
		}
		roles = append(roles, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read roles of %s: %w", actingActor, err)
	}
	editor, science := slices.Contains(roles, "editor"), slices.Contains(roles, "science-lead")

	var slot string
	switch {
	case editor && h.EditorAuthorizedBy == "":
		slot, h.EditorAuthorizedBy = "editor", actingActor
	case science && h.ScienceAuthorizedBy == "":
		slot, h.ScienceAuthorizedBy = "science_lead", actingActor
	case !editor && !science:
		return false, fmt.Errorf("only an editor or science lead of project %s may authorize a lead handoff: %w",
			h.ProjectID, ErrInvalidInput)
	default:
		return false, fmt.Errorf("lead handoff %d already has the authorization %s can give: %w",
			handoffID, actingActor, ErrInvalidInput)
	}
	if _, err := tx.Exec(`UPDATE crew_lead_handoffs SET `+slot+`_authorized_by = $2, `+slot+`_authorized_at = $3
	                       WHERE id = $1`, handoffID, actingActor, now.UTC()); err != nil {
		return false, fmt.Errorf("authorize lead handoff %d: %w", handoffID, err)
	}
	return completeIfReady(tx, now, h, eventID, "authorize", actingActor, "slot", slot)
}

// completeIfReady logs the act and, when the ceremony is satisfied
// (incoming acceptance plus the outgoing acceptance or both
// authorizations), moves the lead flag and closes the ceremony in the same
// transaction.
func completeIfReady(tx *sql.Tx, now time.Time, h *LeadHandoff, eventID int64, op, by string, kv ...string) (bool, error) {
	extra := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		extra[kv[i]] = kv[i+1]
	}
	ready := !h.IncomingAcceptedAt.IsZero() &&
		(!h.OutgoingAcceptedAt.IsZero() || (h.EditorAuthorizedBy != "" && h.ScienceAuthorizedBy != ""))
	extra["completed"] = ready
	if !ready {
		return false, logHandoff(tx, h, eventID, op, by, extra)
	}

	// Lock both parties' Crew rows, then find the incoming lead's earliest.
	var firstRole string
	rows, err := tx.Query(`SELECT actor_id, role FROM project_participants
	                        WHERE project_id = $1 AND actor_id IN ($2, $3)
	                        ORDER BY added_at, role FOR UPDATE`, h.ProjectID, h.FromActor, h.ToActor)
	if err != nil {
		return false, fmt.Errorf("lock crew rows for lead handoff %d: %w", h.ID, err)
	}
	for rows.Next() {
		var actor, role string
		if err := rows.Scan(&actor, &role); err != nil {
			rows.Close()
			return false, fmt.Errorf("lock crew rows for lead handoff %d: %w", h.ID, err)
		}
		if actor == h.ToActor && firstRole == "" {
			firstRole = role
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("lock crew rows for lead handoff %d: %w", h.ID, err)
	}
	if firstRole == "" {
		return false, fmt.Errorf("%s is no longer on project %s's crew; cancel lead handoff %d: %w",
			h.ToActor, h.ProjectID, h.ID, ErrInvalidInput)
	}

	// Clear the old lead first (project_participants_one_lead). A deputy
	// becoming lead drops the deputy flag
	// (project_participants_one_flag_per_actor).
	if _, err := tx.Exec(`UPDATE project_participants SET is_lead = false
	                       WHERE project_id = $1 AND is_lead`, h.ProjectID); err != nil {
		return false, fmt.Errorf("clear lead of project %s: %w", h.ProjectID, err)
	}
	if _, err := tx.Exec(`UPDATE project_participants SET is_deputy = false
	                       WHERE project_id = $1 AND actor_id = $2`, h.ProjectID, h.ToActor); err != nil {
		return false, fmt.Errorf("clear deputy flag of %s: %w", h.ToActor, err)
	}
	if _, err := tx.Exec(`UPDATE project_participants SET is_lead = true
	                       WHERE project_id = $1 AND actor_id = $2 AND role = $3`,
		h.ProjectID, h.ToActor, firstRole); err != nil {
		return false, fmt.Errorf("set lead of project %s: %w", h.ProjectID, err)
	}
	if _, err := tx.Exec(`UPDATE crew_lead_handoffs SET state = 'completed', resolved_at = $2 WHERE id = $1`,
		h.ID, now.UTC()); err != nil {
		return false, fmt.Errorf("complete lead handoff %d: %w", h.ID, err)
	}
	return true, logHandoff(tx, h, eventID, op, by, extra)
}

// CancelLeadHandoff closes a pending ceremony without moving the lead. Only
// the outgoing lead, the incoming lead, or the requester may cancel.
func CancelLeadHandoff(tx *sql.Tx, now time.Time, handoffID int64, by string, eventID int64) error {
	h, err := lockPendingHandoff(tx, handoffID)
	if err != nil {
		return err
	}
	if by != h.FromActor && by != h.ToActor && (by == "" || by != h.RequestedBy) {
		return fmt.Errorf("only %s, %s or the requester may cancel lead handoff %d: %w",
			h.FromActor, h.ToActor, handoffID, ErrInvalidInput)
	}
	return cancelLeadHandoff(tx, now, h, by, eventID)
}

// cancelLeadHandoff is the cancel core without the party check, for project
// close, which cancels whatever ceremony is open.
func cancelLeadHandoff(tx *sql.Tx, now time.Time, h *LeadHandoff, by string, eventID int64) error {
	if _, err := tx.Exec(`UPDATE crew_lead_handoffs SET state = 'cancelled', resolved_at = $2 WHERE id = $1`,
		h.ID, now.UTC()); err != nil {
		return fmt.Errorf("cancel lead handoff %d: %w", h.ID, err)
	}
	return logHandoff(tx, h, eventID, "cancel", by, nil)
}

// CloseCrew is the crew half of closing a project (WL-REQ-1605): it
// cancels any pending lead-handoff ceremony and records the crew as closed
// in the change log. It deletes nothing: the participant rows, linked
// invitations, and events are the preserved roster, role history, and
// derived contributions. The caller is the project-close transaction
// (WL-PLAN-101), which also owns refusing new crew mutations on a closed
// project; this function is written to be callable from that transaction
// and idempotent when nothing is pending. The refusal needs WL-PLAN-101's
// project-state fact and gates before the crew functions are called, so
// they need no change for it. The close row is written once per project;
// a repeat call finds it and writes nothing.
func CloseCrew(tx *sql.Tx, now time.Time, projectID, by string, eventID int64) error {
	h, err := scanLeadHandoff(tx.QueryRow(
		`SELECT `+leadHandoffColumns+` FROM crew_lead_handoffs
		  WHERE project_id = $1 AND state = 'pending' FOR UPDATE`, projectID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return fmt.Errorf("read pending lead handoff of project %s: %w", projectID, err)
	default:
		if err := cancelLeadHandoff(tx, now, h, by, eventID); err != nil {
			return err
		}
	}
	var closed bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM state_log
	        WHERE entity_kind = 'project' AND entity_id = $1
	          AND change @> '{"field":"crew","op":"close"}')`, projectID).Scan(&closed); err != nil {
		return fmt.Errorf("check crew close of project %s: %w", projectID, err)
	}
	if closed {
		return nil
	}
	return LogChange(tx, "project", projectID, eventID, map[string]any{"field": "crew", "op": "close", "by": by})
}
