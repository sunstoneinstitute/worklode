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

// decomposeResponse returns both halves of the split: the converted epic,
// keeping its id, and the children it now tracks.
type decomposeResponse struct {
	Epic     taskJSON   `json:"epic"`
	Children []taskJSON `json:"children"`
}

// decomposeTask handles POST /api/v1/tasks/{id}/decompose: convert the task
// into an epic and create one draft child per title, in one transaction. This
// is the supported way out of the spec-005 needs_decomposition gate.
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

	epic, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	resp := decomposeResponse{Epic: toTaskJSON(epic), Children: make([]taskJSON, 0, len(children))}
	for i := range children {
		resp.Children = append(resp.Children, toTaskJSON(&children[i]))
	}
	writeJSON(w, http.StatusCreated, resp)
}
