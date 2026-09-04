// decisions.go serves 025 §10.1's posed question over the JSON API: adding
// a row to a task, and editing an unanswered one. Recording an answer is a
// different write with a different rule and does not live here.
package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

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
