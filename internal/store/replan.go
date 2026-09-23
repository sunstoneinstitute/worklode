// replan.go: `lode work next --replan` (S28) — hand a stale plan document
// out as claimed re-planning work. The doc-lifecycle subscriber's §8.7 rule
// already mints one "Re-plan: <title>" design task when a plan goes stale
// (docgroom.go); ReplanNext reuses that task in the ordinary case, and
// mintReplanTask below is the fallback for when no such task is open yet.
// What --replan adds is the deliberate hand-out and claim, not the minting.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// ReplanOpts names the project and, optionally, the stale plan to hand out.
type ReplanOpts struct {
	ProjectID string
	Plan      string // a plan ref or slug; empty picks the oldest stale plan
	ActorID   string
	Worktree  string
	TTL       time.Duration
}

// ReplanNext hands a stale plan out as claimed re-planning work (S28): it
// finds the plan, reuses an open design task about it or mints one, then
// claims that task for the actor. Every outcome counts under
// worklode_claims_total{op="replan"} (metrics.go), the same series
// ClaimNext feeds under op="claim_next" — the inner Claim call below counts
// under op="claim" exactly as ClaimNext's does.
//
// A named plan (o.Plan set) that does not exist or does not resolve is
// ErrNotFound, not a quiet "nothing to claim" — a typoed ref must not read
// as an empty queue. Only the no-ref, pick-the-oldest-stale-plan case turns
// "none found" into a clean not-claimed result.
func (s *Store) ReplanNext(ctx context.Context, o ReplanOpts) (*ClaimNextResult, error) {
	plan, err := s.stalePlan(ctx, o.ProjectID, o.Plan)
	if errors.Is(err, ErrNotFound) && o.Plan == "" {
		s.metrics.claim("replan", "none")
		return &ClaimNextResult{Claimed: false, Reason: "no stale plan"}, nil
	}
	if err != nil {
		s.metrics.claim("replan", replanErrorOutcome(err))
		return nil, err
	}

	// A genuine OpenTaskForDoc failure must not be read as "no open task":
	// taskID is "" on that path too, and treating it as ErrNotFound would
	// paper over a database error by minting a task that may already exist.
	taskID, err := s.OpenTaskForDoc(ctx, plan.ID, "design")
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.metrics.claim("replan", "error")
		return nil, err
	}
	outcome := "reused"
	if taskID == "" {
		var minted bool
		taskID, minted, err = s.mintReplanTask(ctx, o, plan)
		if err != nil {
			s.metrics.claim("replan", "error")
			return nil, err
		}
		if minted {
			outcome = "minted"
		}
	}

	lease, err := s.Claim(ctx, taskID, o.ActorID, o.Worktree, o.TTL)
	if err != nil {
		s.metrics.claim("replan", claimOutcome(err))
		return nil, err
	}
	s.metrics.claim("replan", outcome)
	return s.claimNextResultFor(ctx, taskID, 0, lease)
}

// replanErrorOutcome labels a stalePlan failure for worklode_claims_total:
// ErrInvalidInput (a named ref that exists but isn't a stale plan) and
// ErrNotFound-with-a-ref (a named ref that resolves to nothing) are both
// caller mistakes, not faults — "invalid" covers both, "error" is everything
// else (a genuine read failure).
func replanErrorOutcome(err error) string {
	if errors.Is(err, ErrInvalidInput) || errors.Is(err, ErrNotFound) {
		return "invalid"
	}
	return "error"
}

// stalePlanDoc is the plan facts ReplanNext needs beyond its id. Ref is the
// <KEY>-PLAN-<n> shorthand the mint body and the CLI cite.
type stalePlanDoc struct {
	ID    int64
	Ref   string
	Title string
	Slug  string
}

// stalePlan resolves the plan ReplanNext hands out: the document ref names
// when set, or the project's oldest stale plan (by updated_at) when it is
// empty. ErrNotFound means no stale plan exists (empty ref) or ref names no
// document; a ref that resolves to a document that is not a stale plan is
// ErrInvalidInput — the message names what it actually is, since that is
// what the caller has to act on.
func (s *Store) stalePlan(ctx context.Context, project, ref string) (stalePlanDoc, error) {
	var plan stalePlanDoc
	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		var id int64
		if ref == "" {
			err := tx.QueryRowContext(ctx,
				`SELECT id FROM docs
				  WHERE project_id = $1 AND kind = 'plan' AND status = 'stale' AND deleted_at IS NULL
				  ORDER BY updated_at, id LIMIT 1`, project,
			).Scan(&id)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			if err != nil {
				return fmt.Errorf("oldest stale plan of %s: %w", project, err)
			}
		} else {
			resolved, ok, err := resolveDocRef(tx, project, ref)
			if err != nil {
				return fmt.Errorf("resolve plan ref %q: %w", ref, err)
			}
			if !ok {
				return ErrNotFound
			}
			id = resolved
		}

		var kind, status string
		var number int
		var key string
		err := tx.QueryRowContext(ctx,
			`SELECT d.kind, d.status, d.title, d.slug, coalesce(d.number, 0), coalesce(p.key, '')
			   FROM docs d JOIN projects p ON p.id = d.project_id
			  WHERE d.id = $1 AND d.deleted_at IS NULL`, id,
		).Scan(&kind, &status, &plan.Title, &plan.Slug, &number, &key)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read plan %d: %w", id, err)
		}
		plan.ID = id
		plan.Ref = model.Doc{Kind: kind, Number: number, ProjectKey: key}.FormatRef()
		if kind != "plan" {
			return fmt.Errorf("doc %s has kind %s; only a plan can be re-planned: %w", plan.Ref, kind, ErrInvalidInput)
		}
		if status != "stale" {
			return fmt.Errorf("plan %s has status %s; only a stale plan can be re-planned: %w", plan.Ref, status, ErrInvalidInput)
		}
		return nil
	})
	if txErr != nil {
		return stalePlanDoc{}, txErr
	}
	return plan, nil
}

// mintReplanTask returns the id of the design task that asks for the plan
// to be re-planned, minting one only when the plan still has no open design
// task once its document row is locked. minted reports which happened, for
// ReplanNext's metrics label.
//
// The plan's document row is locked FOR UPDATE (lockDoc) for the guard
// check and the mint, the same idiom AcceptDoc uses to serialize concurrent
// writers of one document: two callers racing to re-plan the same stale
// plan take the lock one after the other, and the loser's re-check (inside
// the lock, so it sees the winner's committed row) finds the task the
// winner just minted and reuses it instead of minting a rival.
//
// The task.created event is written only on the minting branch — recorded
// after the re-check, not before it, so a loser that ends up reusing an
// existing task never commits an event describing a creation that did not
// happen. That means this cannot use RecordEvent (which inserts its event
// row up front, before apply runs); the event insert is written by hand
// here instead, the same way docgroom.go's MarkPlansStale does inside a
// transaction it does not unconditionally want a row from.
func (s *Store) mintReplanTask(ctx context.Context, o ReplanOpts, plan stalePlanDoc) (taskID string, minted bool, err error) {
	body := fmt.Sprintf(
		"Plan %s (%s) is stale. Read it with `lode show %s --inline`, decide which "+
			"declarations stand, edit the plan with `lode doc edit`, and re-accept it.",
		plan.Ref, plan.Slug, plan.Ref)

	txErr := s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := lockDoc(tx, plan.ID); err != nil {
			return err
		}
		open, err := openTaskForDoc(ctx, tx, plan.ID, "design")
		if err != nil {
			return err
		}
		if open != "" {
			taskID = open
			return nil
		}

		extID, err := randomExternalID()
		if err != nil {
			return err
		}
		payload, err := EventPayload(map[string]any{"plan": plan.ID, "actor": o.ActorID})
		if err != nil {
			return err
		}
		var eventID int64
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO events (source, external_id, type, payload, received_at)
			 VALUES ('cli', $1, 'task.created', $2, $3) RETURNING id`,
			extID, payload, s.nowFn().UTC(),
		).Scan(&eventID); err != nil {
			return fmt.Errorf("record replan mint event for plan %d: %w", plan.ID, err)
		}

		t, err := CreateTask(tx, s.nowFn(), TaskInput{
			ProjectID: o.ProjectID,
			Title:     "Re-plan: " + plan.Title,
			Body:      body,
			Kind:      "design",
			Priority:  "medium",
			CreatedBy: o.ActorID,
			AboutDoc:  plan.ID,
		}, eventID)
		if err != nil {
			return err
		}
		taskID, minted = t.ID, true
		return nil
	})
	if txErr != nil {
		return "", false, txErr
	}
	return taskID, minted, nil
}
