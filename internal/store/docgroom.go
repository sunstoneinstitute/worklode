package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/staleness"
)

// defaultDocStalenessDays is the instance default for the §8.7 clock (see
// Store.docStalenessDays), overridden per project by
// projects.doc_staleness_days and per instance by LODE_DOC_STALENESS_DAYS
// (WithDocStalenessDays).
const defaultDocStalenessDays = 30

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

// StaleCandidate is one accepted spec or plan with the §8.7 clock facts.
type StaleCandidate struct {
	DocID        int64
	Slug         string
	Version      int
	Kind         string
	Project      string
	UpdatedAt    time.Time
	ProjectDays  int // projects.doc_staleness_days, 0 when NULL
	HasExecution bool
}

// StaleCandidateDocs returns every accepted spec and plan with its §8.7
// clock facts, ordered by doc id so the sweeper and its tests see a stable
// scan order. It fetches facts only; the threshold verdict is
// staleness.At's, the rule's one owner (internal/watcher.StaleAt is the same
// calculation, re-exported for its own callers).
//
// A plan counts as executed when any task minted from it ever held a lease —
// active or expired, since the fact that matters is that execution happened
// at all, not whether it is still in progress. A spec counts as executed
// when an accepted plan covers one of its sections; a plan's `covers` edges
// point at a spec's section (to_anchor set), but the EXISTS below only needs
// the edge and the covering plan's status, not the anchor.
//
// ADRs are excluded here, matching staleness.At. The LEFT JOIN to
// projects keeps a doc whose project row is somehow missing in the result
// with ProjectDays 0, rather than dropping it.
func (s *Store) StaleCandidateDocs(ctx context.Context) ([]StaleCandidate, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT d.id, d.slug, d.version, d.kind, d.project_id, d.updated_at,
		        coalesce(proj.doc_staleness_days, 0),
		        CASE d.kind
		          WHEN 'plan' THEN EXISTS (
		            SELECT 1 FROM leases l
		              JOIN tasks t ON t.id = l.task_id
		             WHERE t.plan_doc = d.id)
		          ELSE EXISTS (
		            SELECT 1 FROM doc_edges de
		              JOIN docs p ON p.id = de.from_doc
		             WHERE de.type = 'covers' AND de.to_doc = d.id
		               AND p.status = 'accepted')
		        END
		   FROM docs d
		   LEFT JOIN projects proj ON proj.id = d.project_id
		  WHERE d.status = 'accepted' AND d.kind IN ('spec', 'plan')
		    AND d.deleted_at IS NULL
		  ORDER BY d.id`)
	if err != nil {
		return nil, fmt.Errorf("list stale candidate docs: %w", err)
	}
	return collectRows(rows, "list stale candidate docs", func(r rowScanner) (StaleCandidate, error) {
		var c StaleCandidate
		err := r.Scan(&c.DocID, &c.Slug, &c.Version, &c.Kind, &c.Project, &c.UpdatedAt,
			&c.ProjectDays, &c.HasExecution)
		return c, err
	})
}

// sweepStaleDocs emits doc.stale (cause "clock") for every accepted spec or
// plan past its staleness threshold with no execution (025 §8.7), on the
// lease sweeper's tick (sweepLeases in sweeper.go). It reads
// StaleCandidateDocs, applies staleness.At (the same calculation
// internal/watcher.StaleAt uses — see internal/staleness's doc comment for
// why the sweeper imports it directly rather than through watcher) with
// s.docStalenessDays as the instance default, and records one event per
// crossing.
//
// The events log's (source, external_id) key — doc.stale:<slug>:<version> —
// makes each firing once-per-version: a revision bumps the version and
// re-arms, an unchanged doc collides on RecordEvent and is skipped, so
// emitted only counts events actually inserted.
//
// A clock-fired document's status is untouched: §8.7 changes how a document
// is served, not its status. Only the §8.6 amendment path (MarkPlansStale)
// flips a status, and only on plans — this runs on both specs and plans.
// Unlike MarkPlansStale, this is not run inside a caller's transaction: each
// candidate's event is its own RecordEvent call against s.db.
func (s *Store) sweepStaleDocs(ctx context.Context) (emitted int, err error) {
	defer func() { s.metrics.emitStaleDocs(emitted) }()
	cands, err := s.StaleCandidateDocs(ctx)
	if err != nil {
		return 0, fmt.Errorf("sweep stale docs: %w", err)
	}
	now := s.nowFn().UTC()
	for _, c := range cands {
		at := staleness.At(staleness.Input{
			DocKind:      c.Kind,
			Status:       "accepted", // StaleCandidateDocs already filters to accepted
			UpdatedAt:    c.UpdatedAt,
			ProjectDays:  c.ProjectDays,
			DefaultDays:  s.docStalenessDays,
			HasExecution: c.HasExecution,
		})
		if at.IsZero() || now.Before(at) {
			continue
		}
		payload, err := EventPayload(map[string]any{"doc": c.DocID, "cause": "clock"})
		if err != nil {
			return emitted, fmt.Errorf("sweep stale docs: marshal payload for doc %d: %w", c.DocID, err)
		}
		_, inserted, err := s.RecordEvent(ctx, staleEventSource,
			StaleExternalID(c.Slug, c.Version), "doc.stale", payload, nil)
		if err != nil {
			return emitted, fmt.Errorf("sweep stale docs: record doc.stale for doc %d: %w", c.DocID, err)
		}
		if inserted {
			emitted++
		}
	}
	return emitted, nil
}
