package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// assignmentAction is the shared body of the four assignment endpoints: one
// recorded event, the outcome counted under its action label, then the
// updated task. They differ only in the event they record, the label they
// count under, and what apply does — everything else was copied four times.
func (s *server) assignmentAction(w http.ResponseWriter, r *http.Request,
	id, eventType, action string, payload any,
	apply func(tx *sql.Tx, eventID int64) error) {
	if err := s.recordEvent(r.Context(), "cli", eventType, payload, apply); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeAssignment(action)

	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// assignTask handles POST /api/v1/tasks/{id}/assign: sets the task's
// assignee to the request body's assignee, or the caller when omitted. This
// is the human-ownership counterpart to claim — it does not take a lease or
// change state.
func (s *server) assignTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.AssignInput
	if err := readOptionalJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	assignee := req.Assignee
	if assignee == "" {
		assignee = actorFrom(r).ID
	}
	s.assignmentAction(w, r, id, "task.assigned", "assign",
		map[string]string{"task": id, "assignee": assignee},
		func(tx *sql.Tx, eventID int64) error {
			return store.AssignTask(tx, s.st.Now(), id, assignee, eventID)
		})
}

// unassignTask handles POST /api/v1/tasks/{id}/unassign: clears the task's
// assignee. No body.
func (s *server) unassignTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.assignmentAction(w, r, id, "task.unassigned", "unassign",
		map[string]string{"task": id},
		func(tx *sql.Tx, eventID int64) error {
			return store.UnassignTask(tx, s.st.Now(), id, eventID)
		})
}

// startTask handles POST /api/v1/tasks/{id}/start: moves a ready task to
// in_progress on behalf of the caller without taking a lease, auto-assigning
// the caller when the task is unassigned (see store.StartTask). No body.
func (s *server) startTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := actorFrom(r)
	s.assignmentAction(w, r, id, "task.started", "start",
		map[string]string{"task": id, "actor": actor.ID},
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.StartTask(tx, s.st.Now(), id, actor.ID, eventID)
			return err
		})
}

// stopTask handles POST /api/v1/tasks/{id}/stop: moves an in_progress task
// assigned to the caller back to ready, the assignment-based counterpart to
// release (see store.StopTask). A task held by an active lease must be
// released through release instead: this 422s rather than touching the
// lease. No body.
func (s *server) stopTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	actor := actorFrom(r)
	s.assignmentAction(w, r, id, "task.stopped", "stop",
		map[string]string{"task": id, "actor": actor.ID},
		func(tx *sql.Tx, eventID int64) error {
			return store.StopTask(tx, s.st.Now(), id, actor.ID, eventID)
		})
}
