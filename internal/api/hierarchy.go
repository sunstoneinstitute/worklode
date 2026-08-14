package api

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/store"
)

type decomposeRequest struct {
	Into []string `json:"into"`
}

// decomposeResponse returns both halves of the split: the parent, keeping its
// id and its kind, and the children it now tracks.
type decomposeResponse struct {
	Parent   taskJSON   `json:"parent"`
	Children []taskJSON `json:"children"`
}

// decomposeTask handles POST /api/v1/tasks/{id}/decompose: create one draft
// child per title under the task, in one transaction. This is the supported
// way out of the spec-005 needs_decomposition gate. The parent's kind is not
// touched — the child_of edges are what make it a container (004 §6.10).
func (s *server) decomposeTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decomposeRequest
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}
	if len(req.Into) == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "into must list at least one child title")
		return
	}

	extID, err := randomExternalID()
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	payload, err := json.Marshal(req)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	actor := actorFrom(r)

	var children []store.Task
	_, _, err = s.st.RecordEvent(r.Context(), "cli", extID, "task.decomposed", payload,
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
	resp := decomposeResponse{Parent: toTaskJSON(parent), Children: make([]taskJSON, 0, len(children))}
	for i := range children {
		resp.Children = append(resp.Children, toTaskJSON(&children[i]))
	}
	writeJSON(w, http.StatusCreated, resp)
}
