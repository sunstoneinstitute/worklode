package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// staleEventSource is events.source of every doc.stale event, whichever path
// wrote it: the §8.6 amendment path below and the §8.7 idle sweeper share the
// (source, external_id) key, so a plan already marked by one is a no-op for
// the other.
const staleEventSource = "system"

// StaleExternalID is that key: the plan and the version of it that went
// stale. A re-planning edit bumps the plan's version (UpdateDocBody), so the
// next amendment can mark it stale again; a second amendment landing on the
// same unchanged plan cannot.
func StaleExternalID(planSlug string, version int) string {
	return "doc.stale:" + planSlug + ":" + strconv.Itoa(version)
}

// MarkPlansStale flips accepted plans to stale (025 §8.6) and records one
// doc.stale event per plan, in the caller's transaction. These are the plans
// PatchDoc reported in DocPatchResult.UnexecutedCoveringPlans: they cover a
// section the amendment moved, and nobody has claimed work from them, so
// §8.2 let the amendment through and this is what it owes them.
//
// specSlug and anchors say what happened to them, in the payload's
// {"cause":"amended","spec":...,"anchors":[...]}; eventID is the patch event
// this is a consequence of.
//
// Nothing is minted here. The doc.stale event flows to the doc-lifecycle
// subscriber, whose §8.7 rule mints the one "Re-plan: <title>" task behind
// its own open-design-task guard — one mint path for both causes.
//
// The count returned is the plans actually flipped: a plan already stale by
// either path collides on the event key and is skipped, so it is not counted
// twice on the metric.
func MarkPlansStale(tx *sql.Tx, now time.Time, planIDs []int64, specSlug string, anchors []string, eventID int64) (int, error) {
	if len(anchors) == 0 {
		anchors = []string{}
	}
	marked := 0
	for _, id := range planIDs {
		d, err := lockDoc(tx, id)
		if err != nil {
			return marked, err
		}
		// Between PatchDoc's read and this write the plan is locked by the
		// same transaction, so only a plan that was already stale or moved on
		// some other way lands here — nothing to mark either way.
		if d.status != "accepted" {
			continue
		}
		payload, err := EventPayload(map[string]any{
			"doc":                id,
			"cause":              "amended",
			"spec":               specSlug,
			"anchors":            anchors,
			"prov:wasInformedBy": "wlid:event/" + strconv.FormatInt(eventID, 10),
		})
		if err != nil {
			return marked, err
		}
		var staleEvent int64
		err = tx.QueryRow(
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ($1, $2, 'doc.stale', $3, $4)
			 ON CONFLICT (source, external_id) DO NOTHING
			 RETURNING id`,
			staleEventSource, StaleExternalID(d.slug, d.version), payload, now.UTC(),
		).Scan(&staleEvent)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return marked, fmt.Errorf("record doc.stale for plan %d: %w", id, err)
		}
		if _, err := tx.Exec(
			`UPDATE docs SET status = 'stale', updated_at = $2 WHERE id = $1`,
			id, now.UTC().Truncate(time.Second)); err != nil {
			return marked, fmt.Errorf("mark plan %d stale: %w", id, err)
		}
		if err := logDocChange(tx, id, staleEvent,
			map[string]string{"field": "status", "old": d.status, "new": "stale"}); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// RecordPlansStale counts n plans marked stale on
// worklode_doc_operations_total{op="stale"}. MarkPlansStale runs inside a
// caller's transaction with no *Store to record through, so the caller — the
// API's patch handler — calls this once that transaction has committed, the
// way RecordPlanTasksMinted is called after an accept.
func (s *Store) RecordPlansStale(n int) {
	for range n {
		s.metrics.docOp("stale", nil)
	}
}

// StalePlanSlug is the slug of plan document planDoc when it is stale (025
// §8.6), and "" when it is not, when the document is gone, or when planDoc is
// 0. It is the claim-time flag: a task minted from a plan whose spec has been
// amended since carries text that may predate the amendment, which the claim
// response and the brief say and neither refuses.
func (s *Store) StalePlanSlug(ctx context.Context, planDoc int64) (string, error) {
	if planDoc == 0 {
		return "", nil
	}
	var slug string
	err := s.db.QueryRowContext(ctx,
		`SELECT slug FROM docs WHERE id = $1 AND status = 'stale' AND deleted_at IS NULL`,
		planDoc).Scan(&slug)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read stale plan %d: %w", planDoc, err)
	}
	return slug, nil
}
