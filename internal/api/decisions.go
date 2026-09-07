// decisions.go serves 025 §10.1's posed question over the JSON API: reading
// the rows a task poses, adding one, editing an unanswered one, and recording
// the answer that closes it.
package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// listDecisions handles GET /api/v1/tasks/{id}/decisions: the questions the
// task poses, in authored order, empty while none are posed. An unknown task
// is a 404 rather than an empty list, so a mistyped id is visible.
func (s *server) listDecisions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.st.GetTask(r.Context(), id); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	rows, err := s.st.ListDecisions(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if rows == nil {
		rows = []model.Decision{}
	}
	writeJSON(w, http.StatusOK, rows)
}

// getDecision handles GET /api/v1/tasks/{id}/decisions/{key}: one row in
// full, with its answer and decider once it has been answered.
func (s *server) getDecision(w http.ResponseWriter, r *http.Request) {
	d, err := s.st.GetDecision(r.Context(), r.PathValue("id"), r.PathValue("key"))
	if err != nil {
		s.mapDecisionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// poseDecision handles POST /api/v1/tasks/{id}/decisions.
func (s *server) poseDecision(w http.ResponseWriter, r *http.Request) {
	var req model.DecisionInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if req.Task != "" {
		writeErr(w, http.StatusUnprocessableEntity, "task is not settable when posing; the path names the task")
		return
	}
	d, err := s.st.AddDecision(r.Context(), r.PathValue("id"), actorIDFrom(r), req)
	if err != nil {
		s.mapDecisionErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// editDecision handles PATCH /api/v1/tasks/{id}/decisions/{key}. "task" in
// the body re-parents the row to another task.
func (s *server) editDecision(w http.ResponseWriter, r *http.Request) {
	var req model.DecisionInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	d, err := s.st.EditDecision(r.Context(), r.PathValue("id"), r.PathValue("key"), actorIDFrom(r), req)
	if err != nil {
		s.mapDecisionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// decideDecision handles POST /api/v1/tasks/{id}/decisions/{key}/decide: the
// answer to one posed question. On a decision-kind task whose last open row
// this was, the same write closes the task.
func (s *server) decideDecision(w http.ResponseWriter, r *http.Request) {
	var req model.DecisionAnswer
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	d, err := s.st.RecordDecision(r.Context(), r.PathValue("id"), r.PathValue("key"), actorIDFrom(r), req)
	if err != nil {
		s.mapDecisionErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// mapDecisionErr is mapStoreErr with the two refusals this feature reports
// as conflicts rather than as unprocessable input: a key already used on the
// task, and an edit of an answered row. Both name a row that is already
// there and cannot be written over, which is what 409 says.
func (s *server) mapDecisionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrDecisionExists), errors.Is(err, store.ErrBadTransition):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		s.mapStoreErr(w, err)
	}
}
