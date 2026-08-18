package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// toLeaseJSON converts a store.Lease (which additionally carries the
// database primary key and released_at — see store.Lease's doc comment) to
// its wire form, model.Lease.
func toLeaseJSON(l *store.Lease) model.Lease {
	return model.Lease{
		TaskID:     l.TaskID,
		ActorID:    l.ActorID,
		Worktree:   l.Worktree,
		AcquiredAt: l.AcquiredAt,
		RenewedAt:  l.RenewedAt,
		ExpiresAt:  l.ExpiresAt,
	}
}

// SlugifyTitle re-exports store.SlugifyTitle so api callers keep a local name
// for the branch-name slug helper (see store.SlugifyTitle for the rules).
func SlugifyTitle(title string) string {
	return store.SlugifyTitle(title)
}

// readOptionalJSON is readJSON, but an empty request body leaves v at its
// zero value instead of failing — the lifecycle endpoints have all-optional
// bodies.
func readOptionalJSON(w http.ResponseWriter, r *http.Request, v any) error {
	if err := readJSON(w, r, v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// claimTask handles POST /api/v1/tasks/{id}/claim: lease the task to the
// caller and move it ready -> in_progress, bound to the caller's worktree
// identity (required). A 409 for an already-leased task carries the current
// holder.
func (s *server) claimTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.ClaimInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Worktree == "" {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	actor := actorFrom(r)

	lease, err := s.st.Claim(r.Context(), id, actor.ID, req.Worktree,
		time.Duration(req.TTLSeconds)*time.Second)
	if errors.Is(err, store.ErrLeased) {
		body := model.ClaimConflictResponse{Error: "task already leased"}
		// Best-effort holder info: if the lease vanished in the race window,
		// the conflict answer still stands, just without a holder.
		if holder, herr := s.st.ActiveLease(r.Context(), id); herr == nil {
			body.Holder = &model.ClaimHolder{
				ActorID:   holder.ActorID,
				ExpiresAt: holder.ExpiresAt,
			}
		} else if errors.Is(herr, store.ErrNotFound) {
			// The task itself has no active lease, so the conflict came from
			// the claimant already holding a lease on this worktree.
			body.Error = "you already hold an active lease on another task from this worktree"
		}
		writeJSON(w, http.StatusConflict, body)
		return
	}
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ClaimResponse{
		Lease:  toLeaseJSON(lease),
		Branch: store.BranchFor(t),
	})
}

// toTaskPickJSON builds a claim-next response task, including a lease shard
// only when lease is non-nil (dry-run and no-ready-task responses have none).
func (s *server) toTaskPickJSON(t *model.Task, fanOut int, lease *store.Lease) model.ClaimNextPick {
	pick := model.ClaimNextPick{
		ID:       t.ID,
		Slug:     SlugifyTitle(t.Title),
		Branch:   store.BranchFor(t),
		Concern:  t.Concern,
		Priority: t.Priority,
		FanOut:   fanOut,
		Project:  t.Project,
	}
	if lease != nil {
		pick.Lease = &model.ClaimNextPickLease{Worktree: lease.Worktree, ExpiresAt: lease.ExpiresAt}
	}
	return pick
}

// claimNext handles POST /api/v1/tasks/claim-next: rank the ready set (see
// store.ClaimNext) and atomically claim the top candidate, falling through to
// the next-ranked one whenever a claim loses the race. worktree is required
// unless dry_run is set. Three response shapes: no ready task (claimed=false,
// reason set), a dry-run hit (claimed=false, dry_run=true, task set, no
// lease), or a real claim (claimed=true, task set with its new lease).
func (s *server) claimNext(w http.ResponseWriter, r *http.Request) {
	var req model.ClaimNextInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Worktree == "" && !req.DryRun {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	if req.Kind != "" && !validKinds[req.Kind] {
		writeErr(w, http.StatusUnprocessableEntity, invalidKindMsg)
		return
	}
	actor := actorFrom(r)

	res, err := s.st.ClaimNext(r.Context(), store.ClaimNextOpts{
		ProjectID:   req.Project,
		Kind:        req.Kind,
		StrictFocus: req.StrictFocus,
		DryRun:      req.DryRun,
		Worktree:    req.Worktree,
		ActorID:     actor.ID,
		TTL:         time.Duration(req.TTLSeconds) * time.Second,
	})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	if !res.Claimed && res.Task == nil {
		writeJSON(w, http.StatusOK, model.ClaimNextResponse{Claimed: false, Reason: "no-ready-task"})
		return
	}
	if req.DryRun {
		pick := s.toTaskPickJSON(res.Task, res.FanOut, nil)
		writeJSON(w, http.StatusOK, model.ClaimNextResponse{Claimed: false, DryRun: true, Task: &pick})
		return
	}
	pick := s.toTaskPickJSON(res.Task, res.FanOut, res.Lease)
	writeJSON(w, http.StatusOK, model.ClaimNextResponse{Claimed: true, Task: &pick})
}

// renewLease handles POST /api/v1/tasks/{id}/renew.
func (s *server) renewLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.RenewInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	lease, err := s.st.Renew(r.Context(), id, actor.ID, time.Duration(req.TTLSeconds)*time.Second)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toLeaseJSON(lease))
}

// releaseLease handles POST /api/v1/tasks/{id}/release.
func (s *server) releaseLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := actorFrom(r)
	if err := s.st.Release(r.Context(), id, actor.ID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// finishTask is the shared body of the done and abandon handlers: one
// recorded event whose apply transitions the task and closes any active
// lease, then the updated task JSON.
func (s *server) finishTask(w http.ResponseWriter, r *http.Request, eventType string,
	transition func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error) {
	id := r.PathValue("id")

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"task": id})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, eventType, payload,
		func(tx *sql.Tx, eventID int64) error {
			now := s.st.Now()
			if err := transition(tx, now, id, eventID); err != nil {
				return err
			}
			return store.CloseActiveLease(tx, now, id)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// doneTask handles POST /api/v1/tasks/{id}/done: current state -> merged,
// closing any active lease in the same transaction. The from-state is read
// inside the transaction so a concurrent change cannot be raced, exactly as
// abandonTask does.
//
// It does not hardcode in_review as the from-state. legalTransitions already
// allows the direct-to-main jumps ready|in_progress -> merged, and pinning
// the endpoint to in_review made it stricter than the domain model: not every
// task earns a review, and some carry no code at all — an admin UI change or
// a CMS edit has no webhook to report anything, so a manual done is the right
// and only close for those. legalTransitions is the gate that remains: draft
// and the terminal states still 422.
func (s *server) doneTask(w http.ResponseWriter, r *http.Request) {
	s.finishTask(w, r, "task.done",
		func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
			cur, err := store.TaskState(tx, taskID)
			if err != nil {
				return err
			}
			return store.Transition(tx, now, taskID, cur, "merged", eventID)
		})
}

// abandonTask handles POST /api/v1/tasks/{id}/abandon: current state ->
// abandoned (422 from terminal states), closing any active lease in the same
// transaction. The from-state is read inside the transaction so a concurrent
// change cannot be raced.
func (s *server) abandonTask(w http.ResponseWriter, r *http.Request) {
	s.finishTask(w, r, "task.abandoned",
		func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
			cur, err := store.TaskState(tx, taskID)
			if err != nil {
				return err
			}
			return store.Transition(tx, now, taskID, cur, "abandoned", eventID)
		})
}

// reopenTask handles POST /api/v1/tasks/{id}/reopen: any delivered state
// (merged, deployed_dev, deployed_prod, released) or abandoned -> ready
// (422 from any other state), closing any active lease in the same
// transaction as a belt-and-suspenders measure (such tasks should not hold
// one). The from-state is read inside the transaction so a concurrent
// change cannot be raced. Reopen always lands on ready, never
// in_progress, so re-entry always goes through a fresh claim; "ready" is
// deliberately not read off legalTransitions here, since ready is also the
// target of unrelated transitions (draft's publish, in_progress's
// release/expiry) that must not be reachable through this endpoint.
//
// Reopening also clears the task's commit attribution, in the same
// transaction: the prior delivery no longer counts, and leaving it in place
// would let the next webhook resolve the task straight back to its former
// delivered state (see store.ClearTaskCommits).
func (s *server) reopenTask(w http.ResponseWriter, r *http.Request) {
	s.finishTask(w, r, "task.reopened",
		func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
			cur, err := store.TaskState(tx, taskID)
			if err != nil {
				return err
			}
			reopenable := map[string]bool{"merged": true, "deployed_dev": true,
				"deployed_prod": true, "released": true, "abandoned": true}
			if !reopenable[cur] {
				return fmt.Errorf("task %s is in state %s, not reopenable: %w",
					taskID, cur, store.ErrBadTransition)
			}
			if err := store.Transition(tx, now, taskID, cur, "ready", eventID); err != nil {
				return err
			}
			return store.ClearTaskCommits(tx, taskID)
		})
}
