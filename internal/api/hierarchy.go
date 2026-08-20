package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// decomposeTask handles POST /api/v1/tasks/{id}/decompose: create one draft
// child per title under the task, in one transaction. This is the supported
// way out of the spec-005 needs_decomposition gate. The parent's kind is not
// touched — the child_of edges are what make it a container (004 §6.10).
func (s *server) decomposeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.DecomposeInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if len(req.Into) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "into must list at least one child title")
		return
	}

	actor := actorFrom(r)

	var children []model.Task
	err := s.recordEvent(r.Context(), "cli", "task.decomposed", req,
		func(tx *sql.Tx, eventID int64) error {
			var err error
			children, err = store.Decompose(tx, s.st.Now(), id, req.Into, actor.ID, eventID)
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}

	parent, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := model.DecomposeResponse{Parent: *parent, Children: children}
	writeJSON(w, http.StatusCreated, resp)
}
