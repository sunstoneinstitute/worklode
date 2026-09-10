// approvalflow.go is spec 029 §7.2's write side: the effective flow snapshot a
// project is stamped with, and the 'awaiting' rows that snapshot demands.
// Everything here is tx-scoped, so the apply and the deliverable-created path
// materialize inside the same event transaction that records what happened
// (021 §4). The read-side rules — which flow matches, which lanes an entity
// owes — are pure functions in approval_rules.go.

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// FlowActorID owns every approval row a flow rule mints (029 §7.2): the rule
// inserted them, and crediting the policy to whichever human filed the idea
// would misstate who did what.
const FlowActorID = "worklode"

// SetProjectApprovalFlow stamps the effective snapshot on the project.
// Overwrites any previous snapshot: applying is an explicit, event-logged
// act, so this is not the silent change the snapshot exists to prevent.
func SetProjectApprovalFlow(tx *sql.Tx, projectID string, snap model.ApprovalFlowSnapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal approval flow snapshot: %w", err)
	}
	res, err := tx.Exec(
		`UPDATE projects
		    SET approval_flow = $2, approval_flow_name = $3, approval_flow_rev = $4
		  WHERE id = $1`,
		projectID, raw, snap.Flow.Name, snap.Flow.Rev)
	if err != nil {
		return fmt.Errorf("stamp approval flow on project %s: %w", projectID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("stamp approval flow on project %s: %w", projectID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ProjectApprovalFlow loads the snapshot; nil when the project has none.
func ProjectApprovalFlow(tx *sql.Tx, projectID string) (*model.ApprovalFlowSnapshot, error) {
	var raw []byte
	err := tx.QueryRow(`SELECT approval_flow FROM projects WHERE id = $1`, projectID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load approval flow of project %s: %w", projectID, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var snap model.ApprovalFlowSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal approval flow of project %s: %w", projectID, err)
	}
	return &snap, nil
}

// SelfReviewPolicy reports what the approval detail page has to say about
// self-review on one approval (029 §7.1, 032 §7): the stamped flow's name and
// rev, which the page names beside a decision made under an exception, and
// whether that flow permits self-review, which decides whether the page
// offers the exception act at all.
//
// allowed is false for every project today; SelfReviewAllowed says why.
func (s *Store) SelfReviewPolicy(ctx context.Context, kind, entityID string) (
	name string, rev string, allowed bool, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", "", false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	projectID, err := projectForApproval(tx, kind, entityID)
	if err != nil || projectID == "" {
		return "", "", false, err
	}
	err = tx.QueryRow(
		`SELECT coalesce(approval_flow_name, ''), coalesce(approval_flow_rev, '')
		   FROM projects WHERE id = $1`, projectID).Scan(&name, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("approval flow stamp of project %s: %w", projectID, err)
	}
	allowed, err = SelfReviewAllowed(tx, kind, entityID)
	return name, rev, allowed, err
}

// MaterializeForEntity inserts the 'awaiting' rows the snapshot's flow
// demands of one entity, lane-keyed and idempotent. required_actor comes
// from the reviewer template (snap.Reviewers[lane]) when set, else the
// row carries the lane's required_role. created_by is the system
// 'worklode' actor: the rule inserted these rows (029 §7.2). New
// deliverable rows bind subject_revision ” — nothing designated yet.
// Returns how many rows were actually inserted.
func MaterializeForEntity(tx *sql.Tx, now time.Time,
	snap model.ApprovalFlowSnapshot, entityKind, entityID, name string) (int, error) {
	createdBy := FlowActorID
	inserted := 0
	for _, r := range RequirementsForEntity(snap.Flow, entityKind, name) {
		// A named reviewer replaces the role: the template already answers
		// "who owes this decision", so re-demanding the group would refuse a
		// reviewer the project named on purpose.
		role, actor := &r.Role, (*string)(nil)
		if a, ok := snap.Reviewers[r.Lane]; ok && a != "" {
			role, actor = nil, &a
		}
		ok, err := InsertAwaitingApproval(tx, now, entityKind, entityID, "",
			r.Lane, role, actor, &createdBy)
		if err != nil {
			return inserted, err
		}
		if ok {
			inserted++
		}
	}
	return inserted, nil
}

// MaterializeForProject runs MaterializeForEntity over the project's
// existing deliverables — the backfill an apply performs so a flow stamped
// after creation still materializes every requirement (029 §7.1: the
// requirement is a visible row, whenever it became a requirement).
func MaterializeForProject(tx *sql.Tx, now time.Time,
	projectID string, snap model.ApprovalFlowSnapshot) (int, error) {
	rows, err := tx.Query(
		`SELECT id, name FROM deliverables WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		return 0, fmt.Errorf("list deliverables of project %s: %w", projectID, err)
	}
	// Read the whole list before writing: a tx holds one connection, so an
	// INSERT issued with these rows still open would fail.
	type deliverable struct{ id, name string }
	var ds []deliverable
	for rows.Next() {
		var d deliverable
		if err := rows.Scan(&d.id, &d.name); err != nil {
			rows.Close()
			return 0, fmt.Errorf("list deliverables of project %s: %w", projectID, err)
		}
		ds = append(ds, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("list deliverables of project %s: %w", projectID, err)
	}

	total := 0
	for _, d := range ds {
		n, err := MaterializeForEntity(tx, now, snap, "deliverable", d.id, d.name)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
