package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// clauseEdgeReq reads the edge body and both clause refs, or answers 400.
func clauseEdgeReq(w http.ResponseWriter, r *http.Request) (from designdoc.ClauseRef, to designdoc.ClauseRef, req model.ClauseEdgeInput, ok bool) {
	from, ok = clauseRef(w, r)
	if !ok {
		return
	}
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return from, to, req, false
	}
	to, ok = designdoc.ParseClauseRef(req.To)
	if !ok {
		writeErr(w, http.StatusBadRequest, "to must look like WL-CL-12")
		return from, to, req, false
	}
	return from, to, req, true
}

// linkClause handles POST /api/v1/clauses/{id}/edges: an architect relates
// two clauses (12-spec-refactoring-design-tree.md S12).
func (s *server) linkClause(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := clauseEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "clause.linked",
		map[string]string{"from": r.PathValue("id"), "to": req.To, "type": req.Type},
		func(tx *sql.Tx, _ int64) error {
			fromID, err := store.ClauseIDByRef(tx, from.Key, from.Number)
			if err != nil {
				return err
			}
			toID, err := store.ClauseIDByRef(tx, to.Key, to.Number)
			if err != nil {
				return err
			}
			return store.LinkClauses(tx, fromID, toID, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// unlinkClause handles DELETE /api/v1/clauses/{id}/edges.
func (s *server) unlinkClause(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := clauseEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "clause.unlinked",
		map[string]string{"from": r.PathValue("id"), "to": req.To, "type": req.Type},
		func(tx *sql.Tx, _ int64) error {
			fromID, err := store.ClauseIDByRef(tx, from.Key, from.Number)
			if err != nil {
				return err
			}
			toID, err := store.ClauseIDByRef(tx, to.Key, to.Number)
			if err != nil {
				return err
			}
			return store.UnlinkClauses(tx, fromID, toID, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
