package api

import (
	"database/sql"
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
	"github.com/sunstoneinstitute/worklode/internal/store"
)

// getChecklist handles GET /api/v1/tasks/{id}/checklist: the checklist items
// parsed out of the task's current body, in order of appearance.
func (s *server) getChecklist(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	t, err := s.st.GetTask(r.Context(), id)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.ParseChecklist(t.Body))
}

// setChecklistItem handles POST /api/v1/tasks/{id}/checklist: checks or
// unchecks one item, identified by ordinal (canonical) or title.
func (s *server) setChecklistItem(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req model.SetChecklistItemInput
	if err := readJSON(w, r, &req); err != nil {
		writeBodyErr(w, err)
		return
	}

	var item model.ChecklistItem
	err := s.recordTaskEvent(r.Context(), "cli", "task.checklist_set", id, req,
		func(tx *sql.Tx, _ int64) error {
			var err error
			item, err = store.SetChecklistItem(tx, s.st.Now(), id, req)
			return err
		})
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
