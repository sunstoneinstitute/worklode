package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// errDeriveRollback ends a dry run's transaction after its report is built.
var errDeriveRollback = errors.New("dry run: roll back")

// deriveRefs handles POST /api/v1/projects/{id}/refs/derive (WL-REQ-1860):
// normalize and re-extract the inline references of the selected nodes, each
// in its own transaction. A node that fails is reported with its error and
// leaves the rest running. DryRun and Diff roll every node back.
func (s *server) deriveRefs(w http.ResponseWriter, r *http.Request) {
	var req model.RefDeriveParams
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	switch req.Kind {
	case "doc", "task", "rule":
	default:
		writeErr(w, http.StatusBadRequest, "kind must be doc, task or rule")
		return
	}
	if len(req.Refs) == 0 && len(req.Status) == 0 && req.DocKind == "" && req.TaskKind == "" && !req.All {
		writeErr(w, http.StatusBadRequest, "select nodes: give refs, a filter or all")
		return
	}
	project := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), project); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	ids, err := s.st.DeriveTargets(r.Context(), project, req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	write := !req.DryRun && !req.Diff
	actor := actorIDFrom(r)
	report := model.RefDeriveReport{Nodes: []model.RefDeriveNode{}}
	for _, id := range ids {
		var node model.RefDeriveNode
		extID, err := randomExternalID()
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		payload, err := json.Marshal(map[string]any{"project": project, "kind": req.Kind, "id": id, "actor": actor})
		if err != nil {
			s.mapStoreErr(w, err)
			return
		}
		now := s.st.Now()
		_, _, err = s.st.RecordEvent(r.Context(), docSource, extID, "refs.derived", payload,
			func(tx *sql.Tx, eventID int64) error {
				var err error
				if node, err = store.DeriveNode(tx, now, project, req.Kind, id, eventID); err != nil {
					return err
				}
				if !write {
					return errDeriveRollback
				}
				return nil
			})
		if err != nil && !errors.Is(err, errDeriveRollback) {
			if node.Ref == "" {
				node.Ref = id
			}
			node.Error = err.Error()
		}
		s.observeRefsDerived(req.Kind, node, write)
		if !req.Diff {
			node.Diff = ""
		}
		if node.Added == nil {
			node.Added, node.Removed, node.Unresolved = []model.InlineRef{}, []model.InlineRef{}, []string{}
		}
		report.Nodes = append(report.Nodes, node)
	}
	writeJSON(w, http.StatusOK, report)
}

// inlinerefsRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var inlinerefsRouteDocs = map[string]routeDoc{
	"POST /api/v1/projects/{id}/refs/derive": {
		summary:   "Normalize and re-extract the inline references of the selected docs, tasks or rules",
		request:   model.RefDeriveParams{},
		responses: map[int]any{http.StatusOK: model.RefDeriveReport{}},
	},
}
