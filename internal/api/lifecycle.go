package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// leaseJSON is the wire form of a lease.
type leaseJSON struct {
	TaskID     string    `json:"task_id"`
	ActorID    string    `json:"actor_id"`
	Worktree   string    `json:"worktree"`
	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func toLeaseJSON(l *store.Lease) leaseJSON {
	return leaseJSON{
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

type claimRequest struct {
	Worktree   string `json:"worktree"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// claimTask handles POST /api/v1/tasks/{id}/claim: lease the task to the
// caller and move it ready -> in_progress, bound to the caller's worktree
// identity (required). A 409 for an already-leased task carries the current
// holder.
func (s *server) claimTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req claimRequest
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
		body := map[string]any{"error": "task already leased"}
		// Best-effort holder info: if the lease vanished in the race window,
		// the conflict answer still stands, just without a holder.
		if holder, herr := s.st.ActiveLease(r.Context(), id); herr == nil {
			body["holder"] = map[string]any{
				"actor_id":   holder.ActorID,
				"expires_at": holder.ExpiresAt,
			}
		} else if errors.Is(herr, store.ErrNotFound) {
			// The task itself has no active lease, so the conflict came from
			// the claimant's worktree already holding a lease elsewhere.
			body["error"] = "worktree already holds an active lease on another task"
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
	writeJSON(w, http.StatusOK, map[string]any{
		"lease":  toLeaseJSON(lease),
		"branch": s.branchPrefix + id + "-" + SlugifyTitle(t.Title),
	})
}

// leasePickJSON is the lease shard of a claim-next task pick.
type leasePickJSON struct {
	Worktree  string    `json:"worktree"`
	ExpiresAt time.Time `json:"expires_at"`
}

// taskPickJSON is the wire form of a claim-next candidate/claimed task: a
// slimmer projection than taskJSON, matching the ranking-relevant fields
// (spec 02) rather than the full task record.
type taskPickJSON struct {
	ID       string         `json:"id"`
	Slug     string         `json:"slug"`
	Branch   string         `json:"branch"`
	Concern  string         `json:"concern"`
	Priority string         `json:"priority"`
	FanOut   int            `json:"fan_out"`
	Project  string         `json:"project"`
	Lease    *leasePickJSON `json:"lease,omitempty"`
}

// toTaskPickJSON builds a claim-next response task, including a lease shard
// only when lease is non-nil (dry-run and no-ready-task responses have none).
func (s *server) toTaskPickJSON(t *store.Task, fanOut int, lease *store.Lease) taskPickJSON {
	pick := taskPickJSON{
		ID:       t.ID,
		Slug:     SlugifyTitle(t.Title),
		Branch:   s.branchPrefix + t.ID + "-" + SlugifyTitle(t.Title),
		Concern:  t.Concern,
		Priority: t.Priority,
		FanOut:   fanOut,
		Project:  t.ProjectID,
	}
	if lease != nil {
		pick.Lease = &leasePickJSON{Worktree: lease.Worktree, ExpiresAt: lease.ExpiresAt}
	}
	return pick
}

type claimNextRequest struct {
	Project     string `json:"project"`
	StrictFocus bool   `json:"strict_focus"`
	DryRun      bool   `json:"dry_run"`
	Worktree    string `json:"worktree"`
	TTLSeconds  int    `json:"ttl_seconds"`
}

// claimNext handles POST /api/v1/tasks/claim-next: rank the ready set (see
// store.ClaimNext) and atomically claim the top candidate, falling through to
// the next-ranked one whenever a claim loses the race. worktree is required
// unless dry_run is set. Three response shapes: no ready task (claimed=false,
// reason set), a dry-run hit (claimed=false, dry_run=true, task set, no
// lease), or a real claim (claimed=true, task set with its new lease).
func (s *server) claimNext(w http.ResponseWriter, r *http.Request) {
	var req claimNextRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Worktree == "" && !req.DryRun {
		writeErr(w, http.StatusBadRequest, "worktree is required")
		return
	}
	actor := actorFrom(r)

	res, err := s.st.ClaimNext(r.Context(), store.ClaimNextOpts{
		ProjectID:   req.Project,
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
		writeJSON(w, http.StatusOK, map[string]any{"claimed": false, "reason": "no-ready-task"})
		return
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, map[string]any{
			"claimed": false,
			"dry_run": true,
			"task":    s.toTaskPickJSON(res.Task, res.FanOut, nil),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"claimed": true,
		"task":    s.toTaskPickJSON(res.Task, res.FanOut, res.Lease),
	})
}

type renewRequest struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// renewLease handles POST /api/v1/tasks/{id}/renew.
func (s *server) renewLease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req renewRequest
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
	writeJSON(w, http.StatusOK, toTaskJSON(t))
}

// doneTask handles POST /api/v1/tasks/{id}/done: in_review -> merged, closing
// any active lease in the same transaction.
func (s *server) doneTask(w http.ResponseWriter, r *http.Request) {
	s.finishTask(w, r, "task.done",
		func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
			return store.Transition(tx, now, taskID, "in_review", "merged", eventID)
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
			return store.Transition(tx, now, taskID, cur, "ready", eventID)
		})
}
