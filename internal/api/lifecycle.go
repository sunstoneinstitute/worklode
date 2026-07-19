package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sunstoneinstitute/work-tracker/internal/store"
)

// leaseJSON is the wire form of a lease.
type leaseJSON struct {
	TaskID     string    `json:"task_id"`
	ActorID    string    `json:"actor_id"`
	SessionID  string    `json:"session_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	RenewedAt  time.Time `json:"renewed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

func toLeaseJSON(l *store.Lease) leaseJSON {
	return leaseJSON{
		TaskID:     l.TaskID,
		ActorID:    l.ActorID,
		SessionID:  l.SessionID,
		AcquiredAt: l.AcquiredAt,
		RenewedAt:  l.RenewedAt,
		ExpiresAt:  l.ExpiresAt,
	}
}

// SlugifyTitle turns a task title into a branch-name slug: lowercase, every
// run of non-alphanumeric (ASCII) characters becomes a single '-', leading
// and trailing '-' are trimmed, at most 40 characters, and "task" if nothing
// remains. Non-ASCII letters are treated as separators so slugs are always
// safe git branch components.
func SlugifyTitle(title string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if pendingDash && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingDash = false
			b.WriteRune(r)
		} else {
			pendingDash = true
		}
	}
	s := b.String()
	if len(s) > 40 {
		s = strings.TrimRight(s[:40], "-")
	}
	if s == "" {
		return "task"
	}
	return s
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
	SessionID  string `json:"session_id"`
	TTLSeconds int    `json:"ttl_seconds"`
}

// claimTask handles POST /api/v1/tasks/{id}/claim: lease the task to the
// caller and move it ready -> in_progress. A 409 for an already-leased task
// carries the current holder.
func (s *server) claimTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req claimRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)

	lease, err := s.st.Claim(r.Context(), id, actor.ID, req.SessionID,
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
		"branch": "wt/" + id + "-" + SlugifyTitle(t.Title),
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

// doneTask handles POST /api/v1/tasks/{id}/done: in_review -> done, closing
// any active lease in the same transaction.
func (s *server) doneTask(w http.ResponseWriter, r *http.Request) {
	s.finishTask(w, r, "task.done",
		func(tx *sql.Tx, now time.Time, taskID string, eventID int64) error {
			return store.Transition(tx, now, taskID, "in_review", "done", eventID)
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
