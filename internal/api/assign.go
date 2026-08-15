package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

// assignRequest is the optional body of POST .../assign: an empty or missing
// assignee defaults to the caller.
type assignRequest struct {
	Assignee string `json:"assignee"`
}

// assignTask handles POST /api/v1/tasks/{id}/assign: sets the task's
// assignee to the request body's assignee, or the caller when omitted. This
// is the human-ownership counterpart to claim — it does not take a lease or
// change state.
func (s *server) assignTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req assignRequest
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	actor := actorFrom(r)
	assignee := req.Assignee
	if assignee == "" {
		assignee = actor.ID
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"task": id, "assignee": assignee})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.assigned", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.AssignTask(tx, s.st.Now(), id, assignee, eventID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeAssignment("assign")

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// unassignTask handles POST /api/v1/tasks/{id}/unassign: clears the task's
// assignee. No body.
func (s *server) unassignTask(w http.ResponseWriter, r *http.Request) {
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
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.unassigned", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.UnassignTask(tx, s.st.Now(), id, eventID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeAssignment("unassign")

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// startTask handles POST /api/v1/tasks/{id}/start: moves a ready task to
// in_progress on behalf of the caller without taking a lease, auto-assigning
// the caller when the task is unassigned (see store.StartTask). No body.
func (s *server) startTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := actorFrom(r)

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"task": id, "actor": actor.ID})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.started", payload,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.StartTask(tx, s.st.Now(), id, actor.ID, eventID)
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeAssignment("start")

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// stopTask handles POST /api/v1/tasks/{id}/stop: moves an in_progress task
// assigned to the caller back to ready, the assignment-based counterpart to
// release (see store.StopTask). A task held by an active lease must be
// released through release instead: this 422s rather than touching the
// lease. No body.
func (s *server) stopTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := actorFrom(r)

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(map[string]string{"task": id, "actor": actor.ID})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.stopped", payload,
		func(tx *sql.Tx, eventID int64) error {
			return store.StopTask(tx, s.st.Now(), id, actor.ID, eventID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeAssignment("stop")

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}
