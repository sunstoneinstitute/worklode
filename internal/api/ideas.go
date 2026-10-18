// ideas.go serves the /api/v1/ideas routes (WL-REQ-1799). Every handler
// passes the caller's actor id to the store, whose one visibility predicate
// decides what the caller may see or change.
package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// writeIdeaErr maps an ideas store error. A store.FieldError is 422 naming
// the field (WL-REQ-1811); any other ErrInvalidInput is the open subject's
// missing author, a 400. ErrNotShared carries the rule's exact message.
func (s *server) writeIdeaErr(w http.ResponseWriter, err error) {
	var fe *store.FieldError
	switch {
	case errors.As(err, &fe):
		writeJSON(w, http.StatusUnprocessableEntity, model.ErrorResponse{
			Error: "invalid idea", Fields: map[string]string{fe.Field: fe.Msg}})
	case errors.Is(err, store.ErrInvalidInput):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, store.ErrNotShared):
		writeErr(w, http.StatusConflict, store.ErrNotShared.Error())
	default:
		s.mapStoreErr(w, err)
	}
}

// listIdeas handles GET /api/v1/ideas.
func (s *server) listIdeas(w http.ResponseWriter, r *http.Request) {
	ideas, err := s.st.ListIdeas(r.Context(), actorIDFrom(r))
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	if ideas == nil {
		ideas = []model.Idea{}
	}
	writeJSON(w, http.StatusOK, model.IdeaListResponse{Ideas: ideas})
}

// createIdea handles POST /api/v1/ideas.
func (s *server) createIdea(w http.ResponseWriter, r *http.Request) {
	var in model.CreateIdeaInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	i, err := s.st.CreateIdea(r.Context(), actorIDFrom(r), in)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("create")
	writeJSON(w, http.StatusCreated, i)
}

// getIdea handles GET /api/v1/ideas/{id}.
func (s *server) getIdea(w http.ResponseWriter, r *http.Request) {
	i, err := s.st.GetIdea(r.Context(), actorIDFrom(r), r.PathValue("id"))
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, i)
}

// patchIdea handles PATCH /api/v1/ideas/{id}.
func (s *server) patchIdea(w http.ResponseWriter, r *http.Request) {
	var in model.PatchIdeaInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	i, err := s.st.PatchIdea(r.Context(), actorIDFrom(r), r.PathValue("id"), in)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("update")
	writeJSON(w, http.StatusOK, i)
}

// shareIdea handles POST /api/v1/ideas/{id}/share. With no intake project
// configured it is 409 before the store is asked anything.
func (s *server) shareIdea(w http.ResponseWriter, r *http.Request) {
	project, ok := s.intakeProject()
	if !ok {
		writeErr(w, http.StatusConflict, "intake project not configured")
		return
	}
	i, err := s.st.ShareIdea(r.Context(), actorIDFrom(r), r.PathValue("id"), project)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("share")
	writeJSON(w, http.StatusOK, i)
}

// deleteIdea handles DELETE /api/v1/ideas/{id}. The DeleteInput body is
// optional, and so is its justification (WL-REQ-1799).
func (s *server) deleteIdea(w http.ResponseWriter, r *http.Request) {
	var in model.DeleteInput
	if err := readOptionalJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	if err := s.st.DeleteIdea(r.Context(), actorIDFrom(r), r.PathValue("id"), in.Justification); err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("delete")
	w.WriteHeader(http.StatusNoContent)
}

// setIdeaIntake handles PATCH /api/v1/ideas/{id}/intake.
func (s *server) setIdeaIntake(w http.ResponseWriter, r *http.Request) {
	var in model.PatchIdeaIntakeInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	i, err := s.st.SetIdeaIntake(r.Context(), actorIDFrom(r), r.PathValue("id"), in)
	if err != nil {
		s.writeIdeaErr(w, err)
		return
	}
	s.observeIdeaWrite("intake")
	writeJSON(w, http.StatusOK, i)
}

// ideaRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var ideaRouteDocs = map[string]routeDoc{
	"GET /api/v1/ideas": {
		summary:   "List the caller's private ideas and every shared idea",
		responses: map[int]any{http.StatusOK: model.IdeaListResponse{}},
	},
	"POST /api/v1/ideas": {
		summary:   "Write a private idea",
		request:   model.CreateIdeaInput{},
		responses: map[int]any{http.StatusCreated: model.Idea{}},
	},
	"GET /api/v1/ideas/{id}": {
		summary:   "Read one idea",
		responses: map[int]any{http.StatusOK: model.Idea{}},
	},
	"PATCH /api/v1/ideas/{id}": {
		summary:   "Edit an idea (its author only)",
		request:   model.PatchIdeaInput{},
		responses: map[int]any{http.StatusOK: model.Idea{}},
	},
	"POST /api/v1/ideas/{id}/share": {
		summary:   "Share an idea into the intake project (its author only)",
		responses: map[int]any{http.StatusOK: model.Idea{}},
	},
	"DELETE /api/v1/ideas/{id}": {
		summary:   "Delete a private idea, or tombstone a shared one and its task",
		request:   model.DeleteInput{},
		responses: map[int]any{http.StatusNoContent: nil},
	},
	"PATCH /api/v1/ideas/{id}/intake": {
		summary:   "Set a shared idea's intake fields",
		request:   model.PatchIdeaIntakeInput{},
		responses: map[int]any{http.StatusOK: model.Idea{}},
	},
}
