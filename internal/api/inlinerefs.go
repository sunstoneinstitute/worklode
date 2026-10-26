package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/inlineref"
	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// errDeriveRollback ends a dry run's transaction after its report is built.
var errDeriveRollback = errors.New("dry run: roll back")

// deriveRefs handles POST /api/v1/projects/{id}/refs/derive (WL-REQ-1860):
// normalize and re-extract the inline references of the selected nodes, each
// in its own transaction. A node that fails is reported with its error and
// leaves the rest running. DryRun and Diff roll every node back. Classify
// also runs the jev classifier per node between two transactions; without a
// TypeSafe key it fails before any node is touched. Limit and
// After page the selection so each request stays inside the client and
// ingress timeouts.
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
	if req.Reclassify && !req.Classify {
		writeErr(w, http.StatusBadRequest, "reclassify needs classify")
		return
	}
	if req.Classify && s.typesafe.APIKey == "" {
		writeErr(w, http.StatusServiceUnavailable, "classification not configured")
		return
	}
	if req.Limit < 0 {
		writeErr(w, http.StatusBadRequest, "limit must not be negative")
		return
	}
	project := r.PathValue("id")
	if _, err := s.st.GetProject(r.Context(), project); err != nil {
		s.mapStoreErr(w, err)
		return
	}
	ids, next, err := s.st.DeriveTargets(r.Context(), project, req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	write := !req.DryRun && !req.Diff
	actor := actorIDFrom(r)
	report := model.RefDeriveReport{Nodes: []model.RefDeriveNode{}, Next: next}
	for _, id := range ids {
		var node model.RefDeriveNode
		var batch store.JevBatch
		err := s.recordDerive(r, "refs.derived", project, req.Kind, id, actor, write, func(tx *sql.Tx, now time.Time, eventID int64) error {
			var err error
			if node, err = store.DeriveNode(tx, now, project, req.Kind, id, eventID); err != nil || !req.Classify {
				return err
			}
			batch, err = store.JevPending(tx, project, req.Kind, id, req.Reclassify)
			return err
		})
		if err == nil && len(batch.Refs) > 0 {
			err = s.classifyNode(r, project, req.Kind, id, actor, write, batch, &node)
		}
		if err != nil {
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
		if node.Classified == nil {
			node.Classified = []model.RefClassification{}
		}
		report.Nodes = append(report.Nodes, node)
	}
	writeJSON(w, http.StatusOK, report)
}

// classifyNode asks Jev about batch in one request outside any transaction,
// then records the answers and writes their typed edges in the node's own
// transaction, rolled back unless write (WL-REQ-1857, WL-REQ-1858). The
// classifications land in node.Classified.
func (s *server) classifyNode(r *http.Request, project string, kind model.NodeKind, id, actor string, write bool,
	batch store.JevBatch, node *model.RefDeriveNode) error {
	state, questions := inlineref.JevQuestions(batch.Node, batch.Refs)
	answers, err := s.typesafe.Ask(r.Context(), s.typesafeModel, state, questions)
	if err != nil {
		return err
	}
	var classified []model.RefClassification
	err = s.recordDerive(r, "refs.classified", project, kind, id, actor, write, func(tx *sql.Tx, now time.Time, eventID int64) error {
		var err error
		classified, err = store.ClassifyNode(tx, now, kind, id, batch, answers, s.typesafeModel, eventID)
		return err
	})
	if err != nil {
		return err
	}
	node.Classified = append(node.Classified, classified...)
	return nil
}

// recordDerive runs fn in one transaction under an event of type typ naming
// the node, and rolls it back unless write.
func (s *server) recordDerive(r *http.Request, typ, project string, kind model.NodeKind, id, actor string, write bool,
	fn func(tx *sql.Tx, now time.Time, eventID int64) error) error {
	extID, err := randomExternalID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"project": project, "kind": kind, "id": id, "actor": actor})
	if err != nil {
		return err
	}
	now := s.st.Now()
	_, _, err = s.st.RecordEvent(r.Context(), docSource, extID, typ, payload, func(tx *sql.Tx, eventID int64) error {
		if err := fn(tx, now, eventID); err != nil {
			return err
		}
		if !write {
			return errDeriveRollback
		}
		return nil
	})
	if errors.Is(err, errDeriveRollback) {
		return nil
	}
	return err
}

// inlinerefsRouteDocs documents the routes this file's handlers serve; see routeDoc in openapi.go.
var inlinerefsRouteDocs = map[string]routeDoc{
	"POST /api/v1/projects/{id}/refs/derive": {
		summary:   "Normalize and re-extract the inline references of the selected docs, tasks or rules",
		request:   model.RefDeriveParams{},
		responses: map[int]any{http.StatusOK: model.RefDeriveReport{}},
	},
}
