// projectdocuments.go serves a project's documents register (WL-REQ-1798)
// over /api/v1.
package api

import (
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// mapProjectDocumentErr maps a register write's error. A rejected value is a
// 400 here (WL-REQ-1798), where other routes answer ErrInvalidInput with 422.
func (s *server) mapProjectDocumentErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrInvalidInput) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mapStoreErr(w, err)
}

// listProjectDocuments handles GET /api/v1/projects/{id}/documents.
func (s *server) listProjectDocuments(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), projectID); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	docs, err := s.st.ListProjectDocuments(r.Context(), projectID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ProjectDocumentListResponse{Documents: docs})
}

// createProjectDocument handles POST /api/v1/projects/{id}/documents.
func (s *server) createProjectDocument(w http.ResponseWriter, r *http.Request) {
	var in model.CreateProjectDocumentInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	d, err := s.st.CreateProjectDocument(r.Context(), store.ProjectOwner(r.PathValue("id")), in, actorIDFrom(r))
	if err != nil {
		s.mapProjectDocumentErr(w, err)
		return
	}
	s.observeProjectDocumentWrite("create")
	writeJSON(w, http.StatusCreated, d)
}

// patchProjectDocument handles PATCH /api/v1/documents/{id}.
func (s *server) patchProjectDocument(w http.ResponseWriter, r *http.Request) {
	var in model.PatchProjectDocumentInput
	if err := readJSON(w, r, &in); err != nil {
		writeBodyErr(w, err)
		return
	}
	d, err := s.st.PatchProjectDocument(r.Context(), r.PathValue("id"), in, actorIDFrom(r))
	if err != nil {
		s.mapProjectDocumentErr(w, err)
		return
	}
	s.observeProjectDocumentWrite("update")
	writeJSON(w, http.StatusOK, d)
}

// deleteProjectDocument handles DELETE /api/v1/documents/{id}.
func (s *server) deleteProjectDocument(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DeleteProjectDocument(r.Context(), r.PathValue("id"), actorIDFrom(r)); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	s.observeProjectDocumentWrite("delete")
	w.WriteHeader(http.StatusNoContent)
}

// projectDocumentRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var projectDocumentRouteDocs = map[string]routeDoc{
	"GET /api/v1/projects/{id}/documents": {
		summary:   "List a project's documents register",
		responses: map[int]any{http.StatusOK: model.ProjectDocumentListResponse{}},
	},
	"POST /api/v1/projects/{id}/documents": {
		summary:   "Register a document on a project",
		request:   model.CreateProjectDocumentInput{},
		responses: map[int]any{http.StatusCreated: model.ProjectDocument{}},
	},
	"PATCH /api/v1/documents/{id}": {
		summary:   "Update a register document",
		request:   model.PatchProjectDocumentInput{},
		responses: map[int]any{http.StatusOK: model.ProjectDocument{}},
	},
	"DELETE /api/v1/documents/{id}": {
		summary:   "Remove a register document",
		responses: map[int]any{http.StatusNoContent: nil},
	},
}
