package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/designdoc"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// ruleEdgeReq reads the edge body and both rule refs, or answers 400.
func ruleEdgeReq(w http.ResponseWriter, r *http.Request) (from designdoc.RuleRef, to designdoc.RuleRef, req model.RuleEdgeInput, ok bool) {
	from, ok = ruleRef(w, r)
	if !ok {
		return
	}
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return from, to, req, false
	}
	to, ok = designdoc.ParseRuleRef(req.To)
	if !ok {
		writeErr(w, http.StatusBadRequest, "to must look like WL-RULE-12")
		return from, to, req, false
	}
	return from, to, req, true
}

// linkRule handles POST /api/v1/rules/{id}/edges: an architect relates
// two rules (12-spec-refactoring-design-tree.md S12).
func (s *server) linkRule(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := ruleEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "rule.linked",
		map[string]string{"from": r.PathValue("id"), "to": req.To, "type": req.Type},
		func(tx *sql.Tx, _ int64) error {
			fromID, err := store.RuleIDByRef(tx, from.Key, from.Number)
			if err != nil {
				return err
			}
			toID, err := store.RuleIDByRef(tx, to.Key, to.Number)
			if err != nil {
				return err
			}
			return store.LinkRules(tx, fromID, toID, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, req)
}

// unlinkRule handles DELETE /api/v1/rules/{id}/edges.
func (s *server) unlinkRule(w http.ResponseWriter, r *http.Request) {
	from, to, req, ok := ruleEdgeReq(w, r)
	if !ok {
		return
	}
	err := s.recordEvent(r.Context(), "cli", "rule.unlinked",
		map[string]string{"from": r.PathValue("id"), "to": req.To, "type": req.Type},
		func(tx *sql.Tx, _ int64) error {
			fromID, err := store.RuleIDByRef(tx, from.Key, from.Number)
			if err != nil {
				return err
			}
			toID, err := store.RuleIDByRef(tx, to.Key, to.Number)
			if err != nil {
				return err
			}
			return store.UnlinkRules(tx, fromID, toID, req.Type)
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
