package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// clauseRef reads the {id} path value as a clause ref such as WL-CL-12.
func clauseRef(w http.ResponseWriter, r *http.Request) (designdoc.ClauseRef, bool) {
	ref, ok := designdoc.ParseClauseRef(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "clause id must look like WL-CL-12")
		return designdoc.ClauseRef{}, false
	}
	return ref, true
}

// getClause handles GET /api/v1/clauses/{id}, where {id} is a clause ref
// such as WL-CL-12.
func (s *server) getClause(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	c, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// editClause handles PUT /api/v1/clauses/{id}: a clause-first write of the
// heading and body (S14, S35). The store regenerates the arranging
// document's body and writes it through the document path, so the event is
// recorded against that document.
func (s *server) editClause(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	var req model.EditClauseInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	current, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	if len(current.ArrangedIn) != 1 {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("clause %s is arranged in %d documents; edit the document instead", current.Ref, len(current.ArrangedIn)))
		return
	}
	now := s.st.Now()
	err = s.recordDocEvent(w, r, "clause_edit", "doc.clause_edited", current.ArrangedIn[0].Doc, req,
		func(tx *sql.Tx, eventID int64) error {
			_, err := store.EditClause(tx, now, ref.Key, ref.Number, req, actorIDFrom(r), eventID)
			return err
		})
	if err != nil {
		return
	}
	c, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// patchClause handles PATCH /api/v1/clauses/{id}: owner and tags (S15). The
// text is PUT; this is the metadata write, the way docs split body from owner.
func (s *server) patchClause(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	var req model.ClauseMetaInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	payload := map[string]any{"clause": r.PathValue("id"), "owner": req.Owner, "tags": req.Tags}
	err := s.recordEvent(r.Context(), "cli", "clause.updated", payload,
		func(tx *sql.Tx, _ int64) error {
			id, err := store.ClauseIDByRef(tx, ref.Key, ref.Number)
			if err != nil {
				return err
			}
			return store.SetClauseMeta(tx, id, req)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	c, err := s.st.GetClause(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// listClauseVersions handles GET /api/v1/clauses/{id}/versions.
func (s *server) listClauseVersions(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	vs, err := s.st.ListClauseVersions(r.Context(), ref.Key, ref.Number)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

// getClauseVersion handles GET /api/v1/clauses/{id}/versions/{n}.
func (s *server) getClauseVersion(w http.ResponseWriter, r *http.Request) {
	ref, ok := clauseRef(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n <= 0 {
		writeErr(w, http.StatusBadRequest, "version must be a positive integer")
		return
	}
	c, err := s.st.GetClauseVersion(r.Context(), ref.Key, ref.Number, n)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
