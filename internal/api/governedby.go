package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// govern handles POST /api/v1/tasks/{id}/governed-by: the architect adds a
// governing rule to a task by hand (12-spec-refactoring-design-tree.md S3).
// Every change is an event on the task.
func (s *server) govern(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GovernInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	ref, ok := designdoc.ParseRuleRef(req.Rule)
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule must look like WL-REQ-12")
		return
	}
	err := s.recordTaskEvent(r.Context(), "cli", "task.governed", id, req,
		func(tx *sql.Tx, eventID int64) error {
			ruleID, err := store.RuleIDByRef(tx, ref.Key, ref.Number)
			if err != nil {
				return err
			}
			return store.Govern(tx, id, ruleID, "manual", req.Pin)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// ungovern handles DELETE /api/v1/tasks/{id}/governed-by.
func (s *server) ungovern(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.GovernInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	ref, ok := designdoc.ParseRuleRef(req.Rule)
	if !ok {
		writeErr(w, http.StatusBadRequest, "rule must look like WL-REQ-12")
		return
	}
	err := s.recordTaskEvent(r.Context(), "cli", "task.ungoverned", id, req,
		func(tx *sql.Tx, eventID int64) error {
			ruleID, err := store.RuleIDByRef(tx, ref.Key, ref.Number)
			if err != nil {
				return err
			}
			return store.Ungovern(tx, id, ruleID)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// governedbyRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var governedbyRouteDocs = map[string]routeDoc{
	"POST /api/v1/tasks/{id}/governed-by": {
		summary:   "Add a governing rule to a task",
		request:   model.GovernInput{},
		responses: map[int]any{http.StatusCreated: model.GovernInput{}},
	},
	"DELETE /api/v1/tasks/{id}/governed-by": {
		summary:   "Remove a governing rule from a task",
		request:   model.GovernInput{},
		responses: map[int]any{http.StatusNoContent: nil},
	},
}
