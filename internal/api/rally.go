// rally.go serves a project's open rally — the thin HTTP skin over
// store.OpenRally and store.BlockerTree, which is where the reads live.
package api

import (
	"net/http"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// getProjectRally handles GET /api/v1/projects/{id}/rally: the project's
// open rally task plus the transitive tree of open tasks it is waiting on.
// 404 when the project has no open rally — store.OpenRally reports that as
// ErrNotFound, the same sentinel a missing project would produce, so this
// does not distinguish the two.
func (s *server) getProjectRally(w http.ResponseWriter, r *http.Request) {
	rally, err := s.st.OpenRally(r.Context(), r.PathValue("id"))
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	tree, err := s.st.BlockerTree(r.Context(), rally.ID)
	if err != nil {
		s.mapStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, model.Rally{Task: *rally, Blockers: tree})
}
